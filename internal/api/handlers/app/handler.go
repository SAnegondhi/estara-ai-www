// Package app provides handlers for app-related endpoints
package app

import (
	"log/slog"
	"net/http"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/services/entitlements"
	"github.com/estara-ai/www/internal/services/scenarios"
	"github.com/estara-ai/www/pkg/httputil"
)

// Handler handles app-related HTTP requests
type Handler struct {
	store               *dbstore.Store
	cfg                 *config.Config
	entitlementService  *entitlements.Service
	logger              *slog.Logger
	Scenarios           *ScenariosHandler
}

// NewHandler creates a new app handler
func NewHandler(store *dbstore.Store, cfg *config.Config) *Handler {
	scenariosService := scenarios.NewService(store, cfg)
	return &Handler{
		store:              store,
		cfg:                cfg,
		entitlementService: entitlements.NewService(store, cfg),
		logger:             slog.Default().With("component", "app_handler"),
		Scenarios:          NewScenariosHandler(store, scenariosService),
	}
}

// EntitlementsRequest represents an entitlements request
type EntitlementsRequest struct {
	UserID string `json:"userId"`
}

// GetEntitlements returns user entitlements
// POST /api/app/entitlements
func (h *Handler) GetEntitlements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user from context (set by auth middleware)
	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Optionally get userId from body to verify match
	var req EntitlementsRequest
	if err := httputil.DecodeJSON(r, &req); err == nil {
		// If userId provided, verify it matches the authenticated user
		if req.UserID != "" && req.UserID != claims.UserID {
			httputil.JSON(w, http.StatusForbidden, map[string]string{
				"error": "User ID mismatch",
			})
			return
		}
	}

	// Get entitlements
	entitlements, err := h.entitlementService.GetEntitlements(ctx, claims.UserID)
	if err != nil {
		h.logger.Error("failed to get entitlements", "error", err, "user_id", claims.UserID)
		httputil.Error(w, http.StatusInternalServerError, "Failed to get entitlements")
		return
	}

	// Return entitlements directly (not wrapped in data object) to match www_v1 format
	httputil.JSON(w, http.StatusOK, entitlements)
}
