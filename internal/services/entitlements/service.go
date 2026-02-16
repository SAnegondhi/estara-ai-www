// Package entitlements provides user entitlements based on subscription tier
// ADR-035: Strategic Repositioning - Report-First Pricing Model
package entitlements

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
)

// Service handles entitlement calculations and checks
type Service struct {
	store  *db.Store
	cfg    *config.Config
	logger *slog.Logger
}

// NewService creates a new entitlements service
func NewService(store *db.Store, cfg *config.Config) *Service {
	return &Service{
		store:  store,
		cfg:    cfg,
		logger: slog.Default().With("component", "entitlements_service"),
	}
}

// SubscriptionTier represents the subscription tier
type SubscriptionTier string

const (
	TierFree                  SubscriptionTier = "free"
	TierReportUser            SubscriptionTier = "report_user"
	TierAnnualAccess          SubscriptionTier = "annual_access"
	TierProfessionalAllocator SubscriptionTier = "professional_allocator"
	TierAAPIInvestor          SubscriptionTier = "aapi_investor"
	TierAAPIAllocator         SubscriptionTier = "aapi_allocator"
	TierEnterprise            SubscriptionTier = "enterprise"
)

// SubscriptionStatus represents the subscription status
type SubscriptionStatus string

const (
	StatusActive   SubscriptionStatus = "active"
	StatusTrialing SubscriptionStatus = "trialing"
	StatusExpired  SubscriptionStatus = "expired"
	StatusPastDue  SubscriptionStatus = "past_due"
	StatusCanceled SubscriptionStatus = "canceled"
)

// UsageLimits represents usage limits for a feature
type UsageLimits struct {
	Used      int `json:"used"`
	Max       int `json:"max"`       // -1 = unlimited, 0 = not available
	Remaining int `json:"remaining"` // -1 = unlimited
}

// MarketRestrictions represents market location restrictions
type MarketRestrictions struct {
	Type            string           `json:"type"` // "unlimited" or "single_location"
	PrimaryLocation *PrimaryLocation `json:"primaryLocation,omitempty"`
	RadiusMiles     int              `json:"radiusMiles,omitempty"`
}

// PrimaryLocation represents a primary location
type PrimaryLocation struct {
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Name string  `json:"name"`
}

// Features represents feature flags
type Features struct {
	BasicPropertySearches bool `json:"basicPropertySearches"`
	InvestmentPlanPicks   bool `json:"investmentPlanPicks"`
	CommunityAnalyses     bool `json:"communityAnalyses"`
	PortfolioAnalysis     bool `json:"portfolioAnalysis"`
	TaxFinancingAnalysis  bool `json:"taxFinancingAnalysis"`
	AreaComparison        bool `json:"areaComparison"`
	WhitelabelReports     bool `json:"whitelabelReports"`
	PrioritySupport       bool `json:"prioritySupport"`
	APIAccess             bool `json:"apiAccess"`
}

// AIUsage represents AI usage information (legacy)
type AIUsage struct {
	CurrentUsage      float64 `json:"currentUsage"`
	MonthlyLimit      float64 `json:"monthlyLimit"`
	Remaining         float64 `json:"remaining"`
	UsagePercentage   float64 `json:"usagePercentage"`
	LimitExceeded     bool    `json:"limitExceeded"`
	RequestsToday     int     `json:"requestsToday"`
	RequestsThisMonth int     `json:"requestsThisMonth"`
}

// Limits represents all usage limits
type Limits struct {
	InvestmentPlanPicks UsageLimits `json:"investmentPlanPicks"`
	AreaComparisons     UsageLimits `json:"areaComparisons"`
}

// Entitlements represents user entitlements
type Entitlements struct {
	// Subscription info
	SubscriptionTier   SubscriptionTier   `json:"subscriptionTier"`
	SubscriptionStatus SubscriptionStatus `json:"subscriptionStatus"`
	HasActiveSubscription bool            `json:"hasActiveSubscription"`

	// Trial info (FREE tier only)
	TrialExpiresAt     *string `json:"trialExpiresAt,omitempty"`
	TrialDaysRemaining *int    `json:"trialDaysRemaining,omitempty"`

	// Usage limits
	Limits Limits `json:"limits"`

	// Market restrictions
	MarketRestrictions MarketRestrictions `json:"marketRestrictions"`

	// Features
	Features Features `json:"features"`

	// Legacy fields for backward compatibility
	AnalysisSessionsRemaining   int     `json:"analysisSessionsRemaining,omitempty"`
	MaxAnalysisSessionsPerMonth int     `json:"maxAnalysisSessionsPerMonth,omitempty"`
	PropertySearchesRemaining   int     `json:"propertySearchesRemaining,omitempty"`
	MaxPropertySearchesPerMonth int     `json:"maxPropertySearchesPerMonth,omitempty"`
	HasDataTier                 string  `json:"hasDataTier,omitempty"`
	AIUsage                     *AIUsage `json:"aiUsage,omitempty"`
}

