package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AccessClaims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	issuer     string
	audience   string
	accessTTL  time.Duration
	now        func() time.Time
}

func NewTokenManager(privateKey ed25519.PrivateKey, issuer, audience string, accessTTL time.Duration) (*TokenManager, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key")
	}
	if !bytes.Equal(privateKey, ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])) {
		return nil, fmt.Errorf("inconsistent Ed25519 private key")
	}
	if issuer == "" || audience == "" {
		return nil, fmt.Errorf("JWT issuer and audience are required")
	}
	if accessTTL <= 0 {
		return nil, fmt.Errorf("access token TTL must be positive")
	}
	privateKey = append(ed25519.PrivateKey(nil), privateKey...)
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	return &TokenManager{
		privateKey: privateKey,
		publicKey:  publicKey,
		issuer:     issuer,
		audience:   audience,
		accessTTL:  accessTTL,
		now:        time.Now,
	}, nil
}

func ParsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode JWT private key: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		key := ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize])
		if !bytes.Equal(raw, key) {
			return nil, fmt.Errorf("JWT private key contains an inconsistent public key")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("JWT private key must be a base64 Ed25519 seed or private key")
	}
}

func (m *TokenManager) IssueAccess(userID, sessionID uuid.UUID) (string, time.Time, error) {
	now := m.now().UTC()
	expiresAt := now.Add(m.accessTTL)
	claims := AccessClaims{
		SessionID: sessionID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{m.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(m.privateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expiresAt, nil
}

func (m *TokenManager) ParseAccess(raw string) (*AccessClaims, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, ErrInvalidToken
	}
	claims := new(AccessClaims)
	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodEdDSA {
				return nil, ErrInvalidSigningMethod
			}
			return m.publicKey, nil
		},
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(m.now),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return nil, ErrInvalidToken
	}
	if _, err := uuid.Parse(claims.SessionID); err != nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func NewRefreshToken() (raw string, hash []byte, err error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(value)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

func HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func validRefreshToken(raw string) bool {
	if len(raw) != base64.RawURLEncoding.EncodedLen(32) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(decoded) == 32
}
