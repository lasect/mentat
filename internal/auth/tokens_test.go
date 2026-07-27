package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccessTokenRoundTripAndValidation(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewTokenManager(privateKey, "test-issuer", "test-audience", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	userID := uuid.New()
	sessionID := uuid.New()
	raw, expires, err := manager.IssueAccess(userID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires = %v", expires)
	}
	claims, err := manager.ParseAccess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != userID.String() || claims.SessionID != sessionID.String() {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	manager.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := manager.ParseAccess(raw); err != ErrInvalidToken {
		t.Fatalf("expired ParseAccess() error = %v", err)
	}
}

func TestAccessTokenRejectsWrongAudience(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer, _ := NewTokenManager(privateKey, "issuer", "audience-a", time.Minute)
	verifier, _ := NewTokenManager(privateKey, "issuer", "audience-b", time.Minute)
	raw, _, err := issuer.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.ParseAccess(raw); err != ErrInvalidToken {
		t.Fatalf("ParseAccess() error = %v", err)
	}
}

func TestTokenManagerOwnsKeyMaterial(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewTokenManager(privateKey, "issuer", "audience", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for index := range privateKey {
		privateKey[index] = 0
	}
	raw, _, err := manager.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseAccess(raw); err != nil {
		t.Fatalf("token manager key changed with caller buffer: %v", err)
	}
}

func TestRefreshTokensAreRandomAndHashable(t *testing.T) {
	first, firstHash, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || string(firstHash) == string(secondHash) {
		t.Fatal("refresh tokens were not unique")
	}
	if string(firstHash) != string(HashRefreshToken(first)) {
		t.Fatal("refresh token hash is not stable")
	}
	if !validRefreshToken(first) {
		t.Fatal("generated refresh token was rejected")
	}
	for _, malformed := range []string{"", "short", strings.Repeat("!", 43), first + "x"} {
		if validRefreshToken(malformed) {
			t.Fatalf("malformed refresh token %q was accepted", malformed)
		}
	}
}

func TestAccessTokenRejectsOversizedInput(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewTokenManager(privateKey, "issuer", "audience", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseAccess(strings.Repeat("a", 4097)); err != ErrInvalidToken {
		t.Fatalf("ParseAccess() error = %v", err)
	}
}

func TestParsePrivateKey(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	key, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != ed25519.PrivateKeySize {
		t.Fatalf("private key length = %d", len(key))
	}
	if _, err := ParsePrivateKey("not-base64"); err == nil {
		t.Fatal("invalid key parsed")
	}
	inconsistent := append(ed25519.NewKeyFromSeed(seed)[:ed25519.SeedSize], make([]byte, ed25519.PublicKeySize)...)
	if _, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(inconsistent)); err == nil {
		t.Fatal("inconsistent private key parsed")
	}
}
