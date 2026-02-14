package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/estara-ai/www/internal/services/market/importer"
	"github.com/estara-ai/www/pkg/httputil"
)

// SetImporter injects the market data importer service.
func (h *Handler) SetImporter(imp *importer.Service) {
	h.importer = imp
}

// MarketDataStatus returns the current status of all market data tables.
func (h *Handler) MarketDataStatus(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "market importer not configured")
		return
	}

	status, err := h.importer.Status(r.Context())
	if err != nil {
		h.logger.Error("failed to get market data status", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to get market data status")
		return
	}

	httputil.Success(w, status)
}

// TriggerMarketDataImport triggers a market data import via internal HTTP call.
func (h *Handler) TriggerMarketDataImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Source == "" {
		httputil.BadRequest(w, "source is required")
		return
	}

	// Map source to cron endpoint
	endpointMap := map[string]string{
		"zillow-zhvi":      "/api/cron/market-data/zillow-zhvi",
		"zillow-zori":      "/api/cron/market-data/zillow-zori",
		"zillow-forecasts": "/api/cron/market-data/zillow-forecasts",
		"zillow-metrics":   "/api/cron/market-data/zillow-metrics",
		"redfin":           "/api/cron/market-data/redfin",
		"compute-national": "/api/cron/market-data/compute-national",
		"full-refresh":     "/api/cron/market-data/full-refresh",
	}

	endpoint, ok := endpointMap[req.Source]
	if !ok {
		httputil.BadRequest(w, fmt.Sprintf("unknown source: %s", req.Source))
		return
	}

	port := h.cfg.Server.Port
	if port == 0 {
		port = 3000
	}
	url := fmt.Sprintf("http://localhost:%d%s", port, endpoint)

	client := &http.Client{Timeout: 5 * time.Minute}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		h.logger.Error("failed to create import request", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	httpReq.Header.Set("X-Cron-Secret", h.cfg.Cron.Secret)

	resp, err := client.Do(httpReq)
	if err != nil {
		h.logger.Error("failed to trigger market data import", "error", err, "source", req.Source)
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("failed to trigger import: %v", err))
		return
	}
	defer resp.Body.Close()

	h.logAdminAudit(ctx, r, "MARKET_DATA_IMPORT", "market_data", "", map[string]interface{}{
		"source":     req.Source,
		"endpoint":   endpoint,
		"statusCode": resp.StatusCode,
	})

	httputil.Success(w, map[string]interface{}{
		"source":     req.Source,
		"triggered":  true,
		"statusCode": resp.StatusCode,
	})
}
