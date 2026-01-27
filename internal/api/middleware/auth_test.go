package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/estara-ai/www/internal/config"
)

// Test configuration
func testConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Secret: "test-secret-key-at-least-32-chars-long",
			Expiry: time.Hour,
		},
		Admin: config.AdminConfig{
			UserIDs: []string{"admin-user-1", "admin-user-2"},
		},
	}
}

func TestNewAuthMiddleware(t *testing.T) {
	cfg := testConfig()
	m := NewAuthMiddleware(cfg)

	assert.NotNil(t, m)
	assert.Equal(t, []byte(cfg.JWT.Secret), m.jwtSecret)
	assert.True(t, m.adminIDs["admin-user-1"])
	assert.True(t, m.adminIDs["admin-user-2"])
	assert.False(t, m.adminIDs["regular-user"])
}

func TestGenerateToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	token, err := m.GenerateToken("user-123", "clerk-456", "test@example.com", "user", time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// Verify the token can be validated
	claims, err := m.validateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
	assert.Equal(t, "clerk-456", claims.ClerkUserID)
	assert.Equal(t, "test@example.com", claims.Email)
	assert.Equal(t, "user", claims.Role)
}

func TestValidateToken_Valid(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	token, err := m.GenerateToken("user-123", "", "test@example.com", "user", time.Hour)
	require.NoError(t, err)

	claims, err := m.validateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
}

