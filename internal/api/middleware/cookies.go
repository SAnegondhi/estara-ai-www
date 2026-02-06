package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// ADR-066: Cookie-based authentication utilities

const (
	// CSRFCookieName is the readable cookie for CSRF tokens
	// Must be readable by JavaScript for double-submit pattern
	CSRFCookieName = "csrf_token"
)

// Cookie names - use __Host- prefix only in production (requires Secure=true)
// In development (HTTP), __Host- prefix doesn't work
func getAccessCookieName(secure bool) string {
	if secure {
		return "__Host-access_token"
	}
	return "access_token"
}

func getRefreshCookieName(secure bool) string {
	if secure {
		return "__Host-refresh_token"
	}
	return "refresh_token"
}

// SetAuthCookies sets httpOnly access and refresh token cookies
// ADR-066: Uses SameSite=None for cross-origin auth in production
// In development (secure=false), uses SameSite=Lax for localhost compatibility
func SetAuthCookies(w http.ResponseWriter, accessToken, refreshToken string, secure bool) {
	// SameSite=None requires Secure=true, so use Lax for localhost (HTTP)
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}

	// Access token cookie - 24 hours
	http.SetCookie(w, &http.Cookie{
		Name:     getAccessCookieName(secure),
		Value:    accessToken,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})

	// Refresh token cookie - 7 days, restricted to auth path
	http.SetCookie(w, &http.Cookie{
		Name:     getRefreshCookieName(secure),
		Value:    refreshToken,
		Path:     "/api/auth",
		MaxAge:   604800, // 7 days
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})
}

// SetAccessCookie sets only the access token cookie (for refresh)
func SetAccessCookie(w http.ResponseWriter, accessToken string, secure bool) {
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}

	http.SetCookie(w, &http.Cookie{
		Name:     getAccessCookieName(secure),
		Value:    accessToken,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})
}

// SetCSRFCookie sets a readable CSRF token cookie and returns the token value
func SetCSRFCookie(w http.ResponseWriter, secure bool, tokenLength int) string {
	token := generateCSRFToken(tokenLength)

	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: false, // Must be readable by JavaScript
		Secure:   secure,
		SameSite: sameSite,
	})

	return token
}

// ClearAuthCookies removes all authentication cookies (logout)
// Clears both production (__Host- prefix) and development cookie names
func ClearAuthCookies(w http.ResponseWriter) {
	// Clear access tokens (both names)
	for _, name := range []string{"__Host-access_token", "access_token"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1, // Delete immediately
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}

	// Clear refresh tokens (both names)
	for _, name := range []string{"__Host-refresh_token", "refresh_token"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/api/auth",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}

	// Clear CSRF token
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// GetAccessToken extracts the access token from the cookie
// Checks both production (__Host- prefix) and development cookie names
func GetAccessToken(r *http.Request) string {
	// Try production name first
	if cookie, err := r.Cookie("__Host-access_token"); err == nil {
		return cookie.Value
	}
	// Fall back to development name
	if cookie, err := r.Cookie("access_token"); err == nil {
		return cookie.Value
	}
	return ""
}

// GetRefreshToken extracts the refresh token from the cookie
// Checks both production (__Host- prefix) and development cookie names
func GetRefreshToken(r *http.Request) string {
	// Try production name first
	if cookie, err := r.Cookie("__Host-refresh_token"); err == nil {
		return cookie.Value
	}
	// Fall back to development name
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		return cookie.Value
	}
	return ""
}

// GetCSRFToken extracts the CSRF token from the cookie
func GetCSRFToken(r *http.Request) string {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// generateCSRFToken generates a cryptographically secure random token
func generateCSRFToken(length int) string {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	if err != nil {
		// Fallback to a less secure but functional token
		// This should never happen in practice
		return base64.URLEncoding.EncodeToString([]byte("fallback-csrf-token"))
	}
	return base64.URLEncoding.EncodeToString(bytes)
}
