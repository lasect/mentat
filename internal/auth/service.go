package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"mentat/internal/appdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const dummyPasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$c3RhdGljLWR1bW15LXNhbHQ$48m04joQyHNKOlurJ9gNv5VzO2BLUfu5ZscmC3gMmEA"

type User struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	DisplayName   string    `json:"display_name"`
	AvatarURL     *string   `json:"avatar_url"`
	Providers     []string  `json:"providers"`
}

type SessionResult struct {
	User          User
	AccessToken   string
	AccessExpires time.Time
	RefreshToken  string
}

type OAuthProfile struct {
	Provider       string
	ProviderUserID string
	Email          string
	EmailVerified  bool
	DisplayName    string
	AvatarURL      string
}

type ClientInfo struct {
	UserAgent string
	IPAddress string
}

type LinkAuthorization struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

type Service struct {
	pool       *pgxpool.Pool
	queries    *appdb.Queries
	tokens     *TokenManager
	refreshTTL time.Duration
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, tokens *TokenManager, refreshTTL time.Duration) (*Service, error) {
	if pool == nil || tokens == nil {
		return nil, fmt.Errorf("database and token manager are required")
	}
	if refreshTTL <= 0 {
		return nil, fmt.Errorf("refresh token TTL must be positive")
	}
	return &Service{
		pool:       pool,
		queries:    appdb.New(pool),
		tokens:     tokens,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}, nil
}

func NormalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", fmt.Errorf("%w: invalid email address", ErrInvalidInput)
	}
	return value, nil
}

func NormalizeDisplayName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 120 {
		return "", fmt.Errorf("%w: display name must be at most 120 characters", ErrInvalidInput)
	}
	return value, nil
}

func (s *Service) Signup(ctx context.Context, email, password, displayName string, client ClientInfo) (SessionResult, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return SessionResult{}, err
	}
	displayName, err = NormalizeDisplayName(displayName)
	if err != nil {
		return SessionResult{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return SessionResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionResult{}, fmt.Errorf("begin signup: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	user, err := q.CreateUser(ctx, appdb.CreateUserParams{
		ID:          uuid.New(),
		Email:       normalized,
		DisplayName: displayName,
	})
	if isUniqueViolation(err) {
		return SessionResult{}, ErrEmailTaken
	}
	if err != nil {
		return SessionResult{}, fmt.Errorf("create user: %w", err)
	}
	if err := q.CreatePasswordCredential(ctx, appdb.CreatePasswordCredentialParams{
		UserID: user.ID, PasswordHash: hash,
	}); err != nil {
		return SessionResult{}, fmt.Errorf("create password credential: %w", err)
	}
	result, err := s.createSession(ctx, q, user, uuid.New(), client)
	if err != nil {
		return SessionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionResult{}, fmt.Errorf("commit signup: %w", err)
	}
	return result, nil
}

func (s *Service) Login(ctx context.Context, email, password string, client ClientInfo) (SessionResult, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		_, _ = VerifyPassword(dummyPasswordHash, password)
		return SessionResult{}, ErrInvalidCredentials
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionResult{}, fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	row, err := q.GetPasswordCredentialByEmail(ctx, normalized)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(dummyPasswordHash, password)
		return SessionResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionResult{}, fmt.Errorf("get password credential: %w", err)
	}
	matched, err := VerifyPassword(row.PasswordHash, password)
	if err != nil {
		return SessionResult{}, fmt.Errorf("verify stored password credential: %w", err)
	}
	if !matched {
		return SessionResult{}, ErrInvalidCredentials
	}
	user, err := q.LockUserByID(ctx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionResult{}, fmt.Errorf("lock login user: %w", err)
	}
	if user.DisabledAt.Valid {
		return SessionResult{}, ErrUserDisabled
	}
	result, err := s.createSession(ctx, q, user, uuid.New(), client)
	if err != nil {
		return SessionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionResult{}, fmt.Errorf("commit login: %w", err)
	}
	return result, nil
}

