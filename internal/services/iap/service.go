package iap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
)

var (
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	ErrMissingReceiptData  = errors.New("missing receipt data")
	ErrUserNotFound        = errors.New("user not found")
)

// Service handles IAP operations for both iOS and Android
type Service struct {
	db     *postgres.DB
	apple  *AppleService
	google *GoogleService
	cfg    *config.Config
	logger *slog.Logger
}

// NewService creates a new IAP service
func NewService(ctx context.Context, db *postgres.DB, cfg *config.Config) (*Service, error) {
	apple := NewAppleService(cfg)

	google, err := NewGoogleService(ctx, cfg)
	if err != nil {
		slog.Warn("failed to initialize Google IAP service", "error", err)
		// Continue without Google - it's optional
	}

	return &Service{
		db:     db,
		apple:  apple,
		google: google,
		cfg:    cfg,
		logger: slog.Default().With("component", "iap_service"),
	}, nil
}

// ValidateIOSReceipt validates an iOS receipt
func (s *Service) ValidateIOSReceipt(ctx context.Context, receiptData string) (*AppleReceiptValidationResponse, error) {
	if receiptData == "" {
		return nil, ErrMissingReceiptData
	}

	return s.apple.ValidateReceipt(ctx, receiptData)
}

// ValidateAndroidReceipt validates an Android receipt
func (s *Service) ValidateAndroidReceipt(ctx context.Context, req *GoogleReceiptValidationRequest) (*GoogleReceiptValidationResponse, error) {
	if req.PurchaseToken == "" || req.ProductID == "" {
		return nil, ErrMissingReceiptData
	}

	if s.google == nil {
		return nil, ErrGoogleServiceUnavailable
	}

	return s.google.ValidateReceipt(ctx, req)
}

// SyncEntitlements syncs IAP subscription with user entitlements
func (s *Service) SyncEntitlements(ctx context.Context, userID string, req *SyncEntitlementsRequest) (*SyncEntitlementsResponse, error) {
	q := queries.New(s.db.Main)

	// Validate based on platform
	var tier SubscriptionTier
	var expiresAt *time.Time
	var transactionID string
	var originalTransactionID string
	var environment string

	switch req.Platform {
	case PlatformIOS:
		result, err := s.ValidateIOSReceipt(ctx, req.ReceiptData)
		if err != nil {
			return nil, fmt.Errorf("iOS receipt validation failed: %w", err)
		}
		if !result.Valid {
			return &SyncEntitlementsResponse{
				Success: false,
				Tier:    TierFree,
				Message: result.Error,
			}, nil
		}
		tier = result.Tier
		expiresAt = result.ExpiresDate
		transactionID = result.TransactionID
		originalTransactionID = result.OriginalTransactionID
		environment = result.Environment

	case PlatformAndroid:
		result, err := s.ValidateAndroidReceipt(ctx, &GoogleReceiptValidationRequest{
			ProductID:     req.ProductID,
			PurchaseToken: req.PurchaseToken,
		})
		if err != nil {
			return nil, fmt.Errorf("Android receipt validation failed: %w", err)
		}
		if !result.Valid {
			return &SyncEntitlementsResponse{
				Success: false,
				Tier:    TierFree,
				Message: result.Error,
			}, nil
		}
		tier = result.Tier
		expiresAt = result.ExpiresDate
		transactionID = result.OrderID
		originalTransactionID = result.OrderID

	default:
		return nil, ErrUnsupportedPlatform
	}

	// Build metadata for IAP tracking
	metadata := map[string]interface{}{
		"iap_platform":                string(req.Platform),
		"iap_product_id":              req.ProductID,
		"iap_transaction_id":          transactionID,
		"iap_original_transaction_id": originalTransactionID,
		"iap_environment":             environment,
		"iap_synced_at":               time.Now().Format(time.RFC3339),
	}
	metadataBytes, _ := json.Marshal(metadata)

	// Update user subscription in database
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		// Create new subscription
		_, err = q.CreateSubscription(ctx, queries.CreateSubscriptionParams{
			ID:     uuid.New().String(),
			UserId: userID,
			Tier:   string(tier),
			Status: "ACTIVE",
			CurrentPeriodStart: pgtype.Timestamp{
				Time:  time.Now(),
				Valid: true,
			},
			CurrentPeriodEnd: pgtype.Timestamp{
				Time:  expiresAtOrDefault(expiresAt),
				Valid: true,
			},
			CancelAtPeriodEnd: false,
			Metadata:          metadataBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create subscription: %w", err)
		}
	} else {
		// Update existing subscription tier and period
		err = q.UpdateSubscriptionTier(ctx, queries.UpdateSubscriptionTierParams{
			ID:   sub.ID,
			Tier: string(tier),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to update subscription tier: %w", err)
		}

		err = q.UpdateSubscriptionPeriod(ctx, queries.UpdateSubscriptionPeriodParams{
			ID: sub.ID,
			CurrentPeriodStart: pgtype.Timestamp{
				Time:  time.Now(),
				Valid: true,
			},
			CurrentPeriodEnd: pgtype.Timestamp{
				Time:  expiresAtOrDefault(expiresAt),
				Valid: true,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to update subscription period: %w", err)
		}

		err = q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
			ID:     sub.ID,
			Status: "ACTIVE",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to update subscription status: %w", err)
		}
	}

	// Record billing audit log
	eventData, _ := json.Marshal(map[string]interface{}{
		"platform":                req.Platform,
		"product_id":              req.ProductID,
		"transaction_id":          transactionID,
		"original_transaction_id": originalTransactionID,
		"tier":                    tier,
		"environment":             environment,
	})

	_, _ = q.CreateBillingAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:     uuid.New().String(),
		UserId: pgtype.Text{String: userID, Valid: true},
		AppleOriginalTransactionId: pgtype.Text{
			String: originalTransactionID,
			Valid:  req.Platform == PlatformIOS && originalTransactionID != "",
		},
		AppleTransactionId: pgtype.Text{
			String: transactionID,
			Valid:  req.Platform == PlatformIOS && transactionID != "",
		},
		AppleProductId: pgtype.Text{
			String: req.ProductID,
			Valid:  req.Platform == PlatformIOS,
		},
		AppleEnvironment: pgtype.Text{
			String: environment,
			Valid:  req.Platform == PlatformIOS && environment != "",
		},
		EventType: "IAP_SYNC",
		EventData: eventData,
	})

	s.logger.Info("IAP entitlements synced",
		"user_id", userID,
		"platform", req.Platform,
		"tier", tier,
		"product_id", req.ProductID,
	)

	return &SyncEntitlementsResponse{
		Success:   true,
		Tier:      tier,
		ExpiresAt: expiresAt,
		Features:  GetFeaturesForTier(tier),
	}, nil
}

