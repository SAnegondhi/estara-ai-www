package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/pkg/httputil"
)

// Context keys for user data
type contextKey string

const (
	userContextKey    contextKey = "user"
	requestIDKey      contextKey = "request_id"
	loggerContextKey  contextKey = "logger"
)

var (
	ErrMissingToken   = errors.New("missing authorization token")
	ErrInvalidToken   = errors.New("invalid token")
	ErrTokenExpired   = errors.New("token has expired")
	ErrInvalidClaims  = errors.New("invalid token claims")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
)

// Claims represents the JWT claims structure
type Claims struct {
	UserID       string `json:"userId"`
	ClerkUserID  string `json:"clerkUserId,omitempty"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	AdminSession bool   `json:"adminSession,omitempty"`
	jwt.RegisteredClaims
}

// AuthMiddleware handles JWT authentication
type AuthMiddleware struct {
	jwtSecret []byte
	adminIDs  map[string]bool
	logger    *slog.Logger
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(cfg *config.Config) *AuthMiddleware {
	adminIDs := make(map[string]bool)
	for _, id := range cfg.Admin.UserIDs {
		adminIDs[id] = true
	}

	return &AuthMiddleware{
		jwtSecret: []byte(cfg.JWT.Secret),
		adminIDs:  adminIDs,
		logger:    slog.Default(),
	}
}

// Authenticate validates the JWT token and adds user to context
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			httputil.Error(w, http.StatusUnauthorized, ErrMissingToken.Error())
			return
		}

		claims, err := m.validateToken(token)
		if err != nil {
			m.logger.Warn("token validation failed",
				"error", err,
				"path", r.URL.Path,
				"method", r.Method,
			)
			if errors.Is(err, ErrTokenExpired) {
				httputil.Error(w, http.StatusUnauthorized, "token expired")
			} else {
				httputil.Error(w, http.StatusUnauthorized, "invalid token")
			}
			return
		}

		// Add user claims to context
		ctx := context.WithValue(r.Context(), userContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin checks if the user has admin role
func (m *AuthMiddleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			httputil.Error(w, http.StatusUnauthorized, "authentication required")
			return
		}

		// Check if user is admin by role or explicit admin list
		isAdmin := strings.EqualFold(user.Role, "admin") || strings.EqualFold(user.Role, "SUPER_ADMIN") || m.adminIDs[user.UserID]
		if !isAdmin {
			m.logger.Warn("admin access denied",
				"user_id", user.UserID,
				"path", r.URL.Path,
			)
			httputil.Error(w, http.StatusForbidden, "admin access required")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AuthenticateHeaderOnly validates the JWT token from the Authorization header only.
// Unlike Authenticate, it does NOT check cookies. Use this for admin routes to prevent
// regular-user httpOnly cookies from overriding the admin Bearer token.
func (m *AuthMiddleware) AuthenticateHeaderOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerTokenFromHeader(r)
		if token == "" {
			httputil.Error(w, http.StatusUnauthorized, ErrMissingToken.Error())
			return
		}

		claims, err := m.validateToken(token)
		if err != nil {
			m.logger.Warn("token validation failed",
				"error", err,
				"path", r.URL.Path,
				"method", r.Method,
			)
			if errors.Is(err, ErrTokenExpired) {
				httputil.Error(w, http.StatusUnauthorized, "token expired")
			} else {
				httputil.Error(w, http.StatusUnauthorized, "invalid token")
			}
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuth tries to authenticate but doesn't require it
func (m *AuthMiddleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token != "" {
			claims, err := m.validateToken(token)
			if err == nil {
				ctx := context.WithValue(r.Context(), userContextKey, claims)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// validateToken validates the JWT token and returns claims
func (m *AuthMiddleware) validateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method is HMAC (prevent algorithm confusion attacks)
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSecret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaims
	}

	// Additional validation
	if claims.UserID == "" {
		return nil, ErrInvalidClaims
	}

	// Check expiration (belt and suspenders)
	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, ErrTokenExpired
	}

	return claims, nil
}

// GenerateToken creates a new JWT token
func (m *AuthMiddleware) GenerateToken(userID, clerkUserID, email, role string, expiry time.Duration) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:      userID,
		ClerkUserID: clerkUserID,
		Email:       email,
		Role:        role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "estara-ai",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.jwtSecret)
}

// GenerateAdminToken creates an admin-specific JWT with 8-hour expiry
func (m *AuthMiddleware) GenerateAdminToken(userID, email string) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:       userID,
		Email:        email,
		Role:         "admin",
		AdminSession: true,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "estara-ai-admin",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.jwtSecret)
}

// IsAdmin checks if the user ID is in the admin list or has admin role
func (m *AuthMiddleware) IsAdmin(userID string) bool {
	return m.adminIDs[userID]
}

// GenerateRefreshToken creates a long-lived refresh token
func (m *AuthMiddleware) GenerateRefreshToken(userID string, expiry time.Duration) (string, error) {
	now := time.Now()
	claims := &jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    "estara-ai",
		Subject:   userID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.jwtSecret)
}

// ValidateRefreshToken validates a refresh token and returns the user ID
func (m *AuthMiddleware) ValidateRefreshToken(tokenString string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSecret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", ErrTokenExpired
		}
		return "", fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return "", ErrInvalidClaims
	}

	if claims.Subject == "" {
		return "", ErrInvalidClaims
	}

	return claims.Subject, nil
}

// DecodeTokenWithoutValidation decodes a JWT token without validating expiration
// Used for token refresh where we need to extract claims from expired tokens
func (m *AuthMiddleware) DecodeTokenWithoutValidation(tokenString string) (*Claims, error) {
	// First try to parse with validation
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSecret, nil
	})

	// If token is expired, we can still extract claims
	if err != nil && !errors.Is(err, jwt.ErrTokenExpired) {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	if token == nil {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidClaims
	}

	// Validate that we have essential claims
	if claims.UserID == "" {
		return nil, ErrInvalidClaims
	}

	return claims, nil
}

// extractBearerToken extracts the token from cookie, Authorization header, or query param
// ADR-066: Priority order: Cookie > Authorization header > Query param
func extractBearerToken(r *http.Request) string {
	// PRIMARY: Try httpOnly cookie first (ADR-066)
	if token := GetAccessToken(r); token != "" {
		return token
	}

	// FALLBACK: Authorization header (backward compatibility during migration)
	auth := r.Header.Get("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return parts[1]
		}
	}

	// FALLBACK: Query param for SSE connections
	// Note: SSE with cookies still uses query param for CSRF validation
	return r.URL.Query().Get("token")
}

// extractBearerTokenFromHeader extracts the token from the Authorization header only.
// Does NOT check cookies — used for admin routes to prevent regular-user cookies
// from shadowing the admin Bearer token when both are present in the browser.
func extractBearerTokenFromHeader(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return parts[1]
		}
	}
	return ""
}

// GetUserFromContext retrieves the user claims from context
func GetUserFromContext(ctx context.Context) *Claims {
	claims, ok := ctx.Value(userContextKey).(*Claims)
	if !ok {
		return nil
	}
	return claims
}

// GetRequestID retrieves the request ID from context
func GetRequestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey).(string)
	if !ok {
		return ""
	}
	return id
}

// GetLogger retrieves the logger from context
func GetLogger(ctx context.Context) *slog.Logger {
	logger, ok := ctx.Value(loggerContextKey).(*slog.Logger)
	if !ok {
		return slog.Default()
	}
	return logger
}

// SetRequestID adds request ID to context
func SetRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// SetLogger adds logger to context
func SetLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}