func (s *Service) Authenticate(ctx context.Context, rawAccess string) (User, uuid.UUID, error) {
	claims, err := s.tokens.ParseAccess(rawAccess)
	if err != nil {
		return User{}, uuid.Nil, err
	}
	sessionID, _ := uuid.Parse(claims.SessionID)
	userID, _ := uuid.Parse(claims.Subject)
	row, err := s.validateSession(ctx, userID, sessionID)
	if err != nil {
		return User{}, uuid.Nil, err
	}
	user, err := s.publicUser(ctx, s.queries, appdb.User{
		ID: row.ID, Email: row.Email, EmailVerifiedAt: row.EmailVerifiedAt,
		DisplayName: row.DisplayName, AvatarUrl: row.AvatarUrl, DisabledAt: row.DisabledAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
	return user, sessionID, err
}

func (s *Service) ValidateSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	_, err := s.validateSession(ctx, userID, sessionID)
	return err
}

func (s *Service) validateSession(ctx context.Context, userID, sessionID uuid.UUID) (appdb.GetSessionWithUserRow, error) {
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return appdb.GetSessionWithUserRow{}, ErrInvalidToken
	}
	row, err := s.queries.GetSessionWithUser(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return appdb.GetSessionWithUserRow{}, ErrSessionRevoked
	}
	if err != nil {
		return appdb.GetSessionWithUserRow{}, fmt.Errorf("get session: %w", err)
	}
	if row.ID != userID {
		return appdb.GetSessionWithUserRow{}, ErrInvalidToken
	}
	if row.SessionRevokedAt.Valid {
		return appdb.GetSessionWithUserRow{}, ErrSessionRevoked
	}
	if !row.SessionExpiresAt.Valid || !row.SessionExpiresAt.Time.After(s.now()) {
		return appdb.GetSessionWithUserRow{}, ErrSessionExpired
	}
	if row.DisabledAt.Valid {
		return appdb.GetSessionWithUserRow{}, ErrUserDisabled
	}
	return row, nil
}

