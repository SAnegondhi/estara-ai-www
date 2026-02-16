package discover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/pkg/httputil"
)

type BatchExportRequest struct {
	ReportID      string   `json:"reportId"`
	EvaluationIDs []string `json:"evaluationIds"`
}

type evaluationRow struct {
	ID               string
	PropertyAddress  string
	PropertyCity     string
	PropertyState    string
	PropertyZip      pgtype.Text
	PropertyDetails  []byte
	PurchasePrice    float64
	DownPaymentPct   float64
	InterestRate     float64
	LoanTermYears    int
	MonthlyRent      float64
	VacancyRatePct   float64
	MaintenanceCost  float64
	PropertyTax      float64
	Insurance        float64
	HoaFees          pgtype.Float8
	AppreciationRate float64
	Scenarios        []byte
	Status           string
	DecisionRecordID pgtype.Text
}

// ExportBatchEvaluations handles POST /api/v2/evaluate/batch/export
func (h *Handler) ExportBatchEvaluations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req BatchExportRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.ReportID == "" || len(req.EvaluationIDs) == 0 {
		httputil.BadRequest(w, "reportId and evaluationIds are required")
		return
	}

	userName := pdf.PDFUser{}
	q := h.store.Q()
	if dbUser, err := q.GetUserByID(ctx, user.UserID); err == nil {
		if dbUser.FirstName.Valid {
			userName.FirstName = dbUser.FirstName.String
		}
		if dbUser.LastName.Valid {
			userName.LastName = dbUser.LastName.String
		}
	}

	evaluations, err := h.fetchEvaluations(ctx, user.UserID, req.EvaluationIDs)
	if err != nil {
		h.logger.Error("failed to fetch evaluations", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to fetch evaluations")
		return
	}

	found := map[string]struct{}{}
	for _, eval := range evaluations {
		found[eval.ID] = struct{}{}
	}
	if len(found) != len(req.EvaluationIDs) {
		missing := make([]string, 0)
		for _, id := range req.EvaluationIDs {
			if _, ok := found[id]; !ok {
				missing = append(missing, id)
			}
		}
		httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
			"error":      "Some evaluations not found or not authorized",
			"missingIds": missing,
		})
		return
	}

	incomplete := make([]string, 0)
	alreadyExported := make([]string, 0)
	for _, eval := range evaluations {
		status := strings.ToUpper(eval.Status)
		if status != "COMPLETED" && status != "EXPORTED" {
			incomplete = append(incomplete, eval.ID)
		}
		if eval.DecisionRecordID.Valid {
			alreadyExported = append(alreadyExported, eval.ID)
		}
	}
	if len(incomplete) > 0 {
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":         "All evaluations must be completed before exporting",
			"incompleteIds": incomplete,
		})
		return
	}
	if len(alreadyExported) > 0 {
		if len(alreadyExported) == len(evaluations) {
			httputil.JSON(w, http.StatusOK, map[string]interface{}{
				"success":         true,
				"alreadyExported": true,
				"message":         "All evaluations in this report have already been exported",
				"exportedCount":   len(alreadyExported),
			})
			return
		}
		httputil.JSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":       "Some evaluations have already been exported",
			"exportedIds": alreadyExported,
			"message":     "Please create a new report with only unexported evaluations",
		})
		return
	}

	quota, err := h.getEvaluationQuota(ctx, user.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
				"error": "No active subscription. Please subscribe to export evaluations.",
			})
			return
		}
		h.logger.Error("failed to get evaluation quota", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to check quota")
		return
	}

	if quota.AnnualLimit != -1 && quota.UsedThisPeriod >= quota.AnnualLimit {
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":     "Evaluation quota exceeded",
			"used":      quota.UsedThisPeriod,
			"limit":     quota.AnnualLimit,
			"periodEnd": quota.PeriodEnd.Format(time.RFC3339),
		})
		return
	}

	now := time.Now()
	if now.After(quota.PeriodEnd) {
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":     "Subscription period has expired. Please renew to continue.",
			"periodEnd": quota.PeriodEnd.Format(time.RFC3339),
		})
		return
	}

	evaluationsForPDF := make([]pdf.EvaluationForPDF, 0, len(evaluations))
	for _, eval := range evaluations {
		evalPDF, err := buildEvaluationForPDF(eval)
		if err != nil {
			h.logger.Error("failed to build evaluation for pdf", "error", err, "evaluation_id", eval.ID)
			httputil.Error(w, http.StatusInternalServerError, "failed to prepare evaluations")
			return
		}
		evaluationsForPDF = append(evaluationsForPDF, evalPDF)
	}

	pdfBytes, err := pdf.BuildEvaluationReportPDF(pdf.EvaluationReportPDFRequest{
		ReportID:    req.ReportID,
		Evaluations: evaluationsForPDF,
		GeneratedAt: now.Format(time.RFC3339),
		User:        &userName,
	})
	if err != nil {
		h.logger.Error("failed to generate pdf", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate pdf")
		return
	}

	updatedQuota, err := h.createDecisionRecords(ctx, user.UserID, req.ReportID, req.EvaluationIDs, now)
	if err != nil {
		h.logger.Error("failed to create decision records", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to export evaluations")
		return
	}

	remaining := "unlimited"
	if updatedQuota.AnnualLimit != -1 {
		remaining = strconv.Itoa(updatedQuota.AnnualLimit - updatedQuota.UsedThisPeriod)
	}

	filename := "estara-evaluation-report-" + req.ReportID + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("X-Quota-Used", strconv.Itoa(updatedQuota.UsedThisPeriod))
	w.Header().Set("X-Quota-Remaining", remaining)
	w.Header().Set("X-Properties-Exported", strconv.Itoa(len(evaluations)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// DownloadDecisionRecord handles GET /api/v2/records/{id}/download
func (h *Handler) DownloadDecisionRecord(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	recordID := strings.TrimSpace(chi.URLParam(r, "id"))
	if recordID == "" {
		httputil.BadRequest(w, "record id is required")
		return
	}

	record, err := h.fetchDecisionRecord(ctx, recordID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.JSON(w, http.StatusNotFound, map[string]interface{}{
				"error": "Decision record not found",
			})
			return
		}
		h.logger.Error("failed to fetch decision record", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to download decision record")
		return
	}
	if record.UserID != user.UserID {
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error": "Not authorized to access this record",
		})
		return
	}

	userName := pdf.PDFUser{}
	q := h.store.Q()
	if dbUser, err := q.GetUserByID(ctx, user.UserID); err == nil {
		if dbUser.FirstName.Valid {
			userName.FirstName = dbUser.FirstName.String
		}
		if dbUser.LastName.Valid {
			userName.LastName = dbUser.LastName.String
		}
	}

	evalPDF, err := buildEvaluationForPDF(record.Evaluation)
	if err != nil {
		h.logger.Error("failed to build evaluation pdf", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate report")
		return
	}

	pdfBytes, err := pdf.BuildEvaluationReportPDF(pdf.EvaluationReportPDFRequest{
		ReportID:    record.ID,
		Evaluations: []pdf.EvaluationForPDF{evalPDF},
		GeneratedAt: record.ExportedAt.Format(time.RFC3339),
		User:        &userName,
	})
	if err != nil {
		h.logger.Error("failed to generate pdf", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate report")
		return
	}

	filename := "estara-evaluation-" + record.ID + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

type evaluationQuota struct {
	Tier           string
	AnnualLimit    int
	UsedThisPeriod int
	PeriodEnd      time.Time
}

type decisionRecord struct {
	ID         string
	UserID     string
	ExportedAt time.Time
	Evaluation evaluationRow
}

func (h *Handler) getEvaluationQuota(ctx context.Context, userID string) (evaluationQuota, error) {
	dbQuota, err := h.store.Q().GetV2EvaluationQuota(ctx, userID)
	if err != nil {
		return evaluationQuota{}, err
	}

	return evaluationQuota{
		Tier:           dbQuota.Tier,
		AnnualLimit:    int(dbQuota.AnnualLimit),
		UsedThisPeriod: int(dbQuota.UsedThisPeriod),
		PeriodEnd:      dbQuota.PeriodEndDate.Time,
	}, nil
}

func (h *Handler) fetchEvaluations(ctx context.Context, userID string, evaluationIDs []string) ([]evaluationRow, error) {
	dbRows, err := h.store.Q().FetchEvaluationsByIDs(ctx, queries.FetchEvaluationsByIDsParams{
		UserID: userID,
		Column2: evaluationIDs,
	})
	if err != nil {
		return nil, err
	}

	results := make([]evaluationRow, 0, len(dbRows))
	for _, row := range dbRows {
		results = append(results, evaluationRow{
			ID:               row.ID,
			PropertyAddress:  row.PropertyAddress,
			PropertyCity:     row.PropertyCity,
			PropertyState:    row.PropertyState,
			PropertyZip:      row.PropertyZip,
			PropertyDetails:  row.PropertyDetails,
			PurchasePrice:    row.PurchasePrice,
			DownPaymentPct:   row.DownPaymentPct,
			InterestRate:     row.InterestRate,
			LoanTermYears:    int(row.LoanTermYears),
			MonthlyRent:      row.MonthlyRent,
			VacancyRatePct:   row.VacancyRatePct,
			MaintenanceCost:  row.MaintenanceCost,
			PropertyTax:      row.PropertyTax,
			Insurance:        row.Insurance,
			HoaFees:          row.HoaFees,
			AppreciationRate: row.AppreciationRate,
			Scenarios:        row.Scenarios,
			Status:           row.EStatus,
			DecisionRecordID: row.DecisionRecordID,
		})
	}
	return results, nil
}

func (h *Handler) fetchDecisionRecord(ctx context.Context, recordID string) (decisionRecord, error) {
	dbRow, err := h.store.Q().GetDecisionRecordByID(ctx, recordID)
	if err != nil {
		return decisionRecord{}, err
	}

	eval := evaluationRow{
		ID:               dbRow.EvaluationID,
		PropertyAddress:  dbRow.PropertyAddress,
		PropertyCity:     dbRow.PropertyCity,
		PropertyState:    dbRow.PropertyState,
		PropertyZip:      dbRow.PropertyZip,
		PropertyDetails:  dbRow.PropertyDetails,
		PurchasePrice:    dbRow.PurchasePrice,
		DownPaymentPct:   dbRow.DownPaymentPct,
		InterestRate:     dbRow.InterestRate,
		LoanTermYears:    int(dbRow.LoanTermYears),
		MonthlyRent:      dbRow.MonthlyRent,
		VacancyRatePct:   dbRow.VacancyRatePct,
		MaintenanceCost:  dbRow.MaintenanceCost,
		PropertyTax:      dbRow.PropertyTax,
		Insurance:        dbRow.Insurance,
		HoaFees:          dbRow.HoaFees,
		AppreciationRate: dbRow.AppreciationRate,
		Scenarios:        dbRow.Scenarios,
		Status:           dbRow.EStatus,
	}

	return decisionRecord{
		ID:         dbRow.ID,
		UserID:     dbRow.UserID,
		ExportedAt: dbRow.ExportedAt.Time,
		Evaluation: eval,
	}, nil
}

func (h *Handler) createDecisionRecords(ctx context.Context, userID, reportID string, evaluationIDs []string, now time.Time) (evaluationQuota, error) {
	memo := map[string]interface{}{
		"version":       "2.0",
		"reportId":      reportID,
		"batchExport":   true,
		"propertyCount": len(evaluationIDs),
		"generatedAt":   now.Format(time.RFC3339),
	}
	memoBytes, err := json.Marshal(memo)
	if err != nil {
		return evaluationQuota{}, err
	}

	var updated evaluationQuota
	err = h.store.WithTx(ctx, func(q *queries.Queries) error {
		// Create decision records for each evaluation
		for _, evalID := range evaluationIDs {
			_, err := q.CreateDecisionRecord(ctx, queries.CreateDecisionRecordParams{
				ID:           uuid.New().String(),
				EvaluationID: evalID,
				UserID:       userID,
				MemoContent:  memoBytes,
				PdfUrl:       pgtype.Text{Valid: false}, // No PDF URL yet
			})
			if err != nil {
				return err
			}
		}

		// Update all evaluations to EXPORTED status
		err := q.UpdateV2EvaluationsStatusBulk(ctx, queries.UpdateV2EvaluationsStatusBulkParams{
			Column1: evaluationIDs,
			Column2: "EXPORTED",
		})
		if err != nil {
			return err
		}

		// Increment quota usage and get updated values
		dbQuota, err := q.IncrementV2EvaluationQuotaUsageReturning(ctx, userID)
		if err != nil {
			return err
		}

		updated = evaluationQuota{
			Tier:           dbQuota.Tier,
			AnnualLimit:    int(dbQuota.AnnualLimit),
			UsedThisPeriod: int(dbQuota.UsedThisPeriod),
			PeriodEnd:      dbQuota.PeriodEndDate.Time,
		}
		return nil
	})

	if err != nil {
		return evaluationQuota{}, err
	}
	return updated, nil
}

func buildEvaluationForPDF(eval evaluationRow) (pdf.EvaluationForPDF, error) {
	if len(eval.Scenarios) == 0 {
		return pdf.EvaluationForPDF{}, errors.New("evaluation scenarios missing")
	}

	var scenarios pdf.EvaluationScenariosPDF
	if err := json.Unmarshal(eval.Scenarios, &scenarios); err != nil {
		return pdf.EvaluationForPDF{}, err
	}

	var details map[string]interface{}
	if len(eval.PropertyDetails) > 0 {
		_ = json.Unmarshal(eval.PropertyDetails, &details)
	}

	property := pdf.EvaluationPropertyPDF{
		Address: eval.PropertyAddress,
		City:    eval.PropertyCity,
		State:   eval.PropertyState,
		Beds:    getInt(details, "beds"),
		Baths:   getFloat(details, "baths"),
		Sqft:    getInt(details, "sqft"),
	}
	if eval.PropertyZip.Valid {
		property.ZipCode = eval.PropertyZip.String
	}

	assumptions := pdf.EvaluationAssumptionsPDF{
		PurchasePrice:    eval.PurchasePrice,
		DownPaymentPct:   eval.DownPaymentPct,
		InterestRate:     eval.InterestRate,
		LoanTermYears:    eval.LoanTermYears,
		MonthlyRent:      eval.MonthlyRent,
		VacancyRatePct:   eval.VacancyRatePct,
		MaintenanceCost:  eval.MaintenanceCost,
		PropertyTax:      eval.PropertyTax,
		Insurance:        eval.Insurance,
		AppreciationRate: eval.AppreciationRate,
	}
	if eval.HoaFees.Valid {
		assumptions.HoaFees = &eval.HoaFees.Float64
	}

	return pdf.EvaluationForPDF{
		ID:          eval.ID,
		Property:    property,
		Assumptions: assumptions,
		Scenarios:   scenarios,
	}, nil
}

func getInt(data map[string]interface{}, key string) int {
	if data == nil {
		return 0
	}
	val, ok := data[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return 0
}

func getFloat(data map[string]interface{}, key string) float64 {
	if data == nil {
		return 0
	}
	val, ok := data[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0
}