func TestValidateToken_Expired(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	// Generate token that expired 1 second ago
	token, err := m.GenerateToken("user-123", "", "test@example.com", "user", -time.Second)
	require.NoError(t, err)

	_, err = m.validateToken(token)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	// Create a token with a different secret
	otherConfig := testConfig()
	otherConfig.JWT.Secret = "different-secret-key-at-least-32-chars-long"
	otherMiddleware := NewAuthMiddleware(otherConfig)

	token, err := otherMiddleware.GenerateToken("user-123", "", "test@example.com", "user", time.Hour)
	require.NoError(t, err)

	// Try to validate with original middleware (different secret)
	_, err = m.validateToken(token)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestValidateToken_InvalidAlgorithm(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	// Create a token with RS256 algorithm (should be rejected)
	claims := &Claims{
		UserID: "user-123",
		Email:  "test@example.com",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	// Use "none" algorithm (attack vector)
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = m.validateToken(tokenString)
	assert.Error(t, err)
}

func TestValidateToken_MissingUserID(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	// Create token without UserID
	claims := &Claims{
		UserID: "", // Empty
		Email:  "test@example.com",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(m.jwtSecret)
	require.NoError(t, err)

	_, err = m.validateToken(tokenString)
	assert.ErrorIs(t, err, ErrInvalidClaims)
}

func TestGenerateRefreshToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	refreshToken, err := m.GenerateRefreshToken("user-123", 7*24*time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, refreshToken)

	// Validate the refresh token
	userID, err := m.ValidateRefreshToken(refreshToken)
	require.NoError(t, err)
	assert.Equal(t, "user-123", userID)
}

func TestValidateRefreshToken_Expired(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	refreshToken, err := m.GenerateRefreshToken("user-123", -time.Second)
	require.NoError(t, err)

	_, err = m.ValidateRefreshToken(refreshToken)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestDecodeTokenWithoutValidation(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	// Generate an expired token
	token, err := m.GenerateToken("user-123", "clerk-456", "test@example.com", "user", -time.Second)
	require.NoError(t, err)

	// Should still be able to decode even though expired
	claims, err := m.DecodeTokenWithoutValidation(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
	assert.Equal(t, "clerk-456", claims.ClerkUserID)
}

func TestAuthenticate_Success(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	token, err := m.GenerateToken("user-123", "", "test@example.com", "user", time.Hour)
	require.NoError(t, err)

	// Create a test handler that checks the user in context
	var capturedClaims *Claims
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetUserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	m.Authenticate(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedClaims)
	assert.Equal(t, "user-123", capturedClaims.UserID)
}

func TestAuthenticate_MissingToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	m.Authenticate(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthenticate_InvalidToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr := httptest.NewRecorder()

	m.Authenticate(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthenticate_ExpiredToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	token, err := m.GenerateToken("user-123", "", "test@example.com", "user", -time.Second)
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	m.Authenticate(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "token expired")
}

func TestAuthenticate_TokenInQueryParam(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	token, err := m.GenerateToken("user-123", "", "test@example.com", "user", time.Hour)
	require.NoError(t, err)

	var capturedClaims *Claims
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetUserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// Pass token via query param (for SSE connections)
	req := httptest.NewRequest(http.MethodGet, "/test?token="+token, nil)
	rr := httptest.NewRecorder()

	m.Authenticate(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedClaims)
	assert.Equal(t, "user-123", capturedClaims.UserID)
}

func TestRequireAdmin_WithAdminRole(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create context with admin user
	claims := &Claims{
		UserID: "some-user",
		Role:   "admin",
	}
	ctx := context.WithValue(context.Background(), userContextKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	m.RequireAdmin(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequireAdmin_WithAdminID(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create context with user in admin list (not admin role)
	claims := &Claims{
		UserID: "admin-user-1", // In the admin IDs list
		Role:   "user",
	}
	ctx := context.WithValue(context.Background(), userContextKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	m.RequireAdmin(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequireAdmin_NonAdmin(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create context with regular user
	claims := &Claims{
		UserID: "regular-user",
		Role:   "user",
	}
	ctx := context.WithValue(context.Background(), userContextKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	m.RequireAdmin(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestRequireAdmin_NoUser(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rr := httptest.NewRecorder()

	m.RequireAdmin(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestOptionalAuth_WithValidToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	token, err := m.GenerateToken("user-123", "", "test@example.com", "user", time.Hour)
	require.NoError(t, err)

	var capturedClaims *Claims
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetUserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	m.OptionalAuth(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedClaims)
	assert.Equal(t, "user-123", capturedClaims.UserID)
}

func TestOptionalAuth_WithoutToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	var capturedClaims *Claims
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetUserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	m.OptionalAuth(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Nil(t, capturedClaims) // No user in context
}

func TestOptionalAuth_WithInvalidToken(t *testing.T) {
	m := NewAuthMiddleware(testConfig())

	var capturedClaims *Claims
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetUserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr := httptest.NewRecorder()

	m.OptionalAuth(handler).ServeHTTP(rr, req)

	// Should succeed but without user in context
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Nil(t, capturedClaims)
}

func TestExtractBearerToken_ValidHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer test-token-123")

	token := extractBearerToken(req)
	assert.Equal(t, "test-token-123", token)
}

func TestExtractBearerToken_LowercaseBearer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "bearer test-token-123")

	token := extractBearerToken(req)
	assert.Equal(t, "test-token-123", token)
}

func TestExtractBearerToken_NoHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	token := extractBearerToken(req)
	assert.Empty(t, token)
}

func TestExtractBearerToken_WrongScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	token := extractBearerToken(req)
	assert.Empty(t, token)
}

func TestExtractBearerToken_QueryParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?token=query-token-123", nil)

	token := extractBearerToken(req)
	assert.Equal(t, "query-token-123", token)
}

func TestExtractBearerToken_HeaderPreferredOverQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?token=query-token", nil)
	req.Header.Set("Authorization", "Bearer header-token")

	token := extractBearerToken(req)
	assert.Equal(t, "header-token", token)
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	// Test SetRequestID and GetRequestID
	ctx = SetRequestID(ctx, "req-123")
	assert.Equal(t, "req-123", GetRequestID(ctx))

	// Test GetRequestID with empty context
	assert.Empty(t, GetRequestID(context.Background()))

	// Test GetUserFromContext with empty context
	assert.Nil(t, GetUserFromContext(context.Background()))

	// Test GetLogger with empty context (should return default)
	logger := GetLogger(context.Background())
	assert.NotNil(t, logger)
}