func (s *Service) Refresh(ctx context.Context, rawRefresh string, client ClientInfo) (SessionResult, error) {
	if !validRefreshToken(rawRefresh) {
		return SessionResult{}, ErrInvalidToken
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionResult{}, fmt.Errorf("begin refresh: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	old, err := q.GetRefreshSessionByTokenHash(ctx, HashRefreshToken(rawRefresh))
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionResult{}, ErrInvalidToken
	}
	if err != nil {
		return SessionResult{}, fmt.Errorf("get refresh session: %w", err)
	}
	user, err := q.LockUserByID(ctx, old.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionResult{}, ErrInvalidToken
	} else if err != nil {
		return SessionResult{}, fmt.Errorf("lock refresh user: %w", err)
	}
	now := s.now().UTC()
	if old.RevokedAt.Valid {
		if err := q.RevokeRefreshFamily(ctx, appdb.RevokeRefreshFamilyParams{
			FamilyID: old.FamilyID, RevokedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			return SessionResult{}, fmt.Errorf("revoke replayed session family: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return SessionResult{}, fmt.Errorf("commit replay revocation: %w", err)
		}
		return SessionResult{}, ErrSessionRevoked
	}
	if !old.ExpiresAt.Valid || !old.ExpiresAt.Time.After(now) {
		return SessionResult{}, ErrSessionExpired
	}
	if user.DisabledAt.Valid {
		return SessionResult{}, ErrUserDisabled
	}
	newID := uuid.New()
	result, err := s.createSessionWithID(ctx, q, user, newID, old.FamilyID, client)
	if err != nil {
		return SessionResult{}, err
	}
	rotated, err := q.RotateRefreshSession(ctx, appdb.RotateRefreshSessionParams{
		ID: old.ID, RevokedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ReplacedBy: pgtype.UUID{Bytes: newID, Valid: true},
	})
	if err != nil {
		return SessionResult{}, fmt.Errorf("rotate refresh session: %w", err)
	}
	if rotated != 1 {
		return SessionResult{}, ErrSessionRevoked
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionResult{}, fmt.Errorf("commit refresh: %w", err)
	}
	return result, nil
}

func (s *Service) Logout(ctx context.Context, rawRefresh string) error {
	if rawRefresh == "" {
		return nil
	}
	if !validRefreshToken(rawRefresh) {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin logout: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	session, err := q.GetRefreshSessionByTokenHash(ctx, HashRefreshToken(rawRefresh))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get logout session: %w", err)
	}
	if _, err := q.LockUserByID(ctx, session.UserID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("lock logout user: %w", err)
	}
	if err := q.RevokeRefreshFamily(ctx, appdb.RevokeRefreshFamilyParams{
		FamilyID:  session.FamilyID,
		RevokedAt: pgtype.Timestamptz{Time: s.now().UTC(), Valid: true},
	}); err != nil {
		return fmt.Errorf("revoke logout session family: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit logout: %w", err)
	}
	return nil
}

func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin logout all: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	if _, err := q.LockUserByID(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	} else if err != nil {
		return fmt.Errorf("lock logout-all user: %w", err)
	}
	if err := q.RevokeAllUserSessions(ctx, appdb.RevokeAllUserSessionsParams{
		UserID: userID, RevokedAt: pgtype.Timestamptz{Time: s.now().UTC(), Valid: true},
	}); err != nil {
		return fmt.Errorf("revoke all user sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit logout all: %w", err)
	}
	return nil
}

func (s *Service) OAuthLogin(ctx context.Context, profile OAuthProfile, link *LinkAuthorization, client ClientInfo) (SessionResult, error) {
	if profile.Provider != "github" && profile.Provider != "google" {
		return SessionResult{}, fmt.Errorf("unsupported OAuth provider")
	}
	profile.ProviderUserID = strings.TrimSpace(profile.ProviderUserID)
	if profile.ProviderUserID == "" || len(profile.ProviderUserID) > 255 {
		return SessionResult{}, ErrInvalidCredentials
	}
	if strings.TrimSpace(profile.Email) == "" {
		return SessionResult{}, ErrEmailNotAvailable
	}
	providerEmail, providerEmailErr := NormalizeEmail(profile.Email)
	if link == nil && (providerEmailErr != nil || !profile.EmailVerified) {
		return SessionResult{}, ErrVerifiedEmailNeeded
	}
	if providerEmailErr != nil {
		return SessionResult{}, ErrInvalidCredentials
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionResult{}, fmt.Errorf("begin OAuth login: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	if link != nil {
		if err := s.validateLinkSession(ctx, q, *link); err != nil {
			return SessionResult{}, err
		}
	}

	identity, identityErr := q.GetOAuthIdentity(ctx, appdb.GetOAuthIdentityParams{
		Provider: profile.Provider, ProviderUserID: profile.ProviderUserID,
	})
	if identityErr == nil {
		if link != nil && identity.UserID != link.UserID {
			return SessionResult{}, ErrIdentityConflict
		}
		user, err := q.LockUserByID(ctx, identity.UserID)
		if err != nil {
			return SessionResult{}, fmt.Errorf("get OAuth user: %w", err)
		}
		if user.DisabledAt.Valid {
			return SessionResult{}, ErrUserDisabled
		}
		result, err := s.createSession(ctx, q, user, uuid.New(), client)
		if err != nil {
			return SessionResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SessionResult{}, fmt.Errorf("commit OAuth login: %w", err)
		}
		return result, nil
	}
	if !errors.Is(identityErr, pgx.ErrNoRows) {
		return SessionResult{}, fmt.Errorf("get OAuth identity: %w", identityErr)
	}

	var user appdb.User
	if link != nil {
		user, err = q.GetUserByID(ctx, link.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionResult{}, ErrInvalidCredentials
		}
	} else {
		user, err = q.GetUserByEmail(ctx, providerEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			verifiedAt := pgtype.Timestamptz{Time: s.now().UTC(), Valid: true}
			avatar := providerAvatarURL(profile.AvatarURL)
			user, err = q.CreateUser(ctx, appdb.CreateUserParams{
				ID: uuid.New(), Email: providerEmail, EmailVerifiedAt: verifiedAt,
				DisplayName: providerDisplayName(profile.DisplayName), AvatarUrl: avatar,
			})
			if isUniqueViolation(err) {
				return SessionResult{}, ErrAccountLinkRequired
			}
		} else if err == nil {
			return SessionResult{}, ErrAccountLinkRequired
		}
	}
	if err != nil {
		return SessionResult{}, fmt.Errorf("resolve OAuth user: %w", err)
	}
	if user.DisabledAt.Valid {
		return SessionResult{}, ErrUserDisabled
	}

	if _, err := q.CreateOAuthIdentity(ctx, appdb.CreateOAuthIdentityParams{
		ID: uuid.New(), UserID: user.ID, Provider: profile.Provider,
		ProviderUserID: profile.ProviderUserID, ProviderEmail: providerEmail,
		EmailVerified: profile.EmailVerified && providerEmail != "",
	}); isUniqueViolation(err) {
		return SessionResult{}, ErrIdentityConflict
	} else if err != nil {
		return SessionResult{}, fmt.Errorf("create OAuth identity: %w", err)
	}
	if profile.EmailVerified && providerEmail == user.Email {
		if err := q.MarkUserEmailVerified(ctx, appdb.MarkUserEmailVerifiedParams{
			ID: user.ID, EmailVerifiedAt: pgtype.Timestamptz{Time: s.now().UTC(), Valid: true},
		}); err != nil {
			return SessionResult{}, fmt.Errorf("mark OAuth email verified: %w", err)
		}
	}
	if err := q.UpdateUserProfileFromOAuth(ctx, appdb.UpdateUserProfileFromOAuthParams{
		ID: user.ID, DisplayName: providerDisplayName(profile.DisplayName), AvatarUrl: providerAvatarURL(profile.AvatarURL),
	}); err != nil {
		return SessionResult{}, fmt.Errorf("update OAuth profile: %w", err)
	}
	user, err = q.GetUserByID(ctx, user.ID)
	if err != nil {
		return SessionResult{}, fmt.Errorf("reload OAuth user: %w", err)
	}
	result, err := s.createSession(ctx, q, user, uuid.New(), client)
	if err != nil {
		return SessionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionResult{}, fmt.Errorf("commit OAuth login: %w", err)
	}
	return result, nil
}

func (s *Service) validateLinkSession(ctx context.Context, q *appdb.Queries, link LinkAuthorization) error {
	if link.UserID == uuid.Nil || link.SessionID == uuid.Nil {
		return ErrInvalidToken
	}
	user, err := q.LockUserByID(ctx, link.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionRevoked
	}
	if err != nil {
		return fmt.Errorf("lock OAuth link user: %w", err)
	}
	if user.DisabledAt.Valid {
		return ErrUserDisabled
	}
	row, err := q.LockSessionWithUser(ctx, link.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionRevoked
	}
	if err != nil {
		return fmt.Errorf("lock OAuth link session: %w", err)
	}
	if row.ID != link.UserID {
		return ErrInvalidToken
	}
	if row.SessionRevokedAt.Valid {
		return ErrSessionRevoked
	}
	if !row.SessionExpiresAt.Valid || !row.SessionExpiresAt.Time.After(s.now()) {
		return ErrSessionExpired
	}
	return nil
}

func (s *Service) createSession(ctx context.Context, q *appdb.Queries, user appdb.User, familyID uuid.UUID, client ClientInfo) (SessionResult, error) {
	return s.createSessionWithID(ctx, q, user, uuid.New(), familyID, client)
}

func (s *Service) createSessionWithID(ctx context.Context, q *appdb.Queries, user appdb.User, sessionID, familyID uuid.UUID, client ClientInfo) (SessionResult, error) {
	raw, hash, err := NewRefreshToken()
	if err != nil {
		return SessionResult{}, err
	}
	_, err = q.CreateRefreshSession(ctx, appdb.CreateRefreshSessionParams{
		ID: sessionID, FamilyID: familyID, UserID: user.ID, TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().UTC().Add(s.refreshTTL), Valid: true},
		UserAgent: truncate(client.UserAgent, 512), IpAddress: truncate(client.IPAddress, 64),
	})
	if err != nil {
		return SessionResult{}, fmt.Errorf("create refresh session: %w", err)
	}
	access, expires, err := s.tokens.IssueAccess(user.ID, sessionID)
	if err != nil {
		return SessionResult{}, err
	}
	public, err := s.publicUser(ctx, q, user)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{User: public, AccessToken: access, AccessExpires: expires, RefreshToken: raw}, nil
}

func (s *Service) publicUser(ctx context.Context, q *appdb.Queries, user appdb.User) (User, error) {
	providers, err := q.ListOAuthProvidersForUser(ctx, user.ID)
	if err != nil {
		return User{}, fmt.Errorf("list OAuth providers: %w", err)
	}
	hasPassword, err := q.HasPasswordCredentialForUser(ctx, user.ID)
	if err != nil {
		return User{}, fmt.Errorf("check password provider: %w", err)
	}
	if hasPassword {
		providers = append([]string{"password"}, providers...)
	}
	return User{
		ID: user.ID, Email: user.Email, EmailVerified: user.EmailVerifiedAt.Valid,
		DisplayName: user.DisplayName, AvatarURL: user.AvatarUrl, Providers: providers,
	}, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func providerAvatarURL(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil
	}
	return &value
}

func truncate(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

func providerDisplayName(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "\uFFFD"))
	runes := []rune(value)
	if len(runes) > 120 {
		runes = runes[:120]
	}
	return string(runes)
}
