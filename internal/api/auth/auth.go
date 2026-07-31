package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	apimiddleware "mentat/internal/api/middleware"
	coreauth "mentat/internal/auth"
	"mentat/internal/config"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

const (
	refreshCookieName = "mentat_refresh"
	intentCookieName  = "mentat_oauth_intent"
)

type contextKey string

const principalContextKey contextKey = "mentat-auth-principal"

type Principal struct {
	User      coreauth.User
	SessionID uuid.UUID
}

type oauthIntent struct {
	Intent    string
	Provider  string
	UserID    string
	SessionID string
}

type Handler struct {
	service         *coreauth.Service
	cfg             config.API
	intentCodec     *securecookie.SecureCookie
	allowedOrigins  map[string]struct{}
	httpClient      *http.Client
	authRateLimiter *apimiddleware.RateLimiter
}

func NewHandler(service *coreauth.Service, cfg config.API) (*Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("auth service is required")
	}
	callback, err := url.Parse(cfg.FrontendCallbackURL)
	if err != nil || callback.Scheme == "" || callback.Host == "" {
		return nil, fmt.Errorf("invalid frontend auth callback URL")
	}
	store := sessions.NewCookieStore(cfg.OAuthCookieAuthKey, cfg.OAuthCookieBlockKey)
	store.Options = &sessions.Options{
		Path: "/v1/auth/oauth", MaxAge: 600, HttpOnly: true,
		Secure: cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	}
	gothic.Store = store
	gothic.GetProviderName = func(r *http.Request) (string, error) {
		provider := chi.URLParam(r, "provider")
		if provider != "github" && provider != "google" {
			return "", fmt.Errorf("unsupported provider")
		}
		return provider, nil
	}
	goth.UseProviders(
		github.New(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.GitHubCallbackURL, "user:email"),
		google.New(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleCallbackURL, "openid", "email", "profile"),
	)
	origins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		origins[strings.TrimRight(origin, "/")] = struct{}{}
	}
	intentCodec := securecookie.New(cfg.OAuthCookieAuthKey, cfg.OAuthCookieBlockKey)
	intentCodec.MaxAge(600)
	return &Handler{
		service: service, cfg: cfg,
		intentCodec:     intentCodec,
		allowedOrigins:  origins,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		authRateLimiter: apimiddleware.NewRateLimiter(20, 8),
	}, nil
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.NotFound(apimiddleware.NotFound)
	r.MethodNotAllowed(apimiddleware.MethodNotAllowed)
	r.Group(func(r chi.Router) {
		r.Use(h.authRateLimiter.Middleware)
		r.Post("/signup", h.signup)
		r.Post("/login", h.login)
		r.With(h.requireAllowedOrigin).Post("/refresh", h.refresh)
		r.With(h.requireAllowedOrigin).Post("/logout", h.logout)
		r.Get("/oauth/{provider}", h.beginOAuth("login"))
		r.Get("/oauth/{provider}/callback", h.oauthCallback)
	})
	r.Group(func(r chi.Router) {
		r.Use(h.RequireAccess)
		r.Get("/me", h.me)
		r.Post("/logout-all", h.logoutAll)
		r.Post("/oauth/{provider}/link", h.prepareOAuthLink)
	})
	return r
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.service.Signup(r.Context(), input.Email, input.Password, input.DisplayName, clientInfo(r))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeSession(w, result, http.StatusCreated)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.service.Login(r.Context(), input.Email, input.Password, clientInfo(r))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeSession(w, result, http.StatusOK)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "authentication is required")
		return
	}
	result, err := h.service.Refresh(r.Context(), cookie.Value, clientInfo(r))
	if err != nil {
		h.clearRefreshCookie(w)
		h.writeServiceError(w, err)
		return
	}
	h.writeSession(w, result, http.StatusOK)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.service.Logout(r.Context(), cookie.Value); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to log out")
			return
		}
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	principal := PrincipalFromContext(r.Context())
	if err := h.service.LogoutAll(r.Context(), principal.User.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to log out")
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": PrincipalFromContext(r.Context()).User})
}

func (h *Handler) beginOAuth(intent string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.setOAuthIntent(w, r, intent) {
			return
		}
		gothic.BeginAuthHandler(w, r)
	}
}

