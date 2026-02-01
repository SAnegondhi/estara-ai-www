package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

// Flush implements http.Flusher for SSE support
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// RequestID adds a unique request ID to each request
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Add to response header
		w.Header().Set("X-Request-ID", requestID)

		// Add to context
		ctx := SetRequestID(r.Context(), requestID)

		// Add to logger context
		logger := slog.With("request_id", requestID)
		ctx = SetLogger(ctx, logger)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logger logs HTTP requests
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Get logger from context (has request_id)
		logger := GetLogger(r.Context())

		// Process request
		next.ServeHTTP(wrapped, r)

		// Calculate duration
		duration := time.Since(start)

		// Log based on status code
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"bytes", wrapped.written,
		}

		// Add query params if present (but not sensitive ones)
		if r.URL.RawQuery != "" {
			attrs = append(attrs, "query", sanitizeQuery(r.URL.RawQuery))
		}

		// Add user ID if authenticated
		if user := GetUserFromContext(r.Context()); user != nil {
			attrs = append(attrs, "user_id", user.UserID)
		}

		// Log at appropriate level based on status
		switch {
		case wrapped.statusCode >= 500:
			logger.Error("request completed", attrs...)
		case wrapped.statusCode >= 400:
			logger.Warn("request completed", attrs...)
		default:
			logger.Info("request completed", attrs...)
		}
	})
}

// sanitizeQuery removes sensitive query parameters
func sanitizeQuery(query string) string {
	// In production, implement proper sanitization
	// For now, just return as-is but truncate if too long
	if len(query) > 200 {
		return query[:200] + "..."
	}
	return query
}
