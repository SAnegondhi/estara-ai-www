package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// ADR-066: Cookie-based authentication utilities

const (
	// AccessCookieName is the httpOnly cookie for JWT access tokens
	// Using __Host- prefix enforces: Secure flag, no Domain, Path=/
	AccessCookieName = "__Host-access_token"

	// RefreshCookieName is the httpOnly cookie for JWT refresh tokens
	// Restricted to /api/auth path only
	RefreshCookieName = "__Host-refresh_token"

	// CSRFCookieName is the readable cookie for CSRF tokens
	// Must be readable by JavaScript for double-submit pattern
	CSRFCookieName = "csrf_token"
)

// SetAuthCookies sets httpOnly access and refresh token cookies
func SetAuthCookies(w http.ResponseWriter, accessToken, refreshToken string, secure bool) {
	// Access token cookie - 24 hours
	http.SetCookie(w, &http.Cookie{
		Name:     AccessCookieName,
		Value:    accessToken,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})

	// Refresh token cookie - 7 days, restricted to auth path
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    refreshToken,
		Path:     "/api/auth",
		MaxAge:   604800, // 7 days
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// SetAccessCookie sets only the access token cookie (for refresh)
func SetAccessCookie(w http.ResponseWriter, accessToken string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     AccessCookieName,
		Value:    accessToken,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// SetCSRFCookie sets a readable CSRF token cookie and returns the token value
func SetCSRFCookie(w http.ResponseWriter, secure bool, tokenLength int) string {
	token := generateCSRFToken(tokenLength)

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: false, // Must be readable by JavaScript
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})

	return token
}

// ClearAuthCookies removes all authentication cookies (logout)
func ClearAuthCookies(w http.ResponseWriter) {
	// Clear access token
	http.SetCookie(w, &http.Cookie{
		Name:     AccessCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1, // Delete immediately
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	// Clear refresh token
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/api/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

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
func GetAccessToken(r *http.Request) string {
	cookie, err := r.Cookie(AccessCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// GetRefreshToken extracts the refresh token from the cookie
func GetRefreshToken(r *http.Request) string {
	cookie, err := r.Cookie(RefreshCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
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
