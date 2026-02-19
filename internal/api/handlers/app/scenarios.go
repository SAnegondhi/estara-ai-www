package app

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/services/scenarios"
	"github.com/estara-ai/www/pkg/httputil"
)

// ScenariosHandler handles scenario-related HTTP requests
type ScenariosHandler struct {
	store   *db.Store
	service *scenarios.Service
}

// NewScenariosHandler creates a new scenarios handler
func NewScenariosHandler(store *db.Store, service *scenarios.Service) *ScenariosHandler {
	return &ScenariosHandler{
		store:   store,
		service: service,
	}
}

// ListScenarios returns all scenarios for the authenticated user
// GET /api/app/scenarios
func (h *ScenariosHandler) ListScenarios(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	scenarioList, err := h.service.List(ctx, claims.UserID)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "Failed to retrieve scenarios")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"scenarios": scenarioList,
	})
}

// GetScenario returns a specific scenario by ID
// GET /api/app/scenarios/{id}
func (h *ScenariosHandler) GetScenario(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	scenarioID := chi.URLParam(r, "id")
	if scenarioID == "" {
		httputil.BadRequest(w, "Scenario ID is required")
		return
	}

	scenario, err := h.service.Get(ctx, claims.UserID, scenarioID)
	if err != nil {
		if errors.Is(err, scenarios.ErrScenarioNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"error":   "Not found",
				"message": "Scenario not found",
			})
			return
		}
		httputil.Error(w, http.StatusInternalServerError, "Failed to retrieve scenario")
		return
	}

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, claims.UserID, "FEATURE_SCENARIO_VIEW",
		fmt.Sprintf("Scenario viewed: %s", scenarioID),
		map[string]any{
			"scenarioId": scenarioID,
		},
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"scenario": scenario,
	})
}

// CreateScenario creates a new scenario
// POST /api/app/scenarios
func (h *ScenariosHandler) CreateScenario(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var input scenarios.CreateScenarioInput
	if err := httputil.DecodeJSON(r, &input); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if input.Name == "" || input.Parameters == nil {
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "Bad Request",
			"message": "Name and parameters are required",
		})
		return
	}

	scenario, err := h.service.Create(ctx, claims.UserID, input)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "Failed to create scenario")
		return
	}

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, claims.UserID, "FEATURE_SCENARIO_CREATE",
		fmt.Sprintf("Scenario created: %s", input.Name),
		map[string]any{
			"scenarioId":   scenario.ID,
			"scenarioName": input.Name,
		},
	)

	httputil.JSON(w, http.StatusCreated, map[string]interface{}{
		"success":  true,
		"scenario": scenario,
	})
}

// UpdateScenario updates an existing scenario
// PUT /api/app/scenarios/{id}
func (h *ScenariosHandler) UpdateScenario(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	scenarioID := chi.URLParam(r, "id")
	if scenarioID == "" {
		httputil.BadRequest(w, "Scenario ID is required")
		return
	}

	var input scenarios.UpdateScenarioInput
	if err := httputil.DecodeJSON(r, &input); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	scenario, err := h.service.Update(ctx, claims.UserID, scenarioID, input)
	if err != nil {
		if errors.Is(err, scenarios.ErrScenarioNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"error":   "Not found",
				"message": "Scenario not found",
			})
			return
		}
		httputil.Error(w, http.StatusInternalServerError, "Failed to update scenario")
		return
	}

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, claims.UserID, "FEATURE_SCENARIO_UPDATE",
		fmt.Sprintf("Scenario updated: %s", scenarioID),
		map[string]any{
			"scenarioId": scenarioID,
		},
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"scenario": scenario,
	})
}

// DeleteScenario deletes a scenario
// DELETE /api/app/scenarios/{id}
func (h *ScenariosHandler) DeleteScenario(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	scenarioID := chi.URLParam(r, "id")
	if scenarioID == "" {
		httputil.BadRequest(w, "Scenario ID is required")
		return
	}

	err := h.service.Delete(ctx, claims.UserID, scenarioID)
	if err != nil {
		if errors.Is(err, scenarios.ErrScenarioNotFound) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"error":   "Not found",
				"message": "Scenario not found",
			})
			return
		}
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete scenario")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Scenario deleted successfully",
	})
}
