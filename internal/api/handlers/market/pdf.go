package market

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/pkg/httputil"
)

// ExportTrendsPDF handles POST /api/market-trends/export
func (h *Handler) ExportTrendsPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "authentication required")
		return
	}

	var req pdf.MarketTrendsExportRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	pdfBytes, err := pdf.BuildMarketTrendsPDF(ctx, req)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "failed to generate pdf")
		return
	}

	filename := "market-trends.pdf"
	if req.Data.CanonicalName != "" {
		filename = "market-trends-" + sanitizeFilename(req.Data.CanonicalName) + ".pdf"
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

func sanitizeFilename(value string) string {
	safe := strings.ToLower(strings.TrimSpace(value))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	safe = re.ReplaceAllString(safe, "-")
	return strings.Trim(safe, "-")
}
