package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/estara-ai/www/pkg/httputil"
)

// ADR-066: CSRF Protection Middleware

// CSRFMiddleware validates CSRF tokens for state-changing requests
// Uses double-submit cookie pattern: compares cookie value with header value
// During migration (ADR-066), this is lenient: only validates if cookie-based auth is used
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip CSRF check for safe (read-only) methods
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF for SSE endpoints (they use query param validation)
		if isSSEEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF for login endpoints (no session yet)
		if strings.HasSuffix(r.URL.Path, "/client-login") ||
			strings.HasSuffix(r.URL.Path, "/passkey/login/begin") ||
			strings.HasSuffix(r.URL.Path, "/passkey/login/finish") {
			next.ServeHTTP(w, r)
			return
		}

		// ADR-066 Migration: Only enforce CSRF if using cookie-based auth
		// If client is using Authorization header, skip CSRF (legacy auth)
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			// Legacy header-based auth - skip CSRF validation
			next.ServeHTTP(w, r)
			return
		}

		// Get CSRF token from cookie
		csrfCookie := GetCSRFToken(r)
		if csrfCookie == "" {
			// No CSRF cookie - might be using legacy auth or no auth at all
			// Let the auth middleware handle the rejection
			next.ServeHTTP(w, r)
			return
		}

		// If we have a CSRF cookie, require the header
		csrfHeader := r.Header.Get("X-CSRF-Token")
		if csrfHeader == "" {
			httputil.Forbidden(w, "CSRF token header required")
			return
		}

		// Constant-time comparison to prevent timing attacks
		if !secureCompare(csrfCookie, csrfHeader) {
			httputil.Forbidden(w, "CSRF token mismatch")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// StrictCSRFMiddleware always requires CSRF tokens (post-migration)
// Use this after migration is complete
func StrictCSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip CSRF check for safe (read-only) methods
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF for SSE endpoints
		if isSSEEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF for login endpoints
		if strings.HasSuffix(r.URL.Path, "/client-login") ||
			strings.HasSuffix(r.URL.Path, "/passkey/login/begin") ||
			strings.HasSuffix(r.URL.Path, "/passkey/login/finish") {
			next.ServeHTTP(w, r)
			return
		}

		// Get CSRF token from cookie
		csrfCookie := GetCSRFToken(r)
		if csrfCookie == "" {
			httputil.Forbidden(w, "CSRF token cookie missing")
			return
		}

		// Get CSRF token from header
		csrfHeader := r.Header.Get("X-CSRF-Token")
		if csrfHeader == "" {
			httputil.Forbidden(w, "CSRF token header missing")
			return
		}

		// Constant-time comparison
		if !secureCompare(csrfCookie, csrfHeader) {
			httputil.Forbidden(w, "CSRF token mismatch")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ValidateSSECSRF validates CSRF token from query param for SSE endpoints
// SSE (EventSource) doesn't support custom headers, so we use query params
func ValidateSSECSRF(r *http.Request) bool {
	// Get CSRF token from cookie
	csrfCookie := GetCSRFToken(r)
	if csrfCookie == "" {
		return false
	}

	// Get CSRF token from query param
	csrfQuery := r.URL.Query().Get("csrf")
	if csrfQuery == "" {
		return false
	}

	return secureCompare(csrfCookie, csrfQuery)
}

// isSSEEndpoint checks if the path is a Server-Sent Events endpoint
func isSSEEndpoint(path string) bool {
	sseEndpoints := []string{
		"/api/v2/discover/search/stream",
		"/api/v2/discover/enrich/stream",
		"/api/v2/investment-plans/",
	}
	for _, endpoint := range sseEndpoints {
		if strings.HasPrefix(path, endpoint) {
			return true
		}
	}
	return false
}

// secureCompare performs constant-time string comparison
// to prevent timing attacks
func secureCompare(a, b string) bool {
	// Use subtle.ConstantTimeCompare for timing-attack resistant comparison
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