// GetSubscriptionPlans returns available subscription plans
func (s *Service) GetSubscriptionPlans() *SubscriptionPlansResponse {
	return &SubscriptionPlansResponse{
		IOSPlans: []SubscriptionPlan{
			{
				ProductID:   string(ProductIOSInvestorMonthly),
				Name:        "Investor Monthly",
				Description: "Monthly access to investor features",
				Tier:        string(TierInvestor),
				Price:       "$19.99",
				Currency:    "USD",
				Period:      "monthly",
				Features:    GetFeaturesForTier(TierInvestor),
			},
			{
				ProductID:   string(ProductIOSInvestorAnnual),
				Name:        "Investor Annual",
				Description: "Annual access to investor features (save 20%)",
				Tier:        string(TierInvestor),
				Price:       "$191.99",
				Currency:    "USD",
				Period:      "annual",
				Features:    GetFeaturesForTier(TierInvestor),
			},
			{
				ProductID:   string(ProductIOSProfessionalMonthly),
				Name:        "Professional Monthly",
				Description: "Monthly access to professional features",
				Tier:        string(TierProfessional),
				Price:       "$49.99",
				Currency:    "USD",
				Period:      "monthly",
				Features:    GetFeaturesForTier(TierProfessional),
			},
			{
				ProductID:   string(ProductIOSProfessionalAnnual),
				Name:        "Professional Annual",
				Description: "Annual access to professional features (save 20%)",
				Tier:        string(TierProfessional),
				Price:       "$479.99",
				Currency:    "USD",
				Period:      "annual",
				Features:    GetFeaturesForTier(TierProfessional),
			},
		},
		AndroidPlans: []SubscriptionPlan{
			{
				ProductID:   string(ProductAndroidInvestorMonthly),
				Name:        "Investor Monthly",
				Description: "Monthly access to investor features",
				Tier:        string(TierInvestor),
				Price:       "$19.99",
				Currency:    "USD",
				Period:      "monthly",
				Features:    GetFeaturesForTier(TierInvestor),
			},
			{
				ProductID:   string(ProductAndroidInvestorAnnual),
				Name:        "Investor Annual",
				Description: "Annual access to investor features (save 20%)",
				Tier:        string(TierInvestor),
				Price:       "$191.99",
				Currency:    "USD",
				Period:      "annual",
				Features:    GetFeaturesForTier(TierInvestor),
			},
			{
				ProductID:   string(ProductAndroidProfessionalMonthly),
				Name:        "Professional Monthly",
				Description: "Monthly access to professional features",
				Tier:        string(TierProfessional),
				Price:       "$49.99",
				Currency:    "USD",
				Period:      "monthly",
				Features:    GetFeaturesForTier(TierProfessional),
			},
			{
				ProductID:   string(ProductAndroidProfessionalAnnual),
				Name:        "Professional Annual",
				Description: "Annual access to professional features (save 20%)",
				Tier:        string(TierProfessional),
				Price:       "$479.99",
				Currency:    "USD",
				Period:      "annual",
				Features:    GetFeaturesForTier(TierProfessional),
			},
		},
	}
}

func expiresAtOrDefault(expiresAt *time.Time) time.Time {
	if expiresAt != nil {
		return *expiresAt
	}
	// Default to 1 month from now
	return time.Now().AddDate(0, 1, 0)
}
