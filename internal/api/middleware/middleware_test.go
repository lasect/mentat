package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestCORS(t *testing.T) {
	handler := CORS([]string{"https://app.tetra.test"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	allowed := httptest.NewRequest(http.MethodGet, "/", nil)
	allowed.Header.Set("Origin", "https://app.tetra.test")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if got := allowedResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://app.tetra.test" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := allowedResponse.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := allowedResponse.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE, OPTIONS" {
		t.Fatalf("allow methods = %q", got)
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/", nil)
	forbidden.Header.Set("Origin", "https://evil.test")
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d", forbiddenResponse.Code)
	}
}

func TestClientIPIgnoresUntrustedForwardingHeaders(t *testing.T) {
	middleware, err := ClientIP(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	handler := middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RemoteIP(r)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if got != "192.0.2.10" {
		t.Fatalf("client IP = %q", got)
	}
}

func TestClientIPResolvesTrustedProxyChain(t *testing.T) {
	middleware, err := ClientIP([]string{"10.0.0.0/8", "192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	handler := middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RemoteIP(r)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.20")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if got != "198.51.100.7" {
		t.Fatalf("client IP = %q", got)
	}
}

func TestClientIPRejectsInvalidTrustedCIDR(t *testing.T) {
	if _, err := ClientIP([]string{"not-a-network"}); err == nil {
		t.Fatal("invalid trusted proxy CIDR succeeded")
	}
}

func TestSecurityHeaders(t *testing.T) {
	response := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	for key, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	} {
		if got := response.Header().Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRoutingErrorsUseAPIEnvelope(t *testing.T) {
	for _, test := range []struct {
		handler http.HandlerFunc
		status  int
		code    string
	}{
		{NotFound, http.StatusNotFound, "not_found"},
		{MethodNotAllowed, http.StatusMethodNotAllowed, "method_not_allowed"},
	} {
		response := httptest.NewRecorder()
		test.handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != test.status {
			t.Fatalf("status = %d", response.Code)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != test.code {
			t.Fatalf("error code = %q", body.Error.Code)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(1, 1)
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	first := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	handler.ServeHTTP(first, request)
	if first.Code != http.StatusNoContent {
		t.Fatalf("first status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request.Clone(request.Context()))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d", second.Code)
	}
}

func TestRateLimiterExpiresInactiveVisitors(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(60, 1)
	limiter.now = func() time.Time { return now }
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "192.0.2.1:1234"
	handler.ServeHTTP(httptest.NewRecorder(), first)
	now = now.Add(rateLimitVisitorTTL + rateLimitGCInterval)
	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "192.0.2.2:1234"
	handler.ServeHTTP(httptest.NewRecorder(), second)
	if _, ok := limiter.visitors["192.0.2.1"]; ok {
		t.Fatal("inactive rate-limit visitor was not removed")
	}
}

func TestRateLimiterBoundsVisitorMap(t *testing.T) {
	limiter := NewRateLimiter(60, 1)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	limiter.lastGC = now
	for index := 0; index < maxRateLimitVisitors; index++ {
		limiter.visitors[strconv.Itoa(index)] = &visitor{
			limiter: rate.NewLimiter(limiter.rate, limiter.burst), lastSeen: now,
		}
	}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "new-visitor"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", response.Code)
	}
	if len(limiter.visitors) != maxRateLimitVisitors {
		t.Fatalf("visitor count = %d", len(limiter.visitors))
	}
}
