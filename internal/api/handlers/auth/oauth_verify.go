package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// OAuthIdentity holds the verified identity from a social login provider.
type OAuthIdentity struct {
	Email string
	Name  string
}

// jwksCache caches JWKS keyfuncs per URL with 1-hour TTL.
var (
	jwksCacheMu sync.RWMutex
	jwksCacheMap = make(map[string]*jwksCacheEntry)
)

type jwksCacheEntry struct {
	kf        keyfunc.Keyfunc
	expiresAt time.Time
}

// getOrFetchJWKS returns a cached JWKS keyfunc or fetches a new one.
func getOrFetchJWKS(ctx context.Context, jwksURL string) (jwt.Keyfunc, error) {
	jwksCacheMu.RLock()
	if entry, ok := jwksCacheMap[jwksURL]; ok && time.Now().Before(entry.expiresAt) {
		kf := entry.kf.Keyfunc
		jwksCacheMu.RUnlock()
		return kf, nil
	}
	jwksCacheMu.RUnlock()

	jwksCacheMu.Lock()
	defer jwksCacheMu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := jwksCacheMap[jwksURL]; ok && time.Now().Before(entry.expiresAt) {
		return entry.kf.Keyfunc, nil
	}

	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS from %s: %w", jwksURL, err)
	}

	jwksCacheMap[jwksURL] = &jwksCacheEntry{
		kf:        kf,
		expiresAt: time.Now().Add(1 * time.Hour),
	}

	return kf.Keyfunc, nil
}

// verifyIDToken parses and verifies a JWT ID token against a JWKS URL.
func verifyIDToken(ctx context.Context, idToken, jwksURL string, validIssuers []string, expectedAud string) (jwt.MapClaims, error) {
	keyFunc, err := getOrFetchJWKS(ctx, jwksURL)
	if err != nil {
		return nil, fmt.Errorf("JWKS fetch failed: %w", err)
	}

	token, err := jwt.Parse(idToken, keyFunc,
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("token verification failed: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Verify audience
	aud, err := claims.GetAudience()
	if err != nil || !containsString(aud, expectedAud) {
		return nil, fmt.Errorf("invalid audience: expected %s", expectedAud)
	}

	// Verify issuer
	iss, err := claims.GetIssuer()
	if err != nil {
		return nil, fmt.Errorf("missing issuer")
	}
	if !matchIssuer(iss, validIssuers) {
		return nil, fmt.Errorf("invalid issuer: %s", iss)
	}

	return claims, nil
}

// VerifyGoogleToken verifies a Google token (ID token or access token) and returns the user's identity.
// If the token is a JWT (3 dot-separated parts), it's verified via JWKS.
// Otherwise, it's treated as an access token and verified via Google's userinfo endpoint.
func VerifyGoogleToken(ctx context.Context, token, clientID string) (*OAuthIdentity, error) {
	parts := strings.SplitN(token, ".", 4)
	if len(parts) == 3 {
		return verifyGoogleIDToken(ctx, token, clientID)
	}
	return verifyGoogleAccessToken(ctx, token)
}

// verifyGoogleIDToken verifies a Google JWT ID token via JWKS.
func verifyGoogleIDToken(ctx context.Context, idToken, clientID string) (*OAuthIdentity, error) {
	const jwksURL = "https://www.googleapis.com/oauth2/v3/certs"
	validIssuers := []string{"accounts.google.com", "https://accounts.google.com"}

	claims, err := verifyIDToken(ctx, idToken, jwksURL, validIssuers, clientID)
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}

	// Google requires email_verified = true
	emailVerified, _ := claims["email_verified"].(bool)
	if !emailVerified {
		return nil, fmt.Errorf("google: email not verified")
	}

	email, _ := claims["email"].(string)
	if email == "" {
		return nil, fmt.Errorf("google: missing email claim")
	}

	name, _ := claims["name"].(string)

	return &OAuthIdentity{Email: email, Name: name}, nil
}

// verifyGoogleAccessToken verifies a Google access token via the userinfo endpoint.
func verifyGoogleAccessToken(ctx context.Context, accessToken string) (*OAuthIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return nil, fmt.Errorf("google: create userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google: userinfo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google: userinfo returned status %d", resp.StatusCode)
	}

	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("google: decode userinfo: %w", err)
	}

	if !info.EmailVerified {
		return nil, fmt.Errorf("google: email not verified")
	}
	if info.Email == "" {
		return nil, fmt.Errorf("google: missing email from userinfo")
	}

	return &OAuthIdentity{Email: info.Email, Name: info.Name}, nil
}

// VerifyMicrosoftIDToken verifies a Microsoft ID token and returns the user's identity.
func VerifyMicrosoftIDToken(ctx context.Context, idToken, clientID string) (*OAuthIdentity, error) {
	const jwksURL = "https://login.microsoftonline.com/common/discovery/v2.0/keys"
	// Microsoft tokens use version-specific issuers; check prefix instead of exact match
	validIssuers := []string{
		"https://login.microsoftonline.com/",
		"https://sts.windows.net/",
	}

	claims, err := verifyIDToken(ctx, idToken, jwksURL, validIssuers, clientID)
	if err != nil {
		return nil, fmt.Errorf("microsoft: %w", err)
	}

	// Microsoft puts email in different claims depending on account type
	email, _ := claims["email"].(string)
	if email == "" {
		email, _ = claims["preferred_username"].(string)
	}
	if email == "" {
		return nil, fmt.Errorf("microsoft: missing email claim")
	}

	name, _ := claims["name"].(string)

	return &OAuthIdentity{Email: email, Name: name}, nil
}

// VerifyAppleIDToken verifies an Apple ID token and returns the user's identity.
func VerifyAppleIDToken(ctx context.Context, idToken, clientID string) (*OAuthIdentity, error) {
	const jwksURL = "https://appleid.apple.com/auth/keys"
	validIssuers := []string{"https://appleid.apple.com"}

	claims, err := verifyIDToken(ctx, idToken, jwksURL, validIssuers, clientID)
	if err != nil {
		return nil, fmt.Errorf("apple: %w", err)
	}

	email, _ := claims["email"].(string)
	if email == "" {
		return nil, fmt.Errorf("apple: missing email claim")
	}

	// Apple only sends name on first authorization; name is passed from frontend
	return &OAuthIdentity{Email: email}, nil
}

// containsString checks if a slice contains a specific string.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// matchIssuer checks if the token issuer matches any of the valid issuers.
// Supports prefix matching for Microsoft's tenant-specific issuers.
func matchIssuer(iss string, validIssuers []string) bool {
	for _, valid := range validIssuers {
		if iss == valid {
			return true
		}
		// Prefix match for Microsoft: "https://login.microsoftonline.com/{tenantId}/v2.0"
		if len(valid) > 0 && valid[len(valid)-1] == '/' {
			if len(iss) > len(valid) && iss[:len(valid)] == valid {
				return true
			}
		}
	}
	return false
}
