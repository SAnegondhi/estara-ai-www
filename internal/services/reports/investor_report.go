// Package reports provides report generation and management services
package reports

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/market/economics"
)

var (
	ErrReportNotFound    = errors.New("report not found")
	ErrUnauthorized      = errors.New("unauthorized to access report")
	ErrInsufficientQuota = errors.New("insufficient report quota")
)

// ReportType represents the type of investor report
type ReportType string

const (
	ReportTypeSnapshot      ReportType = "SNAPSHOT"
	ReportTypeMIR           ReportType = "MIR"
	ReportTypeCIP           ReportType = "CIP"
	ReportTypeComprehensive ReportType = "COMPREHENSIVE"
)

// ReportStatus represents the status of a report
type ReportStatus string

const (
	ReportStatusPending    ReportStatus = "PENDING"
	ReportStatusGenerating ReportStatus = "GENERATING"
	ReportStatusComplete   ReportStatus = "COMPLETE"
	ReportStatusFailed     ReportStatus = "FAILED"
	ReportStatusExpired    ReportStatus = "EXPIRED"
)

// SourceType represents how the report was funded
type SourceType string

const (
	SourceTypeInsightAccess SourceType = "INSIGHT_ACCESS"
	SourceTypeReportPack    SourceType = "REPORT_PACK"
	SourceTypeSinglePurchase SourceType = "SINGLE_PURCHASE"
	SourceTypeFreeSnapshot  SourceType = "FREE_SNAPSHOT"
)

// InvestorReportService handles investor report operations
type InvestorReportService struct {
	store           *dbstore.Store
	cfg             *config.Config
	logger          *slog.Logger
	// ADR-069: Economic backdrop service for reports
	economicBackdrop *EconomicBackdropService
}

// NewInvestorReportService creates a new investor report service
func NewInvestorReportService(store *dbstore.Store, cfg *config.Config) *InvestorReportService {
	return &InvestorReportService{
		store:  store,
		cfg:    cfg,
		logger: slog.Default().With("component", "investor_report_service"),
	}
}

// NewInvestorReportServiceWithEconomics creates a service with economic data integration (ADR-069)
func NewInvestorReportServiceWithEconomics(store *dbstore.Store, cfg *config.Config, econ economics.Provider) *InvestorReportService {
	s := NewInvestorReportService(store, cfg)
	if econ != nil {
		s.economicBackdrop = NewEconomicBackdropService(econ)
	}
	return s
}

// GetEconomicBackdrop generates economic backdrop for a report
// ADR-069: Provides current economic conditions for report context
func (s *InvestorReportService) GetEconomicBackdrop(ctx context.Context, city, state string, medianHomePrice float64) (*EconomicBackdrop, error) {
	if s.economicBackdrop == nil {
		return nil, nil
	}
	return s.economicBackdrop.GenerateBackdrop(ctx, city, state, medianHomePrice)
}

// InvestorReport represents an investor report for API responses
type InvestorReport struct {
	ID              string                 `json:"id"`
	UserID          *string                `json:"userId,omitempty"`
	Email           *string                `json:"email,omitempty"`
	PropertyID      *string                `json:"propertyId,omitempty"`
	PropertyAddress *string                `json:"propertyAddress,omitempty"`
	Type            ReportType             `json:"type"`
	Status          ReportStatus           `json:"status"`
	SourceType      SourceType             `json:"sourceType"`
	MetricsData     map[string]interface{} `json:"metricsData,omitempty"`
	NarrativeData   map[string]interface{} `json:"narrativeData,omitempty"`
	FullReport      *string                `json:"fullReport,omitempty"`
	PdfURL          *string                `json:"pdfUrl,omitempty"`
	CacheKey        *string                `json:"cacheKey,omitempty"`
	CreatedAt       string                 `json:"createdAt"`
	CompletedAt     *string                `json:"completedAt,omitempty"`
}

// GenerateReportInput represents input for generating a report
type GenerateReportInput struct {
	PropertyID         string                 `json:"propertyId"`
	PropertyAddress    string                 `json:"propertyAddress"`
	Type               ReportType             `json:"type"`
	InvestmentCriteria map[string]interface{} `json:"investmentCriteria"`
	CacheKey           *string                `json:"cacheKey,omitempty"`
}

