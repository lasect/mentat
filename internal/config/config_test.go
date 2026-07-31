package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestValidateWebURL(t *testing.T) {
	for _, candidate := range []string{
		"https://app.mentat.example/auth/callback",
		"http://localhost:5173/auth/callback",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		if _, err := validateWebURL(candidate); err != nil {
			t.Fatalf("validateWebURL(%q): %v", candidate, err)
		}
	}
	for _, candidate := range []string{
		"http://app.mentat.example",
		"ftp://localhost/resource",
		"https://user@example.com",
		"not-a-url",
	} {
		if _, err := validateWebURL(candidate); err == nil {
			t.Fatalf("validateWebURL(%q) succeeded", candidate)
		}
	}
}

func TestLoadAPIRejectsInsecurePublicCookies(t *testing.T) {
	setValidAPIEnv(t)
	t.Setenv("MENTAT_COOKIE_SECURE", "false")
	t.Setenv("MENTAT_FRONTEND_AUTH_CALLBACK_URL", "https://app.mentat.example/auth/callback")
	if _, err := LoadAPI(); err == nil || !strings.Contains(err.Error(), "MENTAT_COOKIE_SECURE") {
		t.Fatalf("LoadAPI() error = %v", err)
	}
}

func TestLoadAPIRejectsReusedEncryptionKeys(t *testing.T) {
	setValidAPIEnv(t)
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\x01", 32)))
	t.Setenv("MENTAT_OAUTH_COOKIE_AUTH_KEY", key)
	t.Setenv("MENTAT_OAUTH_COOKIE_BLOCK_KEY", key)
	if _, err := LoadAPI(); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("LoadAPI() error = %v", err)
	}
}

func TestLoadAPIAcceptsLocalDevelopmentConfiguration(t *testing.T) {
	setValidAPIEnv(t)
	if _, err := LoadAPI(); err != nil {
		t.Fatal(err)
	}
}

func setValidAPIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MENTAT_DATABASE_URL", "postgres://mentat:mentat@localhost/mentat")
	t.Setenv("MENTAT_JWT_PRIVATE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("MENTAT_OAUTH_COOKIE_AUTH_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\x01", 32))))
	t.Setenv("MENTAT_OAUTH_COOKIE_BLOCK_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\x02", 32))))
	t.Setenv("MENTAT_CONNECTION_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\x03", 32))))
	t.Setenv("MENTAT_GITHUB_CLIENT_ID", "github-client")
	t.Setenv("MENTAT_GITHUB_CLIENT_SECRET", "github-secret")
	t.Setenv("MENTAT_GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("MENTAT_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("MENTAT_COOKIE_SECURE", "false")
	t.Setenv("MENTAT_FRONTEND_AUTH_CALLBACK_URL", "http://localhost:5173/auth/callback")
	t.Setenv("MENTAT_ALLOWED_ORIGINS", "http://localhost:5173")
	t.Setenv("MENTAT_TRUSTED_PROXY_CIDRS", "")
	t.Setenv("MENTAT_GITHUB_CALLBACK_URL", "http://localhost:8080/v1/auth/oauth/github/callback")
	t.Setenv("MENTAT_GOOGLE_CALLBACK_URL", "http://localhost:8080/v1/auth/oauth/google/callback")
}
