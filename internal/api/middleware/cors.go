package middleware

import (
	"net/http"

	"github.com/go-chi/cors"

	"github.com/estara-ai/www/internal/config"
)

// NewCORSMiddleware creates CORS middleware with strict configuration
func NewCORSMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	allowedOrigins := cfg.CORS.AllowedOrigins
	if len(allowedOrigins) == 0 {
		// Default allowed origins for development
		if cfg.IsDevelopment() {
			allowedOrigins = []string{
				"http://localhost:3002",  // client
				"http://localhost:3500",  // website
				"http://localhost:3100",  // scout API
				"http://localhost:3101",  // scout web
			}
		}
	}

	return cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-ID", "X-CSRF-Token", "Accept", "Origin"},
		ExposedHeaders:   []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "Retry-After"},
		AllowCredentials: cfg.CORS.AllowCredentials,
		MaxAge:           86400, // 24 hours
	})
}