// List returns all investor reports for a user
func (s *InvestorReportService) List(ctx context.Context, userID string, limit, offset int32) ([]InvestorReport, int64, error) {
	// Get total count
	total, err := s.store.Q().CountUserInvestorReports(ctx, pgtype.Text{String: userID, Valid: true})
	if err != nil {
		return nil, 0, err
	}

	// Get reports with pagination
	dbReports, err := s.store.Q().ListUserInvestorReports(ctx, queries.ListUserInvestorReportsParams{
		UserId: pgtype.Text{String: userID, Valid: true},
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, 0, err
	}

	reports := make([]InvestorReport, len(dbReports))
	for i, dbReport := range dbReports {
		reports[i] = s.toInvestorReport(dbReport)
	}

	return reports, total, nil
}

// Get returns a specific investor report by ID
func (s *InvestorReportService) Get(ctx context.Context, userID, reportID string) (*InvestorReport, error) {
	dbReport, err := s.store.Q().GetInvestorReportByIDAndUser(ctx, queries.GetInvestorReportByIDAndUserParams{
		ID:     reportID,
		UserId: pgtype.Text{String: userID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReportNotFound
		}
		return nil, err
	}

	report := s.toInvestorReport(dbReport)
	return &report, nil
}

// GetStatus returns the status of a report
func (s *InvestorReportService) GetStatus(ctx context.Context, userID, reportID string) (*InvestorReport, error) {
	// Same as Get but could be optimized to only fetch status fields
	return s.Get(ctx, userID, reportID)
}

// Generate creates a new investor report request
func (s *InvestorReportService) Generate(ctx context.Context, userID string, email string, input GenerateReportInput) (*InvestorReport, error) {
	// Check if user has quota (from InsightAccess or ReportPack)
	sourceType, sourceID, err := s.checkAndConsumeQuota(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Generate IDs
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Generate criteria hash for caching
	criteriaHash := s.generateCriteriaHash(input)

	// Check for cached report with same criteria
	if cachedReport, err := s.store.Q().GetInvestorReportByCriteriaHash(ctx, pgtype.Text{String: criteriaHash, Valid: true}); err == nil {
		// Return cached report instead of generating new one
		report := s.toInvestorReport(cachedReport)
		return &report, nil
	}

	// Serialize investment criteria
	criteriaJSON, _ := json.Marshal(input.InvestmentCriteria)

	// Create report record
	var sourcePackID, sourceAccessID pgtype.Text
	if sourceType == SourceTypeReportPack {
		sourcePackID = pgtype.Text{String: sourceID, Valid: true}
	} else if sourceType == SourceTypeInsightAccess {
		sourceAccessID = pgtype.Text{String: sourceID, Valid: true}
	}

	var cacheKey pgtype.Text
	if input.CacheKey != nil {
		cacheKey = pgtype.Text{String: *input.CacheKey, Valid: true}
	}

	dbReport, err := s.store.Q().CreateInvestorReport(ctx, queries.CreateInvestorReportParams{
		ID:                 id,
		UserId:             pgtype.Text{String: userID, Valid: true},
		Email:              pgtype.Text{String: email, Valid: true},
		PropertyId:         pgtype.Text{String: input.PropertyID, Valid: true},
		PropertyAddress:    pgtype.Text{String: input.PropertyAddress, Valid: true},
		Type:               string(input.Type),
		Status:             string(ReportStatusPending),
		SourceType:         string(sourceType),
		SourcePackId:       sourcePackID,
		SourceAccessId:     sourceAccessID,
		InvestmentCriteria: criteriaJSON,
		CacheKey:           cacheKey,
		CriteriaHash:       pgtype.Text{String: criteriaHash, Valid: true},
	})
	if err != nil {
		return nil, err
	}

	// TODO: Queue report generation job

	report := s.toInvestorReport(dbReport)
	return &report, nil
}

// GetDownloadURL returns the download URL for a completed report
func (s *InvestorReportService) GetDownloadURL(ctx context.Context, userID, reportID string) (string, error) {
	report, err := s.Get(ctx, userID, reportID)
	if err != nil {
		return "", err
	}

	if report.Status != ReportStatusComplete {
		return "", errors.New("report not yet complete")
	}

	if report.PdfURL == nil {
		return "", errors.New("PDF not available")
	}

	// TODO: Generate signed URL for S3/storage
	return *report.PdfURL, nil
}

// checkAndConsumeQuota checks if user has report quota and consumes one
func (s *InvestorReportService) checkAndConsumeQuota(ctx context.Context, userID string) (SourceType, string, error) {
	// First check InsightAccess (annual subscription)
	insightAccess, err := s.store.Q().GetInsightAccessByUserID(ctx, userID)
	if err == nil {
		// Has active InsightAccess - check if reports remain
		remaining := int(insightAccess.ReportsPerPeriod - insightAccess.ReportsUsed + insightAccess.RolloverReports)
		if remaining > 0 {
			// Consume from InsightAccess
			_, err := s.store.Q().IncrementInsightAccessUsage(ctx, insightAccess.ID)
			if err != nil {
				return "", "", err
			}
			return SourceTypeInsightAccess, insightAccess.ID, nil
		}
	}

	// Check ReportPacks (FIFO order)
	reportPacks, err := s.store.Q().GetReportPacksByUserID(ctx, userID)
	if err == nil && len(reportPacks) > 0 {
		// Use oldest pack with remaining reports
		pack := reportPacks[0]
		_, err := s.store.Q().IncrementReportPackUsage(ctx, pack.ID)
		if err != nil {
			return "", "", err
		}
		return SourceTypeReportPack, pack.ID, nil
	}

	return "", "", ErrInsufficientQuota
}

// generateCriteriaHash generates a hash for caching purposes
func (s *InvestorReportService) generateCriteriaHash(input GenerateReportInput) string {
	data := map[string]interface{}{
		"propertyId":         input.PropertyID,
		"type":               input.Type,
		"investmentCriteria": input.InvestmentCriteria,
	}
	jsonBytes, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonBytes)
	return hex.EncodeToString(hash[:])
}

// toInvestorReport converts a database model to API response
func (s *InvestorReportService) toInvestorReport(dbReport queries.InvestorReport) InvestorReport {
	report := InvestorReport{
		ID:         dbReport.ID,
		Type:       ReportType(dbReport.Type.(string)),
		Status:     ReportStatus(dbReport.Status.(string)),
		SourceType: SourceType(dbReport.SourceType.(string)),
		CreatedAt:  dbReport.CreatedAt.Time.Format(time.RFC3339),
	}

	if dbReport.UserId.Valid {
		report.UserID = &dbReport.UserId.String
	}
	if dbReport.Email.Valid {
		report.Email = &dbReport.Email.String
	}
	if dbReport.PropertyId.Valid {
		report.PropertyID = &dbReport.PropertyId.String
	}
	if dbReport.PropertyAddress.Valid {
		report.PropertyAddress = &dbReport.PropertyAddress.String
	}
	if dbReport.FullReport.Valid {
		report.FullReport = &dbReport.FullReport.String
	}
	if dbReport.PdfUrl.Valid {
		report.PdfURL = &dbReport.PdfUrl.String
	}
	if dbReport.CacheKey.Valid {
		report.CacheKey = &dbReport.CacheKey.String
	}
	if dbReport.CompletedAt.Valid {
		completedAt := dbReport.CompletedAt.Time.Format(time.RFC3339)
		report.CompletedAt = &completedAt
	}

	// Parse JSON fields
	if len(dbReport.MetricsData) > 0 {
		var metrics map[string]interface{}
		if err := json.Unmarshal(dbReport.MetricsData, &metrics); err == nil {
			report.MetricsData = metrics
		}
	}
	if len(dbReport.NarrativeData) > 0 {
		var narrative map[string]interface{}
		if err := json.Unmarshal(dbReport.NarrativeData, &narrative); err == nil {
			report.NarrativeData = narrative
		}
	}

	return report
}
