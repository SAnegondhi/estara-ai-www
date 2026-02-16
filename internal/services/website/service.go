package website

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/billing"
)

const (
	MaxFreeSnapshots = 3
	SnapshotCacheTTL = 24 * time.Hour
)

var (
	ErrSnapshotLimitReached = errors.New("free snapshot limit reached")
	ErrInvalidReportType    = errors.New("invalid report type")
	ErrReportNotFound       = errors.New("report not found")
	ErrOrderNotFound        = errors.New("order not found")
)

// Service handles website operations
type Service struct {
	store   *db.Store
	stripe  *billing.StripeClient
	cfg     *config.Config
	logger  *slog.Logger
}

// NewService creates a new website service
func NewService(store *db.Store, cfg *config.Config) *Service {
	return &Service{
		store:  store,
		stripe: billing.NewStripeClient(&cfg.Stripe),
		cfg:    cfg,
		logger: slog.Default().With("component", "website_service"),
	}
}

// GenerateReport initiates report generation
func (s *Service) GenerateReport(ctx context.Context, req *GenerateReportRequest) (*GenerateReportResponse, error) {
	q := s.store.Q()

	// Validate report type
	switch req.ReportType {
	case ReportTypeSnapshot, ReportTypeMIR, ReportTypeCIP, ReportTypeComprehensive:
		// Valid
	default:
		return nil, ErrInvalidReportType
	}

	// Generate criteria hash
	criteriaJSON, _ := json.Marshal(req.Criteria)
	hash := sha256.Sum256(criteriaJSON)
	criteriaHash := hex.EncodeToString(hash[:])

	// Create report record
	reportID := uuid.New().String()
	address := fmt.Sprintf("%s, %s, %s %s", req.Address, req.City, req.State, req.ZipCode)

	_, err := q.CreateInvestorReport(ctx, queries.CreateInvestorReportParams{
		ID:              reportID,
		UserId:          pgtype.Text{Valid: false},
		Email:           pgtype.Text{String: req.Email, Valid: req.Email != ""},
		PropertyId:      pgtype.Text{String: req.PropertyID, Valid: req.PropertyID != ""},
		PropertyAddress: pgtype.Text{String: address, Valid: true},
		Type:            req.ReportType,
		Status:          ReportStatusPending,
		SourceType:      pgtype.Text{String: "WEBSITE", Valid: true},
		SourcePackId:    pgtype.Text{String: req.ReportPackID, Valid: req.ReportPackID != ""},
		SourceAccessId:  pgtype.Text{String: req.InsightAccess, Valid: req.InsightAccess != ""},
		StripePaymentId: pgtype.Text{String: req.StripePayment, Valid: req.StripePayment != ""},
		InvestmentCriteria: criteriaJSON,
		CacheKey:        pgtype.Text{Valid: false},
		CriteriaHash:    pgtype.Text{String: criteriaHash, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create report: %w", err)
	}

	s.logger.Info("report generation initiated",
		"report_id", reportID,
		"type", req.ReportType,
		"email", req.Email,
	)

	return &GenerateReportResponse{
		Success:  true,
		ReportID: reportID,
		Status:   ReportStatusPending,
	}, nil
}

// CreateCheckoutSession creates a Stripe checkout session for website purchases
func (s *Service) CreateCheckoutSession(ctx context.Context, req *CheckoutRequest) (*CheckoutResponse, error) {
	// Map product type
	productType := billing.ProductSubscription
	switch req.ProductType {
	case "SINGLE_REPORT":
		productType = billing.ProductSingleReport
	case "REPORT_PACK":
		productType = billing.ProductReportPack
	case "INSIGHT_ACCESS":
		productType = billing.ProductInsightAccess
	}

	checkoutReq := &billing.CheckoutSessionRequest{
		PriceID:        req.PriceID,
		ProductType:    productType,
		SuccessURL:     req.SuccessURL,
		CancelURL:      req.CancelURL,
		CustomerEmail:  req.Email,
		Metadata:       req.Metadata,
		AllowPromoCode: req.AllowPromo,
	}

	resp, err := s.stripe.CreateCheckoutSession(checkoutReq, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create checkout session: %w", err)
	}

	return &CheckoutResponse{
		SessionID:      resp.SessionID,
		SessionURL:     resp.SessionURL,
		PublishableKey: resp.PublishableKey,
	}, nil
}

// CreateFreeSnapshot creates a free snapshot request
func (s *Service) CreateFreeSnapshot(ctx context.Context, req *FreeSnapshotRequest) (*FreeSnapshotResponse, error) {
	q := s.store.Q()

	// Check snapshot limit by email
	count, err := q.CountSnapshotsByEmail(ctx, pgtype.Text{String: req.Email, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("failed to check snapshot count: %w", err)
	}

	if count >= MaxFreeSnapshots {
		return &FreeSnapshotResponse{
			Success:   false,
			Message:   "You have reached the maximum number of free snapshots",
			Remaining: 0,
		}, nil
	}

	// Generate criteria hash
	criteriaJSON, _ := json.Marshal(req.Criteria)
	hash := sha256.Sum256(criteriaJSON)
	criteriaHash := hex.EncodeToString(hash[:])

	// Check for cached snapshot
	cached, err := q.GetSnapshotByCacheKey(ctx, pgtype.Text{String: criteriaHash, Valid: true})
	if err == nil && interfaceToString(cached.Status) == "COMPLETE" {
		// Return cached result
		return &FreeSnapshotResponse{
			Success:   true,
			RequestID: cached.ID,
			SessionID: cached.SessionId,
			Remaining: int(MaxFreeSnapshots - count),
		}, nil
	}

	// Create new snapshot request
	requestID := uuid.New().String()
	sessionID := uuid.New().String()
	location := fmt.Sprintf("%s, %s, %s %s", req.Address, req.City, req.State, req.ZipCode)

	_, err = q.CreateSnapshotRequest(ctx, queries.CreateSnapshotRequestParams{
		ID:             requestID,
		SessionId:      sessionID,
		UserId:         pgtype.Text{Valid: false},
		Email:          pgtype.Text{String: req.Email, Valid: true},
		Criteria:       criteriaJSON,
		CriteriaHash:   pgtype.Text{String: criteriaHash, Valid: true},
		Location:       pgtype.Text{String: location, Valid: true},
		Status:         ReportStatusPending,
		SnapshotNumber: int32(count + 1),
		IpAddress:      pgtype.Text{String: req.IPAddress, Valid: req.IPAddress != ""},
		UserAgent:      pgtype.Text{String: req.UserAgent, Valid: req.UserAgent != ""},
		Source:         pgtype.Text{String: req.Source, Valid: req.Source != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot request: %w", err)
	}

	s.logger.Info("free snapshot request created",
		"request_id", requestID,
		"email", req.Email,
		"snapshot_number", count+1,
	)

	return &FreeSnapshotResponse{
		Success:   true,
		RequestID: requestID,
		SessionID: sessionID,
		Remaining: int(MaxFreeSnapshots - count - 1),
	}, nil
}

// GetOrderStatus retrieves the status of an order (report or snapshot)
func (s *Service) GetOrderStatus(ctx context.Context, orderID string) (*OrderStatusResponse, error) {
	q := s.store.Q()

	// Try to find as investor report first
	report, err := q.GetInvestorReportByID(ctx, orderID)
	if err == nil {
		resp := &OrderStatusResponse{
			OrderID:     report.ID,
			Status:      interfaceToString(report.Status),
			ProductType: interfaceToString(report.Type),
			ReportID:    report.ID,
			CreatedAt:   report.CreatedAt.Time,
		}
		if report.CompletedAt.Valid {
			resp.CompletedAt = &report.CompletedAt.Time
		}
		if report.PdfUrl.Valid && report.PdfUrl.String != "" {
			resp.ReportURL = report.PdfUrl.String
		}
		return resp, nil
	}

	// Try to find as snapshot
	snapshot, err := q.GetSnapshotRequestByID(ctx, orderID)
	if err == nil {
		resp := &OrderStatusResponse{
			OrderID:     snapshot.ID,
			Status:      interfaceToString(snapshot.Status),
			ProductType: "SNAPSHOT",
			ReportID:    snapshot.ID,
			CreatedAt:   snapshot.CreatedAt.Time,
		}
		if snapshot.CompletedAt.Valid {
			resp.CompletedAt = &snapshot.CompletedAt.Time
		}
		return resp, nil
	}

	return nil, ErrOrderNotFound
}

// interfaceToString safely converts an interface{} to string
func interfaceToString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// GetPricingConfig returns the public pricing configuration
func (s *Service) GetPricingConfig() *PricingConfig {
	return &PricingConfig{
		Subscriptions: []PricingPlan{
			{
				ID:          "annual",
				Name:        "Annual Access",
				Description: "Structured decision outputs for every opportunity — aligned with your strategy and modeled under conservative assumptions.",
				Price:       "$1,200/yr",
				PriceID:     s.cfg.Stripe.PriceAnnualAccess,
				Interval:    "yearly",
				Features: []string{
					"Opportunity qualification",
					"Conservative underwriting",
					"Risk indicators",
					"Market context integration",
					"Portfolio-aware evaluation",
					"Structured decision memos",
					"PDF export",
				},
			},
			{
				ID:          "professional",
				Name:        "Professional Allocator",
				Description: "Deeper scenario analysis, comparison frameworks, and multi-property optimization.",
				Price:       "$2,400/yr",
				PriceID:     s.cfg.Stripe.PriceProfessionalAllocator,
				Interval:    "yearly",
				Popular:     true,
				Features: []string{
					"Everything in Annual Access, plus:",
					"Advanced scenario modeling",
					"Enhanced portfolio analytics",
					"Multi-property optimization",
					"Relative comparison across pipeline",
					"Custom scenario parameters",
					"Priority support",
				},
			},
		},
		OneTimePurchases: []PricingItem{},
		Features: []FeatureSet{
			{
				Tier: "annual",
				Features: []string{
					"Opportunity qualification",
					"Conservative underwriting",
					"Risk indicators",
					"Market context integration",
					"Portfolio-aware evaluation",
					"Structured decision memos",
					"PDF export",
				},
			},
			{
				Tier: "professional",
				Features: []string{
					"Everything in Annual Access, plus:",
					"Advanced scenario modeling",
					"Enhanced portfolio analytics",
					"Multi-property optimization",
					"Relative comparison across pipeline",
					"Custom scenario parameters",
					"Priority support",
				},
			},
		},
	}
}

// GetInsightAccessStatus checks the status of a user's insight access
func (s *Service) GetInsightAccessStatus(ctx context.Context, userID string) (*InsightAccessStatusResponse, error) {
	q := s.store.Q()

	access, err := q.GetInsightAccessByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &InsightAccessStatusResponse{
				Active:       false,
				ReportsUsed:  0,
				ReportsTotal: 0,
			}, nil
		}
		return nil, fmt.Errorf("failed to get insight access: %w", err)
	}

	tier := ""
	if access.Tier != nil {
		tier = access.Tier.(string)
	}

	resp := &InsightAccessStatusResponse{
		Active:       true,
		Tier:         tier,
		ReportsUsed:  int(access.ReportsUsed),
		ReportsTotal: int(access.ReportsPerPeriod),
	}

	if access.PeriodEndDate.Valid {
		resp.PeriodEnd = &access.PeriodEndDate.Time
	}

	return resp, nil
}

// CreateGuestSession creates a new guest session
func (s *Service) CreateGuestSession(ctx context.Context) (*GuestSessionResponse, error) {
	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour)
	token := generateToken(sessionID)

	return &GuestSessionResponse{
		SessionID: sessionID,
		ExpiresAt: expiresAt,
		Token:     token,
	}, nil
}

func generateToken(sessionID string) string {
	hash := sha256.Sum256([]byte(sessionID + time.Now().String()))
	return hex.EncodeToString(hash[:16])
}
