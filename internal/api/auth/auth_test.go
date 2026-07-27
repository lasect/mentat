package auth

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreauth "tetra/internal/auth"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
)

func TestDecodeJSON(t *testing.T) {
	var destination struct {
		Email string `json:"email"`
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"person@example.com"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	if !decodeJSON(response, request, &destination) {
		t.Fatalf("decodeJSON failed: %s", response.Body.String())
	}
	if destination.Email != "person@example.com" {
		t.Fatalf("email = %q", destination.Email)
	}
}

func TestDecodeJSONRejectsUnknownAndTrailingValues(t *testing.T) {
	for _, body := range []string{
		`{"email":"person@example.com","extra":true}`,
		`{"email":"person@example.com"} {}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		var destination struct {
			Email string `json:"email"`
		}
		if decodeJSON(response, request, &destination) {
			t.Fatalf("decodeJSON(%q) succeeded", body)
		}
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", response.Code)
		}
	}
}

func TestDecodeJSONRejectsNonJSONMediaType(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"person@example.com"}`))
	request.Header.Set("Content-Type", "application/json-patch+json")
	response := httptest.NewRecorder()
	var destination struct {
		Email string `json:"email"`
	}
	if decodeJSON(response, request, &destination) {
		t.Fatal("decodeJSON accepted a non-JSON media type")
	}
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestServiceErrorMapping(t *testing.T) {
	for _, test := range []struct {
		err    error
		code   string
		status int
	}{
		{fmt.Errorf("signup: %w", coreauth.ErrInvalidInput), "validation_error", http.StatusUnprocessableEntity},
		{coreauth.ErrEmailTaken, "email_taken", http.StatusConflict},
		{coreauth.ErrInvalidCredentials, "invalid_credentials", http.StatusUnauthorized},
		{coreauth.ErrSessionRevoked, "session_revoked", http.StatusUnauthorized},
		{coreauth.ErrEmailNotAvailable, "email_not_available", http.StatusUnprocessableEntity},
		{coreauth.ErrIdentityConflict, "identity_conflict", http.StatusConflict},
	} {
		code, status := serviceError(test.err)
		if code != test.code || status != test.status {
			t.Fatalf("serviceError(%v) = %q, %d", test.err, code, status)
		}
	}
}

func TestRawBool(t *testing.T) {
	if !rawBool(map[string]any{"verified": true}, "verified") {
		t.Fatal("bool true was not recognized")
	}
	if !rawBool(map[string]any{"verified": "TRUE"}, "verified") {
		t.Fatal("string true was not recognized")
	}
	if rawBool(map[string]any{"verified": 1}, "verified") {
		t.Fatal("numeric value was recognized")
	}
}

func TestSupportedProvider(t *testing.T) {
	if !supportedProvider("github") || !supportedProvider("google") {
		t.Fatal("configured providers were rejected")
	}
	if supportedProvider("gitlab") || supportedProvider("") {
		t.Fatal("unsupported provider was accepted")
	}
}

func TestOAuthIntentIsBoundToProvider(t *testing.T) {
	codec := securecookie.New(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	codec.MaxAge(600)
	handler := &Handler{intentCodec: codec}
	encoded, err := codec.Encode(intentCookieName, oauthIntent{
		Intent: "link", Provider: "github",
		UserID:    "f8327a23-6b20-4b27-ae72-966bde74ea47",
		SessionID: "c36470c3-a9b9-4468-b558-dc541da06186",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: intentCookieName, Value: encoded})
	if _, err := handler.readOAuthIntent(request, "github"); err != nil {
		t.Fatalf("matching provider rejected: %v", err)
	}
	if _, err := handler.readOAuthIntent(request, "google"); err == nil {
		t.Fatal("OAuth intent was accepted for a different provider")
	}
}

func TestSetOAuthLinkIntentBindsAuthenticatedSession(t *testing.T) {
	codec := securecookie.New(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	codec.MaxAge(600)
	handler := &Handler{intentCodec: codec}
	userID := uuid.New()
	sessionID := uuid.New()
	router := chi.NewRouter()
	router.Post("/oauth/{provider}/link", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), principalContextKey, Principal{
			User: coreauth.User{ID: userID}, SessionID: sessionID,
		})
		if handler.setOAuthIntent(w, r.WithContext(ctx), "link") {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/oauth/github/link", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	result := response.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	var intent oauthIntent
	if err := codec.Decode(intentCookieName, cookies[0].Value, &intent); err != nil {
		t.Fatal(err)
	}
	if intent.Intent != "link" || intent.Provider != "github" ||
		intent.UserID != userID.String() || intent.SessionID != sessionID.String() {
		t.Fatalf("intent = %#v", intent)
	}
}

func TestSetOAuthIntentRejectsCallerSuppliedState(t *testing.T) {
	codec := securecookie.New(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	handler := &Handler{intentCodec: codec}
	router := chi.NewRouter()
	router.Get("/oauth/{provider}", func(w http.ResponseWriter, r *http.Request) {
		if handler.setOAuthIntent(w, r, "login") {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/oauth/github?state=attacker-controlled", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("OAuth intent cookie was set for caller-supplied state")
	}
}