// UserData represents user data from the database
type UserData struct {
	ID       string
	Email    string
	Tier     *string
	Status   *string
	TrialEnd *time.Time
}

// UsageData represents usage data from the database
type UsageData struct {
	InvestmentPicksUsed int
	AreaComparisonsUsed int
}

// GetEntitlements returns entitlements for a user
func (s *Service) GetEntitlements(ctx context.Context, userID string) (*Entitlements, error) {
	// Get user with subscription info
	userData, err := s.getUserData(ctx, userID)
	if err != nil {
		s.logger.Warn("user not found, returning free trial entitlements", "user_id", userID)
		return s.buildFreeTrialEntitlements(), nil
	}

	// Get usage data
	usageData, err := s.getUsageData(ctx, userID)
	if err != nil {
		s.logger.Warn("failed to get usage data", "error", err, "user_id", userID)
		usageData = &UsageData{}
	}

	return s.buildEntitlements(userData, usageData), nil
}

// getUserData retrieves user data from database
func (s *Service) getUserData(ctx context.Context, userID string) (*UserData, error) {
	query := `
		SELECT u.id, u.email, s.tier, s.status, s."trialEnd"
		FROM users u
		LEFT JOIN subscriptions s ON u.id = s."userId"
		WHERE u.id = $1
	`

	var user UserData
	err := s.store.Pool().QueryRow(ctx, query, userID).Scan(
		&user.ID,
		&user.Email,
		&user.Tier,
		&user.Status,
		&user.TrialEnd,
	)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// getUsageData retrieves usage data for current month
func (s *Service) getUsageData(ctx context.Context, userID string) (*UsageData, error) {
	now := time.Now().UTC()
	month := int(now.Month())
	year := now.Year()

	var usage UsageData

	// Get investment plan usage
	investmentQuery := `
		SELECT COALESCE("picksUsed", 0)
		FROM investment_plan_usage
		WHERE "userId" = $1 AND month = $2 AND year = $3
	`
	err := s.store.Pool().QueryRow(ctx, investmentQuery, userID, month, year).Scan(&usage.InvestmentPicksUsed)
	if err != nil {
		// No usage record is fine
		usage.InvestmentPicksUsed = 0
	}

	// Get area comparison usage
	comparisonQuery := `
		SELECT COALESCE("comparisonsUsed", 0)
		FROM area_comparison_usage
		WHERE "userId" = $1 AND month = $2 AND year = $3
	`
	err = s.store.Pool().QueryRow(ctx, comparisonQuery, userID, month, year).Scan(&usage.AreaComparisonsUsed)
	if err != nil {
		// No usage record is fine
		usage.AreaComparisonsUsed = 0
	}

	return &usage, nil
}

// buildEntitlements builds entitlements from user and usage data
func (s *Service) buildEntitlements(userData *UserData, usageData *UsageData) *Entitlements {
	tier := normalizeTier(userData.Tier)
	status := mapSubscriptionStatus(userData.Status, userData.TrialEnd)
	isBlocked := status == StatusExpired && tier == TierFree

	// Get tier features and limits
	features := getTierFeatures(tier)
	investmentPicksMax := getInvestmentPicksLimit(tier)
	areaComparisonMax := getAreaComparisonLimit(tier)

	// Build limits
	limits := Limits{
		InvestmentPlanPicks: UsageLimits{
			Used:      usageData.InvestmentPicksUsed,
			Max:       investmentPicksMax,
			Remaining: calculateRemaining(usageData.InvestmentPicksUsed, investmentPicksMax),
		},
		AreaComparisons: UsageLimits{
			Used:      usageData.AreaComparisonsUsed,
			Max:       areaComparisonMax,
			Remaining: calculateRemaining(usageData.AreaComparisonsUsed, areaComparisonMax),
		},
	}

	// Build market restrictions (unlimited in ADR-035 model)
	marketRestrictions := MarketRestrictions{
		Type: "unlimited",
	}

	// Calculate trial info for FREE tier
	var trialExpiresAt *string
	var trialDaysRemaining *int
	if tier == TierFree && userData.TrialEnd != nil {
		expiresStr := userData.TrialEnd.Format(time.RFC3339)
		trialExpiresAt = &expiresStr

		remaining := int(userData.TrialEnd.Sub(time.Now()).Hours() / 24)
		if remaining < 0 {
			remaining = 0
		}
		trialDaysRemaining = &remaining
	}

	// If blocked, disable all features
	if isBlocked {
		features = Features{}
	}

	return &Entitlements{
		SubscriptionTier:      tier,
		SubscriptionStatus:    status,
		HasActiveSubscription: isPaidTier(tier) && status == StatusActive,

		// Trial info
		TrialExpiresAt:     trialExpiresAt,
		TrialDaysRemaining: trialDaysRemaining,

		// Limits
		Limits: limits,

		// Market restrictions
		MarketRestrictions: marketRestrictions,

		// Features
		Features: features,

		// Legacy fields
		AnalysisSessionsRemaining:   calculateRemaining(usageData.InvestmentPicksUsed, investmentPicksMax),
		MaxAnalysisSessionsPerMonth: investmentPicksMax,
		PropertySearchesRemaining:   -1, // Unlimited basic searches
		MaxPropertySearchesPerMonth: -1,
		HasDataTier:                 getHasDataTier(tier),
		AIUsage: &AIUsage{
			CurrentUsage:      0,
			MonthlyLimit:      100,
			Remaining:         100,
			UsagePercentage:   0,
			LimitExceeded:     false,
			RequestsToday:     0,
			RequestsThisMonth: 0,
		},
	}
}

// buildFreeTrialEntitlements builds default free trial entitlements
func (s *Service) buildFreeTrialEntitlements() *Entitlements {
	tier := TierFree
	features := getTierFeatures(tier)
	investmentPicksMax := getInvestmentPicksLimit(tier)
	areaComparisonMax := getAreaComparisonLimit(tier)

	trialDays := 15

	return &Entitlements{
		SubscriptionTier:      tier,
		SubscriptionStatus:    StatusTrialing,
		HasActiveSubscription: false,
		TrialDaysRemaining:    &trialDays,

		Limits: Limits{
			InvestmentPlanPicks: UsageLimits{
				Used:      0,
				Max:       investmentPicksMax,
				Remaining: investmentPicksMax,
			},
			AreaComparisons: UsageLimits{
				Used:      0,
				Max:       areaComparisonMax,
				Remaining: areaComparisonMax,
			},
		},

		MarketRestrictions: MarketRestrictions{
			Type:        "single_location",
			RadiusMiles: 50,
		},

		Features: features,

		// Legacy fields
		AnalysisSessionsRemaining:   investmentPicksMax,
		MaxAnalysisSessionsPerMonth: investmentPicksMax,
		PropertySearchesRemaining:   -1,
		MaxPropertySearchesPerMonth: -1,
		HasDataTier:                 "limited",
		AIUsage: &AIUsage{
			CurrentUsage:      0,
			MonthlyLimit:      5.00,
			Remaining:         5.00,
			UsagePercentage:   0,
			LimitExceeded:     false,
			RequestsToday:     0,
			RequestsThisMonth: 0,
		},
	}
}

// Helper functions

func normalizeTier(tier *string) SubscriptionTier {
	if tier == nil || *tier == "" {
		return TierFree
	}

	upper := strings.ToUpper(*tier)
	switch upper {
	case "FREE":
		return TierFree
	case "REPORT_USER":
		return TierReportUser
	case "ANNUAL_ACCESS":
		return TierAnnualAccess
	case "PROFESSIONAL_ALLOCATOR":
		return TierProfessionalAllocator
	// Backwards compatibility for old tier names
	case "INVESTOR":
		return TierAnnualAccess
	case "PROFESSIONAL":
		return TierProfessionalAllocator
	case "AAPI_INVESTOR":
		return TierAAPIInvestor
	case "AAPI_ALLOCATOR":
		return TierAAPIAllocator
	case "ENTERPRISE":
		return TierEnterprise
	default:
		return TierFree
	}
}

func mapSubscriptionStatus(status *string, trialEnd *time.Time) SubscriptionStatus {
	if status == nil || *status == "" {
		return StatusTrialing
	}

	upper := strings.ToUpper(*status)

	// Check if trial has expired
	if upper == "TRIALING" || upper == "FREE" {
		if trialEnd != nil && trialEnd.Before(time.Now()) {
			return StatusExpired
		}
		return StatusTrialing
	}

	switch upper {
	case "ACTIVE":
		return StatusActive
	case "PAST_DUE":
		return StatusPastDue
	case "CANCELED":
		return StatusCanceled
	case "EXPIRED":
		return StatusExpired
	default:
		return StatusTrialing
	}
}

func calculateRemaining(used, max int) int {
	if max == -1 {
		return -1 // Unlimited
	}
	if max == 0 {
		return 0 // Not available
	}
	remaining := max - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func isPaidTier(tier SubscriptionTier) bool {
	return tier != TierFree
}

func getHasDataTier(tier SubscriptionTier) string {
	if isPaidTier(tier) {
		return "unlimited"
	}
	return "limited"
}

func getTierFeatures(tier SubscriptionTier) Features {
	switch tier {
	case TierFree:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   false,
			CommunityAnalyses:     false,
			PortfolioAnalysis:     false,
			TaxFinancingAnalysis:  false,
			AreaComparison:        false,
			WhitelabelReports:     false,
			PrioritySupport:       false,
			APIAccess:             false,
		}
	case TierReportUser:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   true,
			CommunityAnalyses:     true,
			PortfolioAnalysis:     false,
			TaxFinancingAnalysis:  true,
			AreaComparison:        true,
			WhitelabelReports:     false,
			PrioritySupport:       false,
			APIAccess:             false,
		}
	case TierAnnualAccess:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   true,
			CommunityAnalyses:     true,
			PortfolioAnalysis:     true,
			TaxFinancingAnalysis:  true,
			AreaComparison:        true,
			WhitelabelReports:     false,
			PrioritySupport:       false,
			APIAccess:             false,
		}
	case TierProfessionalAllocator, TierEnterprise:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   true,
			CommunityAnalyses:     true,
			PortfolioAnalysis:     true,
			TaxFinancingAnalysis:  true,
			AreaComparison:        true,
			WhitelabelReports:     true,
			PrioritySupport:       true,
			APIAccess:             false,
		}
	case TierAAPIInvestor:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   true,
			CommunityAnalyses:     true,
			PortfolioAnalysis:     true,
			TaxFinancingAnalysis:  true,
			AreaComparison:        true,
			WhitelabelReports:     false,
			PrioritySupport:       true,
			APIAccess:             false,
		}
	case TierAAPIAllocator:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   true,
			CommunityAnalyses:     true,
			PortfolioAnalysis:     true,
			TaxFinancingAnalysis:  true,
			AreaComparison:        true,
			WhitelabelReports:     true,
			PrioritySupport:       true,
			APIAccess:             false,
		}
	default:
		return Features{
			BasicPropertySearches: true,
			InvestmentPlanPicks:   false,
			CommunityAnalyses:     false,
			PortfolioAnalysis:     false,
			TaxFinancingAnalysis:  false,
			AreaComparison:        false,
			WhitelabelReports:     false,
			PrioritySupport:       false,
			APIAccess:             false,
		}
	}
}

func getInvestmentPicksLimit(tier SubscriptionTier) int {
	switch tier {
	case TierFree:
		return 0 // No free picks - must purchase reports
	case TierReportUser:
		return 0 // One-time purchase, no monthly limit
	case TierAnnualAccess:
		return 24 // 24 reports/year
	case TierProfessionalAllocator:
		return 60 // 60 reports/year
	case TierAAPIInvestor:
		return 36 // 36 reports/year
	case TierAAPIAllocator:
		return 100 // 100 reports/year
	case TierEnterprise:
		return -1 // Unlimited
	default:
		return 0
	}
}

func getAreaComparisonLimit(tier SubscriptionTier) int {
	switch tier {
	case TierFree:
		return 0 // Not available
	case TierReportUser:
		return 0 // Not available
	default:
		return -1 // Unlimited for paid tiers
	}
}
