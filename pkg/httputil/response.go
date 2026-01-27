package httputil

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
)

// SuccessResponse represents a successful API response
type SuccessResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
}

// ErrorResponse represents an error API response
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// PaginatedResponse represents a paginated API response
type PaginatedResponse struct {
	Success    bool        `json:"success"`
	Data       interface{} `json:"data"`
	Pagination Pagination  `json:"pagination"`
}

// Pagination holds pagination metadata
type Pagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"pageSize"`
	TotalCount int64 `json:"totalCount"`
	TotalPages int   `json:"totalPages"`
}

// JSON writes a JSON response with the given status code
func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// Success writes a successful JSON response
func Success(w http.ResponseWriter, data interface{}) {
	JSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Data:    data,
	})
}

// Created writes a 201 Created response
func Created(w http.ResponseWriter, data interface{}) {
	JSON(w, http.StatusCreated, SuccessResponse{
		Success: true,
		Data:    data,
	})
}

// NoContent writes a 204 No Content response
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Error writes an error JSON response
// In production, details are hidden from the response
func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, ErrorResponse{
		Success: false,
		Error:   message,
	})
}

// ErrorWithDetails writes an error JSON response with details
// Details are only included in development mode
func ErrorWithDetails(w http.ResponseWriter, status int, message string, details error) {
	response := ErrorResponse{
		Success: false,
		Error:   message,
	}

	// Only include details in development
	if os.Getenv("ENV") == "development" && details != nil {
		response.Details = details.Error()
	}

	// Log the full error server-side
	slog.Error("request error",
		"status", status,
		"message", message,
		"details", details,
	)

	JSON(w, status, response)
}

// Paginated writes a paginated JSON response
func Paginated(w http.ResponseWriter, data interface{}, page, pageSize int, totalCount int64) {
	totalPages := int(totalCount) / pageSize
	if int(totalCount)%pageSize > 0 {
		totalPages++
	}

	JSON(w, http.StatusOK, PaginatedResponse{
		Success: true,
		Data:    data,
		Pagination: Pagination{
			Page:       page,
			PageSize:   pageSize,
			TotalCount: totalCount,
			TotalPages: totalPages,
		},
	})
}

// BadRequest writes a 400 Bad Request response
func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, message)
}

// Unauthorized writes a 401 Unauthorized response
func Unauthorized(w http.ResponseWriter, message string) {
	if message == "" {
		message = "unauthorized"
	}
	Error(w, http.StatusUnauthorized, message)
}

// Forbidden writes a 403 Forbidden response
func Forbidden(w http.ResponseWriter, message string) {
	if message == "" {
		message = "forbidden"
	}
	Error(w, http.StatusForbidden, message)
}

// NotFound writes a 404 Not Found response
func NotFound(w http.ResponseWriter, message string) {
	if message == "" {
		message = "not found"
	}
	Error(w, http.StatusNotFound, message)
}

// InternalError writes a 500 Internal Server Error response
func InternalError(w http.ResponseWriter, err error) {
	// Don't expose internal errors in production
	message := "internal server error"
	if os.Getenv("ENV") == "development" && err != nil {
		message = err.Error()
	}

	// Always log the full error
	slog.Error("internal server error", "error", err)

	Error(w, http.StatusInternalServerError, message)
}

// DecodeJSON decodes a JSON request body into the provided struct
func DecodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// GetQueryParam returns a query parameter with a default value
func GetQueryParam(r *http.Request, key, defaultValue string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// GetQueryParamInt returns a query parameter as int with a default value
func GetQueryParamInt(r *http.Request, key string, defaultValue int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return defaultValue
	}

	var result int
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return defaultValue
	}
	return result
}
