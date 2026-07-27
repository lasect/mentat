package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServiceCredentialAndSessionLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TETRA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TETRA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(746387214)"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := lock.Exec(context.Background(), "SELECT pg_advisory_unlock(746387214)"); err != nil {
			t.Errorf("release integration-test lock: %v", err)
		}
	}()
	if _, err := pool.Exec(ctx, "TRUNCATE refresh_sessions, oauth_identities, password_credentials, users CASCADE"); err != nil {
		t.Fatal(err)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := NewTokenManager(privateKey, "integration-test", "tetra-test", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, tokens, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client := ClientInfo{UserAgent: "integration-test", IPAddress: "192.0.2.10"}
	password := "correct horse battery staple"

	signup, err := service.Signup(ctx, "Person@Example.com", password, "Person", client)
	if err != nil {
		t.Fatal(err)
	}
	if signup.User.Email != "person@example.com" || signup.User.EmailVerified {
		t.Fatalf("unexpected signup user: %#v", signup.User)
	}
	if len(signup.User.Providers) != 1 || signup.User.Providers[0] != "password" {
		t.Fatalf("unexpected providers: %#v", signup.User.Providers)
	}
	if _, _, err := service.Authenticate(ctx, signup.AccessToken); err != nil {
		t.Fatalf("authenticate signup token: %v", err)
	}
	if _, err := service.Signup(ctx, "person@example.com", password, "", client); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate signup error = %v", err)
	}
	if _, err := service.Login(ctx, "person@example.com", "wrong password value", client); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid login error = %v", err)
	}

	refreshed, err := service.Refresh(ctx, signup.RefreshToken, client)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken == signup.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Refresh(ctx, signup.RefreshToken, client); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("replayed refresh error = %v", err)
	}
	if _, _, err := service.Authenticate(ctx, refreshed.AccessToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("family token after replay error = %v", err)
	}

	rotatingLogin, err := service.Login(ctx, "person@example.com", password, client)
	if err != nil {
		t.Fatal(err)
	}
	rotatedLogin, err := service.Refresh(ctx, rotatingLogin.RefreshToken, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, rotatingLogin.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Authenticate(ctx, rotatedLogin.AccessToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("rotated access after family logout error = %v", err)
	}

	login, err := service.Login(ctx, "person@example.com", password, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, login.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateSession(ctx, login.User.ID, mustSessionID(t, service, login.AccessToken)); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("validate session after logout error = %v", err)
	}
	if _, _, err := service.Authenticate(ctx, login.AccessToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("access after logout error = %v", err)
	}

	firstDevice, err := service.Login(ctx, "person@example.com", password, client)
	if err != nil {
		t.Fatal(err)
	}
	secondDevice, err := service.Login(ctx, "person@example.com", password, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.LogoutAll(ctx, signup.User.ID); err != nil {
		t.Fatal(err)
	}
	for _, accessToken := range []string{firstDevice.AccessToken, secondDevice.AccessToken} {
		if _, _, err := service.Authenticate(ctx, accessToken); !errors.Is(err, ErrSessionRevoked) {
			t.Fatalf("access after logout-all error = %v", err)
		}
	}

	profile := OAuthProfile{
		Provider: "google", ProviderUserID: "google-user-1",
		Email: "person@example.com", EmailVerified: true,
	}
	if _, err := service.OAuthLogin(ctx, profile, nil, client); !errors.Is(err, ErrAccountLinkRequired) {
		t.Fatalf("unauthed OAuth account linking error = %v", err)
	}
	linkSession, err := service.Login(ctx, "person@example.com", password, client)
	if err != nil {
		t.Fatal(err)
	}
	link := &LinkAuthorization{
		UserID: signup.User.ID, SessionID: mustSessionID(t, service, linkSession.AccessToken),
	}
	if _, err := service.OAuthLogin(ctx, profile, link, client); err != nil {
		t.Fatalf("authenticated OAuth account linking: %v", err)
	}
	if err := service.Logout(ctx, linkSession.RefreshToken); err != nil {
		t.Fatal(err)
	}
	secondProfile := profile
	secondProfile.ProviderUserID = "google-user-2"
	if _, err := service.OAuthLogin(ctx, secondProfile, link, client); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("OAuth linking with revoked session error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET disabled_at = now() WHERE id = $1", signup.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OAuthLogin(ctx, profile, nil, client); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled OAuth login error = %v", err)
	}
}

func mustSessionID(t *testing.T, service *Service, accessToken string) uuid.UUID {
	t.Helper()
	claims, err := service.tokens.ParseAccess(accessToken)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return sessionID
}
