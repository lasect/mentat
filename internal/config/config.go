package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type API struct {
	Address             string
	DatabaseURL         string
	FrontendCallbackURL string
	AllowedOrigins      []string
	TrustedProxyCIDRs   []string
	JWTPrivateKey       string
	JWTIssuer           string
	JWTAudience         string
	AccessTTL           time.Duration
	RefreshTTL          time.Duration
	CookieSecure        bool
	CookieDomain        string
	OAuthCookieAuthKey  []byte
	OAuthCookieBlockKey []byte
	ConnectionKey       []byte
	GitHubClientID      string
	GitHubClientSecret  string
	GitHubCallbackURL   string
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleCallbackURL   string
}

func LoadAPI() (API, error) {
	cfg := API{
		Address:             envOr("TETRA_API_ADDRESS", ":8080"),
		DatabaseURL:         os.Getenv("TETRA_DATABASE_URL"),
		FrontendCallbackURL: envOr("TETRA_FRONTEND_AUTH_CALLBACK_URL", "http://localhost:5173/auth/callback"),
		AllowedOrigins:      splitCSV(envOr("TETRA_ALLOWED_ORIGINS", "http://localhost:5173")),
		TrustedProxyCIDRs:   splitCSV(os.Getenv("TETRA_TRUSTED_PROXY_CIDRS")),
		JWTPrivateKey:       os.Getenv("TETRA_JWT_PRIVATE_KEY"),
		JWTIssuer:           envOr("TETRA_JWT_ISSUER", "tetra-api"),
		JWTAudience:         envOr("TETRA_JWT_AUDIENCE", "tetra"),
		AccessTTL:           15 * time.Minute,
		RefreshTTL:          30 * 24 * time.Hour,
		CookieDomain:        os.Getenv("TETRA_COOKIE_DOMAIN"),
		GitHubClientID:      os.Getenv("TETRA_GITHUB_CLIENT_ID"),
		GitHubClientSecret:  os.Getenv("TETRA_GITHUB_CLIENT_SECRET"),
		GitHubCallbackURL:   envOr("TETRA_GITHUB_CALLBACK_URL", "http://localhost:8080/v1/auth/oauth/github/callback"),
		GoogleClientID:      os.Getenv("TETRA_GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  os.Getenv("TETRA_GOOGLE_CLIENT_SECRET"),
		GoogleCallbackURL:   envOr("TETRA_GOOGLE_CALLBACK_URL", "http://localhost:8080/v1/auth/oauth/google/callback"),
	}
	var err error
	cfg.CookieSecure, err = strconv.ParseBool(envOr("TETRA_COOKIE_SECURE", "true"))
	if err != nil {
		return API{}, fmt.Errorf("TETRA_COOKIE_SECURE: %w", err)
	}
	cfg.OAuthCookieAuthKey, err = decodeKey("TETRA_OAUTH_COOKIE_AUTH_KEY", 32)
	if err != nil {
		return API{}, err
	}
	cfg.OAuthCookieBlockKey, err = decodeKey("TETRA_OAUTH_COOKIE_BLOCK_KEY", 32)
	if err != nil {
		return API{}, err
	}
	cfg.ConnectionKey, err = decodeKey("TETRA_CONNECTION_ENCRYPTION_KEY", 32)
	if err != nil {
		return API{}, err
	}
	if bytes.Equal(cfg.OAuthCookieAuthKey, cfg.OAuthCookieBlockKey) ||
		bytes.Equal(cfg.OAuthCookieAuthKey, cfg.ConnectionKey) ||
		bytes.Equal(cfg.OAuthCookieBlockKey, cfg.ConnectionKey) {
		return API{}, fmt.Errorf("OAuth cookie and connection encryption keys must be independent")
	}
	required := []struct {
		name  string
		value string
	}{
		{"TETRA_DATABASE_URL", cfg.DatabaseURL},
		{"TETRA_JWT_PRIVATE_KEY", cfg.JWTPrivateKey},
		{"TETRA_GITHUB_CLIENT_ID", cfg.GitHubClientID},
		{"TETRA_GITHUB_CLIENT_SECRET", cfg.GitHubClientSecret},
		{"TETRA_GOOGLE_CLIENT_ID", cfg.GoogleClientID},
		{"TETRA_GOOGLE_CLIENT_SECRET", cfg.GoogleClientSecret},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return API{}, fmt.Errorf("%s is required", item.name)
		}
	}
	if len(cfg.AllowedOrigins) == 0 {
		return API{}, fmt.Errorf("TETRA_ALLOWED_ORIGINS must contain at least one origin")
	}
	for _, origin := range cfg.AllowedOrigins {
		parsed, err := validateWebURL(origin)
		if err != nil {
			return API{}, err
		}
		if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return API{}, fmt.Errorf("allowed origin %q must contain only scheme and host", origin)
		}
	}
	callbacks := []string{
		cfg.FrontendCallbackURL,
		cfg.GitHubCallbackURL,
		cfg.GoogleCallbackURL,
	}
	for _, candidate := range callbacks {
		parsed, err := validateWebURL(candidate)
		if err != nil {
			return API{}, err
		}
		if !cfg.CookieSecure && !isLoopbackHost(parsed.Hostname()) {
			return API{}, fmt.Errorf("TETRA_COOKIE_SECURE may be false only for loopback callback URLs")
		}
	}
	return cfg, nil
}

func validateWebURL(candidate string) (*url.URL, error) {
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid configured URL %q", candidate)
	}
	if parsed.Scheme == "https" {
		return parsed, nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return parsed, nil
	}
	return nil, fmt.Errorf("configured URL %q must use HTTPS (HTTP is allowed only for loopback hosts)", candidate)
}

func isLoopbackHost(hostname string) bool {
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, strings.TrimRight(item, "/"))
		}
	}
	return result
}

func decodeKey(name string, size int) ([]byte, error) {
	value := os.Getenv(name)
	if value == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64: %w", name, err)
	}
	if len(raw) != size {
		return nil, fmt.Errorf("%s must decode to %d bytes", name, size)
	}
	return raw, nil
}
