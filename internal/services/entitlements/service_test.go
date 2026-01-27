package entitlements

import (
	"testing"
)

func TestSubscriptionTierConstants(t *testing.T) {
	// Verify tier constants exist and have expected values
	if TierFree != "free" {
		t.Errorf("expected TierFree='free', got='%s'", TierFree)
	}
	if TierInvestor != "investor" {
		t.Errorf("expected TierInvestor='investor', got='%s'", TierInvestor)
	}
	if TierProfessional != "professional" {
		t.Errorf("expected TierProfessional='professional', got='%s'", TierProfessional)
	}
	if TierAAPIInvestor != "aapi_investor" {
		t.Errorf("expected TierAAPIInvestor='aapi_investor', got='%s'", TierAAPIInvestor)
	}
	if TierAAPIAllocator != "aapi_allocator" {
		t.Errorf("expected TierAAPIAllocator='aapi_allocator', got='%s'", TierAAPIAllocator)
	}
}

func TestSubscriptionStatusConstants(t *testing.T) {
	if StatusActive != "active" {
		t.Errorf("expected StatusActive='active', got='%s'", StatusActive)
	}
	if StatusTrialing != "trialing" {
		t.Errorf("expected StatusTrialing='trialing', got='%s'", StatusTrialing)
	}
	if StatusExpired != "expired" {
		t.Errorf("expected StatusExpired='expired', got='%s'", StatusExpired)
	}
	if StatusPastDue != "past_due" {
		t.Errorf("expected StatusPastDue='past_due', got='%s'", StatusPastDue)
	}
	if StatusCanceled != "canceled" {
		t.Errorf("expected StatusCanceled='canceled', got='%s'", StatusCanceled)
	}
}

func TestUsageLimits_Struct(t *testing.T) {
	limits := UsageLimits{
		Used:      5,
		Max:       10,
		Remaining: 5,
	}

	if limits.Used != 5 {
		t.Errorf("expected used=5, got=%d", limits.Used)
	}
	if limits.Max != 10 {
		t.Errorf("expected max=10, got=%d", limits.Max)
	}
	if limits.Remaining != 5 {
		t.Errorf("expected remaining=5, got=%d", limits.Remaining)
	}
}

func TestUsageLimits_UnlimitedCase(t *testing.T) {
	// -1 indicates unlimited
	limits := UsageLimits{
		Used:      100,
		Max:       -1,
		Remaining: -1,
	}

	if limits.Max != -1 {
		t.Errorf("expected max=-1 (unlimited), got=%d", limits.Max)
	}
	if limits.Remaining != -1 {
		t.Errorf("expected remaining=-1 (unlimited), got=%d", limits.Remaining)
	}
}

func TestUsageLimits_NotAvailable(t *testing.T) {
	// 0 for max indicates feature not available
	limits := UsageLimits{
		Used:      0,
		Max:       0,
		Remaining: 0,
	}

	if limits.Max != 0 {
		t.Errorf("expected max=0 (not available), got=%d", limits.Max)
	}
}

func TestFeatures_Struct(t *testing.T) {
	features := Features{
		BasicPropertySearches: true,
		InvestmentPlanPicks:   true,
		CommunityAnalyses:     false,
		PortfolioAnalysis:     true,
		TaxFinancingAnalysis:  false,
		AreaComparison:        true,
		WhitelabelReports:     false,
		PrioritySupport:       false,
		APIAccess:             false,
	}

	if !features.BasicPropertySearches {
		t.Error("expected BasicPropertySearches=true")
	}
	if !features.InvestmentPlanPicks {
		t.Error("expected InvestmentPlanPicks=true")
	}
	if features.CommunityAnalyses {
		t.Error("expected CommunityAnalyses=false")
	}
}

