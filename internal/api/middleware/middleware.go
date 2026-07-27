package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"
)

type clientIPContextKey struct{}

func ClientIP(trustedCIDRs []string) (func(http.Handler) http.Handler, error) {
	trusted := make([]*net.IPNet, 0, len(trustedCIDRs))
	for _, candidate := range trustedCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(candidate))
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", candidate, err)
		}
		trusted = append(trusted, network)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, trusted)
			ctx := context.WithValue(r.Context(), clientIPContextKey{}, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}, nil
}

func RemoteIP(r *http.Request) string {
	if value, ok := r.Context().Value(clientIPContextKey{}).(string); ok && value != "" {
		return value
	}
	return peerIP(r.RemoteAddr)
}

func resolveClientIP(r *http.Request, trusted []*net.IPNet) string {
	current := net.ParseIP(peerIP(r.RemoteAddr))
	if current == nil {
		return peerIP(r.RemoteAddr)
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0 && isTrustedIP(current, trusted); index-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if candidate == nil {
			break
		}
		current = candidate
	}
	return current.String()
}

func isTrustedIP(ip net.IP, trusted []*net.IPNet) bool {
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func peerIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[strings.TrimRight(origin, "/")] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			if origin != "" {
				if _, ok := allowed[origin]; !ok {
					writeForbidden(w)
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func NotFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, "not_found", "route was not found")
}

func MethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this route")
}

func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()
		next.ServeHTTP(wrapped, r)
		slog.InfoContext(
			r.Context(),
			"http request",
			"request_id", chimiddleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.Status(),
			"bytes", wrapped.BytesWritten(),
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     rate.Limit
	burst    int
	now      func() time.Time
	lastGC   time.Time
}

const (
	maxRateLimitVisitors = 10_000
	rateLimitVisitorTTL  = 10 * time.Minute
	rateLimitGCInterval  = time.Minute
)

func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate.Limit(perMinute / 60),
		burst:    burst,
		now:      time.Now,
	}
}

func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := RemoteIP(r)
		now := l.now()
		l.mu.Lock()
		if l.lastGC.IsZero() || now.Sub(l.lastGC) >= rateLimitGCInterval {
			for key, candidate := range l.visitors {
				if now.Sub(candidate.lastSeen) > rateLimitVisitorTTL {
					delete(l.visitors, key)
				}
			}
			l.lastGC = now
		}
		entry := l.visitors[host]
		if entry == nil && len(l.visitors) < maxRateLimitVisitors {
			entry = &visitor{limiter: rate.NewLimiter(l.rate, l.burst)}
			l.visitors[host] = entry
		}
		allowed := false
		if entry != nil {
			entry.lastSeen = now
			allowed = entry.limiter.Allow()
		}
		l.mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "rate_limited", "message": "too many requests"},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeForbidden(w http.ResponseWriter) {
	writeAPIError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