func (h *Handler) prepareOAuthLink(w http.ResponseWriter, r *http.Request) {
	if !h.setOAuthIntent(w, r, "link") {
		return
	}
	authURL, err := gothic.GetAuthURL(w, r)
	if err != nil {
		h.clearIntentCookie(w)
		writeError(w, http.StatusBadGateway, "oauth_unavailable", "unable to start OAuth")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": authURL})
}

func (h *Handler) setOAuthIntent(w http.ResponseWriter, r *http.Request, intent string) bool {
	if !supportedProvider(chi.URLParam(r, "provider")) {
		writeError(w, http.StatusNotFound, "unsupported_provider", "OAuth provider is not supported")
		return false
	}
	if r.URL.Query().Has("state") {
		writeError(w, http.StatusBadRequest, "invalid_oauth_state", "OAuth state is generated by the server")
		return false
	}
	provider := chi.URLParam(r, "provider")
	value := oauthIntent{Intent: intent, Provider: provider}
	if intent == "link" {
		principal := PrincipalFromContext(r.Context())
		value.UserID = principal.User.ID.String()
		value.SessionID = principal.SessionID.String()
	}
	encoded, err := h.intentCodec.Encode(intentCookieName, value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to start OAuth")
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name: intentCookieName, Value: encoded, Path: "/v1/auth/oauth",
		MaxAge: 600, HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	return true
}

func (h *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if !supportedProvider(chi.URLParam(r, "provider")) {
		writeError(w, http.StatusNotFound, "unsupported_provider", "OAuth provider is not supported")
		return
	}
	provider := chi.URLParam(r, "provider")
	intent, err := h.readOAuthIntent(r, provider)
	if err != nil {
		h.redirectOAuthError(w, r, "invalid_oauth_state")
		return
	}
	var link *coreauth.LinkAuthorization
	if intent.Intent == "link" {
		userID, userErr := uuid.Parse(intent.UserID)
		sessionID, sessionErr := uuid.Parse(intent.SessionID)
		if userErr != nil || sessionErr != nil ||
			h.service.ValidateSession(r.Context(), userID, sessionID) != nil {
			h.redirectOAuthError(w, r, "invalid_oauth_state")
			return
		}
		link = &coreauth.LinkAuthorization{UserID: userID, SessionID: sessionID}
	}
	gothUser, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		h.redirectOAuthError(w, r, "oauth_failed")
		return
	}
	_ = gothic.Logout(w, r)
	email, verified, err := h.verifiedProviderEmail(r.Context(), gothUser)
	if err != nil {
		h.redirectOAuthError(w, r, "oauth_email_unavailable")
		return
	}
	result, err := h.service.OAuthLogin(r.Context(), coreauth.OAuthProfile{
		Provider: gothUser.Provider, ProviderUserID: gothUser.UserID,
		Email: email, EmailVerified: verified, DisplayName: gothUser.Name,
		AvatarURL: gothUser.AvatarURL,
	}, link, clientInfo(r))
	if err != nil {
		code, _ := serviceError(err)
		h.redirectOAuthError(w, r, code)
		return
	}
	h.setRefreshCookie(w, result.RefreshToken)
	h.clearIntentCookie(w)
	http.Redirect(w, r, h.cfg.FrontendCallbackURL, http.StatusSeeOther)
}

func (h *Handler) verifiedProviderEmail(ctx context.Context, user goth.User) (string, bool, error) {
	switch user.Provider {
	case "google":
		verified := rawBool(user.RawData, "email_verified") || rawBool(user.RawData, "verified_email")
		return user.Email, verified, nil
	case "github":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
		if err != nil {
			return "", false, err
		}
		req.Header.Set("Authorization", "Bearer "+user.AccessToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := h.httpClient.Do(req)
		if err != nil {
			return "", false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", false, fmt.Errorf("GitHub emails returned %s", resp.Status)
		}
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&emails); err != nil {
			return "", false, err
		}
		for _, item := range emails {
			if item.Primary && item.Verified {
				return item.Email, true, nil
			}
		}
		for _, item := range emails {
			if item.Verified {
				return item.Email, true, nil
			}
		}
		return "", false, fmt.Errorf("can not verify github email")
	default:
		return "", false, fmt.Errorf("unsupported provider")
	}
}

func (h *Handler) RequireAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		scheme, token, ok := strings.Cut(header, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			writeError(w, http.StatusUnauthorized, "invalid_token", "authentication is required")
			return
		}
		user, sessionID, err := h.service.Authenticate(r.Context(), strings.TrimSpace(token))
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey, Principal{User: user, SessionID: sessionID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) requireAllowedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" {
			if _, ok := h.allowedOrigins[origin]; !ok {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func PrincipalFromContext(ctx context.Context) Principal {
	principal, _ := ctx.Value(principalContextKey).(Principal)
	return principal
}

func (h *Handler) writeSession(w http.ResponseWriter, result coreauth.SessionResult, status int) {
	h.setRefreshCookie(w, result.RefreshToken)
	expiresIn := int(time.Until(result.AccessExpires).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	writeJSON(w, status, map[string]any{
		"user": result.User, "access_token": result.AccessToken,
		"token_type": "Bearer", "expires_in": expiresIn,
	})
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: value, Path: "/v1/auth",
		MaxAge: int(h.cfg.RefreshTTL.Seconds()), Domain: h.cfg.CookieDomain,
		HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Path: "/v1/auth", MaxAge: -1,
		Domain: h.cfg.CookieDomain, HttpOnly: true, Secure: h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) readOAuthIntent(r *http.Request, provider string) (oauthIntent, error) {
	cookie, err := r.Cookie(intentCookieName)
	if err != nil {
		return oauthIntent{}, err
	}
	var value oauthIntent
	if err := h.intentCodec.Decode(intentCookieName, cookie.Value, &value); err != nil {
		return oauthIntent{}, err
	}
	if (value.Intent != "login" && value.Intent != "link") || value.Provider != provider {
		return oauthIntent{}, fmt.Errorf("invalid OAuth intent")
	}
	return value, nil
}

func (h *Handler) clearIntentCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: intentCookieName, Path: "/v1/auth/oauth", MaxAge: -1,
		HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) redirectOAuthError(w http.ResponseWriter, r *http.Request, code string) {
	target, _ := url.Parse(h.cfg.FrontendCallbackURL)
	query := target.Query()
	query.Set("oauth_error", code)
	target.RawQuery = query.Encode()
	h.clearIntentCookie(w)
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	code, status := serviceError(err)
	message := "request could not be completed"
	switch code {
	case "validation_error":
		message = "email or password is invalid"
	case "email_taken":
		message = "an account with this email already exists"
	case "invalid_credentials":
		message = "invalid email or password"
	case "invalid_token", "session_expired", "session_revoked":
		message = "authentication is required"
	case "user_disabled":
		message = "this account is disabled"
	case "verified_email_required":
		message = "the provider must supply a verified email"
	case "email_not_available":
		message = "the provider did not supply an email address"
	case "account_link_required":
		message = "sign in to link this provider"
	case "identity_conflict":
		message = "this provider identity is already linked"
	}
	writeError(w, status, code, message)
}

func serviceError(err error) (string, int) {
	switch {
	case errors.Is(err, coreauth.ErrInvalidInput):
		return "validation_error", http.StatusUnprocessableEntity
	case errors.Is(err, coreauth.ErrEmailTaken):
		return "email_taken", http.StatusConflict
	case errors.Is(err, coreauth.ErrInvalidCredentials):
		return "invalid_credentials", http.StatusUnauthorized
	case errors.Is(err, coreauth.ErrInvalidToken):
		return "invalid_token", http.StatusUnauthorized
	case errors.Is(err, coreauth.ErrSessionExpired):
		return "session_expired", http.StatusUnauthorized
	case errors.Is(err, coreauth.ErrSessionRevoked):
		return "session_revoked", http.StatusUnauthorized
	case errors.Is(err, coreauth.ErrUserDisabled):
		return "user_disabled", http.StatusForbidden
	case errors.Is(err, coreauth.ErrVerifiedEmailNeeded):
		return "verified_email_required", http.StatusUnprocessableEntity
	case errors.Is(err, coreauth.ErrEmailNotAvailable):
		return "email_not_available", http.StatusUnprocessableEntity
	case errors.Is(err, coreauth.ErrAccountLinkRequired):
		return "account_link_required", http.StatusConflict
	case errors.Is(err, coreauth.ErrIdentityConflict):
		return "identity_conflict", http.StatusConflict
	default:
		return "internal_error", http.StatusInternalServerError
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func clientInfo(r *http.Request) coreauth.ClientInfo {
	return coreauth.ClientInfo{UserAgent: r.UserAgent(), IPAddress: apimiddleware.RemoteIP(r)}
}

func rawBool(data map[string]any, key string) bool {
	value, ok := data[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func supportedProvider(provider string) bool {
	return provider == "github" || provider == "google"
}