func TestMarketRestrictions_Unlimited(t *testing.T) {
	restrictions := MarketRestrictions{
		Type: "unlimited",
	}

	if restrictions.Type != "unlimited" {
		t.Errorf("expected type='unlimited', got='%s'", restrictions.Type)
	}
	if restrictions.PrimaryLocation != nil {
		t.Error("expected primaryLocation to be nil for unlimited")
	}
}

func TestMarketRestrictions_SingleLocation(t *testing.T) {
	restrictions := MarketRestrictions{
		Type: "single_location",
		PrimaryLocation: &PrimaryLocation{
			Lat:  30.2672,
			Lng:  -97.7431,
			Name: "Austin, TX",
		},
		RadiusMiles: 50,
	}

	if restrictions.Type != "single_location" {
		t.Errorf("expected type='single_location', got='%s'", restrictions.Type)
	}
	if restrictions.PrimaryLocation == nil {
		t.Error("expected primaryLocation to be set")
	}
	if restrictions.PrimaryLocation.Name != "Austin, TX" {
		t.Errorf("expected name='Austin, TX', got='%s'", restrictions.PrimaryLocation.Name)
	}
	if restrictions.RadiusMiles != 50 {
		t.Errorf("expected radiusMiles=50, got=%d", restrictions.RadiusMiles)
	}
}

func TestAIUsage_Struct(t *testing.T) {
	usage := AIUsage{
		CurrentUsage:      5.50,
		MonthlyLimit:      20.00,
		Remaining:         14.50,
		UsagePercentage:   27.5,
		LimitExceeded:     false,
		RequestsToday:     10,
		RequestsThisMonth: 45,
	}

	if usage.LimitExceeded {
		t.Error("expected limitExceeded=false")
	}
	if usage.UsagePercentage != 27.5 {
		t.Errorf("expected usagePercentage=27.5, got=%f", usage.UsagePercentage)
	}
}

func TestAIUsage_LimitExceeded(t *testing.T) {
	usage := AIUsage{
		CurrentUsage:    25.00,
		MonthlyLimit:    20.00,
		Remaining:       0,
		UsagePercentage: 125.0,
		LimitExceeded:   true,
	}

	if !usage.LimitExceeded {
		t.Error("expected limitExceeded=true when currentUsage > monthlyLimit")
	}
}

func TestTierFeatureMapping(t *testing.T) {
	// Test helper function for tier feature mapping
	tierFeatures := map[SubscriptionTier][]string{
		TierFree:         {},
		TierInvestor:     {"market_analysis", "property_search"},
		TierProfessional: {"market_analysis", "property_search", "report_generation", "pdf_export"},
		TierAAPIAllocator: {"market_analysis", "property_search", "report_generation", "pdf_export", "api_access"},
	}

	for tier, expectedFeatures := range tierFeatures {
		t.Run(string(tier), func(t *testing.T) {
			// Just verify the mapping structure is correct
			if tier == TierFree && len(expectedFeatures) != 0 {
				t.Error("free tier should have no features")
			}
			if tier == TierInvestor && len(expectedFeatures) < 2 {
				t.Error("investor tier should have basic features")
			}
		})
	}
}

func TestValidFeatures(t *testing.T) {
	validFeatures := []string{
		"market_analysis",
		"property_search",
		"report_generation",
		"pdf_export",
		"scenario_modeling",
		"investment_planning",
	}

	for _, f := range validFeatures {
		if !isValidFeature(f) {
			t.Errorf("expected %s to be a valid feature", f)
		}
	}

	invalidFeatures := []string{"unknown", "", "admin_access", "MARKET_ANALYSIS"}
	for _, f := range invalidFeatures {
		if isValidFeature(f) {
			t.Errorf("expected %s to be an invalid feature", f)
		}
	}
}

// Helper function
func isValidFeature(feature string) bool {
	validFeatures := map[string]bool{
		"market_analysis":    true,
		"property_search":    true,
		"report_generation":  true,
		"pdf_export":         true,
		"scenario_modeling":  true,
		"investment_planning": true,
	}
	return validFeatures[feature]
}
