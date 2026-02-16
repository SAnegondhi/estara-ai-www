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
	query := `
		SELECT tier, annual_limit, used_this_period, period_end_date
		FROM v2_evaluation_quotas
		WHERE user_id = $1
	`

	var quota evaluationQuota
	err := h.store.Pool().QueryRow(ctx, query, userID).Scan(&quota.Tier, &quota.AnnualLimit, &quota.UsedThisPeriod, &quota.PeriodEnd)
	if err != nil {
		return evaluationQuota{}, err
	}
	return quota, nil
}

func (h *Handler) fetchEvaluations(ctx context.Context, userID string, evaluationIDs []string) ([]evaluationRow, error) {
	query := `
		SELECT
			e.id,
			e.property_address,
			e.property_city,
			e.property_state,
			e.property_zip,
			e.property_details,
			e.purchase_price,
			e.down_payment_pct,
			e.interest_rate,
			e.loan_term_years,
			e.monthly_rent,
			e.vacancy_rate_pct,
			e.maintenance_cost,
			e.property_tax,
			e.insurance,
			e.hoa_fees,
			e.appreciation_rate,
			e.scenarios,
			e.status,
			r.id AS decision_record_id
		FROM v2_evaluations e
		LEFT JOIN v2_decision_records r ON r.evaluation_id = e.id
		WHERE e.user_id = $1 AND e.id = ANY($2::text[])
	`

	rows, err := h.store.Pool().Query(ctx, query, userID, evaluationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]evaluationRow, 0)
	for rows.Next() {
		var row evaluationRow
		if err := rows.Scan(
			&row.ID,
			&row.PropertyAddress,
			&row.PropertyCity,
			&row.PropertyState,
			&row.PropertyZip,
			&row.PropertyDetails,
			&row.PurchasePrice,
			&row.DownPaymentPct,
			&row.InterestRate,
			&row.LoanTermYears,
			&row.MonthlyRent,
			&row.VacancyRatePct,
			&row.MaintenanceCost,
			&row.PropertyTax,
			&row.Insurance,
			&row.HoaFees,
			&row.AppreciationRate,
			&row.Scenarios,
			&row.Status,
			&row.DecisionRecordID,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func (h *Handler) fetchDecisionRecord(ctx context.Context, recordID string) (decisionRecord, error) {
	query := `
		SELECT
			r.id,
			r.user_id,
			r.exported_at,
			e.id,
			e.property_address,
			e.property_city,
			e.property_state,
			e.property_zip,
			e.property_details,
			e.purchase_price,
			e.down_payment_pct,
			e.interest_rate,
			e.loan_term_years,
			e.monthly_rent,
			e.vacancy_rate_pct,
			e.maintenance_cost,
			e.property_tax,
			e.insurance,
			e.hoa_fees,
			e.appreciation_rate,
			e.scenarios,
			e.status
		FROM v2_decision_records r
		JOIN v2_evaluations e ON r.evaluation_id = e.id
		WHERE r.id = $1
	`

	var record decisionRecord
	var eval evaluationRow
	if err := h.store.Pool().QueryRow(ctx, query, recordID).Scan(
		&record.ID,
		&record.UserID,
		&record.ExportedAt,
		&eval.ID,
		&eval.PropertyAddress,
		&eval.PropertyCity,
		&eval.PropertyState,
		&eval.PropertyZip,
		&eval.PropertyDetails,
		&eval.PurchasePrice,
		&eval.DownPaymentPct,
		&eval.InterestRate,
		&eval.LoanTermYears,
		&eval.MonthlyRent,
		&eval.VacancyRatePct,
		&eval.MaintenanceCost,
		&eval.PropertyTax,
		&eval.Insurance,
		&eval.HoaFees,
		&eval.AppreciationRate,
		&eval.Scenarios,
		&eval.Status,
	); err != nil {
		return decisionRecord{}, err
	}
	record.Evaluation = eval
	return record, nil
}

func (h *Handler) createDecisionRecords(ctx context.Context, userID, reportID string, evaluationIDs []string, now time.Time) (evaluationQuota, error) {
	tx, err := h.store.Pool().Begin(ctx)
	if err != nil {
		return evaluationQuota{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	memo := map[string]interface{}{
		"version":       "2.0",
		"reportId":      reportID,
		"batchExport":   true,
		"propertyCount": len(evaluationIDs),
		"generatedAt":   now.Format(time.RFC3339),
	}
	memoBytes, err := json.Marshal(memo)
	if err != nil {
		_ = tx.Rollback(ctx)
		return evaluationQuota{}, err
	}

	insertQuery := `
		INSERT INTO v2_decision_records (id, evaluation_id, user_id, memo_content)
		VALUES ($1, $2, $3, $4)
	`
	for _, evalID := range evaluationIDs {
		_, err = tx.Exec(ctx, insertQuery, uuid.New().String(), evalID, userID, memoBytes)
		if err != nil {
			return evaluationQuota{}, err
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE v2_evaluations
		SET status = 'EXPORTED'
		WHERE id = ANY($1::text[])
	`, evaluationIDs)
	if err != nil {
		return evaluationQuota{}, err
	}

	var updated evaluationQuota
	err = tx.QueryRow(ctx, `
		UPDATE v2_evaluation_quotas
		SET used_this_period = used_this_period + 1
		WHERE user_id = $1
		RETURNING tier, annual_limit, used_this_period, period_end_date
	`, userID).Scan(&updated.Tier, &updated.AnnualLimit, &updated.UsedThisPeriod, &updated.PeriodEnd)
	if err != nil {
		return evaluationQuota{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return evaluationQuota{}, err
	}
	committed = true

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
