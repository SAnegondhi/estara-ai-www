package memo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/geospatial"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/fred"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

// Service orchestrates Decision Memo generation.
type Service struct {
	aiClient    *anthropic.Client
	aggregator  *aggregator.Aggregator
	metroReader *timeseries.MetroReader
	fredService *fred.Service
	geoSpatial  geospatial.Service
	db          *queries.Queries
	calculator  *Calculator
	logger      *slog.Logger
}

// ServiceConfig holds dependencies for creating a memo service.
type ServiceConfig struct {
	AIClient    *anthropic.Client
	Aggregator  *aggregator.Aggregator
	MetroReader *timeseries.MetroReader
	FREDService *fred.Service
	GeoSpatial  geospatial.Service
	DB          *queries.Queries
}

// NewService creates a new memo generation service.
func NewService(cfg ServiceConfig) *Service {
	return &Service{
		aiClient:    cfg.AIClient,
		aggregator:  cfg.Aggregator,
		metroReader: cfg.MetroReader,
		fredService: cfg.FREDService,
		geoSpatial:  cfg.GeoSpatial,
		db:          cfg.DB,
		calculator:  NewCalculator(),
		logger:      slog.Default().With("component", "memo_service"),
	}
}

// ProgressFunc is called to report generation progress.
type ProgressFunc func(pct float64, message string)

// GenerateMemos generates Decision Memos for a batch of properties.
func (s *Service) GenerateMemos(ctx context.Context, properties []BatchPropertyInput, opts GenerateOptions, onProgress ProgressFunc) ([]MemoData, error) {
	if len(properties) == 0 {
		return nil, fmt.Errorf("no properties provided")
	}

	report := func(pct float64, msg string) {
		if onProgress != nil {
			onProgress(pct, msg)
		}
	}

	// Determine market from first property (all props in same batch share market)
	city := properties[0].City
	state := properties[0].State

	// Step 1: Fetch market data (10-25%)
	report(10, "Fetching market data")
	marketData, err := s.fetchMarketData(ctx, city, state)
	if err != nil {
		s.logger.Warn("failed to fetch market data", "error", err)
	}

	// Step 2: Fetch FRED rates (20%)
	report(20, "Fetching economic rates")
	var mortgageRate, vacancyRate float64
	if s.fredService != nil {
		rates, err := s.fredService.GetAllRates(ctx)
		if err == nil {
			mortgageRate = rates.MortgageRate30Year
			vacancyRate = rates.RentalVacancyRate
		}
	}

	// Step 2b: Check portfolio snapshot (27%)
	report(27, "Checking portfolio impact")
	hasInvestmentPlan := false
	var portfolioImpacts []*PortfolioImpactData
	if s.db != nil && opts.UserID != "" {
		snapshot, err := s.db.GetLatestPortfolioSnapshot(ctx, opts.UserID)
		if err == nil {
			hasInvestmentPlan = true
			portfolioImpacts = s.calculator.ComputePortfolioImpacts(snapshot, properties)
		}
	}

	// Step 3: Fetch time series for charts (30%)
	report(25, "Loading market trends")
	marketCtx := s.buildMarketContext(ctx, city, state, marketData, properties[0])

	// Step 4: Compute financials per property (30-50%)
	report(30, "Computing financials")
	calculations := make([]*CalculationOutput, len(properties))
	for i, prop := range properties {
		input := CalculationInput{
			Property:      prop,
			MarketData:    marketData,
			MortgageRate:  mortgageRate,
			MarketVacancy: vacancyRate,
			PriceCAGR:     marketCtx.PriceCAGR,
			RentCAGR:      marketCtx.RentCAGR,
		}
		calculations[i] = s.calculator.Compute(input)
	}

	// Step 5: Query geo-spatial for comps (50-55%)
	report(50, "Checking comparables")
	comps := make([]*ComparablesData, len(properties))
	if s.geoSpatial != nil && s.geoSpatial.IsConfigured() {
		for i, prop := range properties {
			comps[i] = s.fetchComparables(ctx, prop)
		}
	}

	// Step 6: Call AI for narratives (55-85%)
	report(55, "Generating analysis")
	aiOutput, err := s.generateNarratives(ctx, properties, calculations, marketData, opts.Strategy, city, state, hasInvestmentPlan)
	if err != nil {
		s.logger.Error("AI narrative generation failed", "error", err)
		// Continue with empty narratives rather than failing entirely
		aiOutput = &AIBatchOutput{Properties: make([]AIPropertyOutput, len(properties))}
		for i := range aiOutput.Properties {
			aiOutput.Properties[i] = AIPropertyOutput{
				Index:            i,
				Status:           "Qualified",
				RiskProfile:      "Moderate",
				StrategyFit:      50,
				Confidence:       40,
				CapitalRole:      "Core",
				ExecutiveSummary: "Analysis unavailable — AI generation failed.",
				KeyUncertainty:   "Unable to generate risk assessment.",
			}
		}
	}

	// Step 7: Assemble memos (85-100%)
	report(85, "Assembling memos")
	memos := make([]MemoData, len(properties))
	for i, prop := range properties {
		// Override subject-specific fields on the shared market context
		propMarketCtx := marketCtx
		propMarketCtx.SubjectPrice = prop.Price
		propMarketCtx.SubjectRent = prop.EstimatedRent

		memo := MemoData{
			PropertyAddress:  prop.Address,
			PropertySubtitle: buildSubtitle(prop),
			GeneratedAt:      time.Now(),
			Strategy:         opts.Strategy,
			MarketContext:    propMarketCtx,
			Property:         prop,
		}

		// Merge computed financials
		if calc := calculations[i]; calc != nil {
			memo.KeyFinancials = calc.KeyFinancials
			memo.Assumptions = calc.Assumptions
			memo.CashFlowProjections = calc.CashFlowProjections
			memo.Scenarios = calc.Scenarios
		}

		// Merge comps
		memo.Comparables = comps[i]

		// Merge portfolio impact
		if i < len(portfolioImpacts) && portfolioImpacts[i] != nil {
			memo.PortfolioImpact = portfolioImpacts[i]
		}

		// Merge AI output
		if i < len(aiOutput.Properties) {
			ai := aiOutput.Properties[i]
			memo.Status = ai.Status
			memo.RiskProfile = ai.RiskProfile
			memo.StrategyFit = ai.StrategyFit
			memo.Confidence = ai.Confidence
			memo.CapitalRole = ai.CapitalRole
			memo.RelativeRank = fmt.Sprintf("#%d of %d", i+1, len(properties))
			memo.ExecutiveSummary = ai.ExecutiveSummary
			memo.RiskFactors = ai.RiskFactors
			memo.KeyUncertainty = ai.KeyUncertainty
			memo.DecisionContext = ai.DecisionContext
		}

		memos[i] = memo
	}

	report(100, "Complete")
	return memos, nil
}

// fetchMarketData retrieves market data for the city/state.
func (s *Service) fetchMarketData(ctx context.Context, city, state string) (*aggregator.MarketData, error) {
	if s.aggregator == nil {
		return nil, fmt.Errorf("aggregator not configured")
	}
	return s.aggregator.GetMarketData(ctx, city, state)
}

// buildMarketContext constructs market trend data for charts.
// prop is used to set the subject property reference lines on the charts.
func (s *Service) buildMarketContext(ctx context.Context, city, state string, marketData *aggregator.MarketData, prop BatchPropertyInput) MarketContextData {
	mctx := MarketContextData{
		City:         city,
		State:        state,
		SubjectPrice: prop.Price,
		SubjectRent:  prop.EstimatedRent,
	}

	if marketData != nil {
		mctx.MedianPrice = marketData.MedianHomePrice
		mctx.MedianRent = marketData.MedianRent
	}

	// Fetch time series if available
	if s.metroReader != nil {
		end := time.Now()
		start := end.AddDate(-5, 0, 0)
		data, err := s.metroReader.GetCityTimeSeries(ctx, city, state, start, end)
		if err == nil && len(data) > 0 {
			for _, d := range data {
				dateStr := d.Date.Format("2006-01")
				if d.ZHVI > 0 {
					mctx.PriceTrend = append(mctx.PriceTrend, TimePoint{Date: dateStr, Value: d.ZHVI})
				}
				if d.ZORI > 0 {
					mctx.RentTrend = append(mctx.RentTrend, TimePoint{Date: dateStr, Value: d.ZORI})
				}
			}

			// Compute 5yr CAGRs
			if len(mctx.PriceTrend) >= 2 {
				first := mctx.PriceTrend[0].Value
				last := mctx.PriceTrend[len(mctx.PriceTrend)-1].Value
				if first > 0 {
					years := float64(len(mctx.PriceTrend)) / 12
					if years > 0 {
						mctx.PriceCAGR = (pow(last/first, 1/years) - 1) * 100
					}
				}
			}
			if len(mctx.RentTrend) >= 2 {
				first := mctx.RentTrend[0].Value
				last := mctx.RentTrend[len(mctx.RentTrend)-1].Value
				if first > 0 {
					years := float64(len(mctx.RentTrend)) / 12
					if years > 0 {
						mctx.RentCAGR = (pow(last/first, 1/years) - 1) * 100
					}
				}
			}
		}
	}

	return mctx
}

// fetchComparables retrieves comparable sales/rentals for a property.
func (s *Service) fetchComparables(ctx context.Context, prop BatchPropertyInput) *ComparablesData {
	result := &ComparablesData{}

	sales, err := s.geoSpatial.GetComparableSales(ctx, geospatial.ComparableSalesRequest{
		Latitude:     prop.Latitude,
		Longitude:    prop.Longitude,
		ZipCode:      prop.ZipCode,
		RadiusMiles:  0.5,
		PropertyType: prop.PropertyType,
		Beds:         prop.Beds,
		MonthsBack:   6,
		Limit:        5,
	})
	if err == nil {
		result.Sales = sales
	}

	rentals, err := s.geoSpatial.GetComparableRentals(ctx, geospatial.ComparableRentalsRequest{
		Latitude:     prop.Latitude,
		Longitude:    prop.Longitude,
		ZipCode:      prop.ZipCode,
		RadiusMiles:  0.5,
		PropertyType: prop.PropertyType,
		Beds:         prop.Beds,
		Limit:        5,
	})
	if err == nil {
		result.Rentals = rentals
	}

	if result.Sales == nil && result.Rentals == nil {
		return nil
	}
	return result
}

// generateNarratives calls the AI to generate narrative sections for all properties.
func (s *Service) generateNarratives(
	ctx context.Context,
	properties []BatchPropertyInput,
	calculations []*CalculationOutput,
	marketData *aggregator.MarketData,
	strategy, city, state string,
	hasInvestmentPlan bool,
) (*AIBatchOutput, error) {
	if s.aiClient == nil {
		return nil, fmt.Errorf("AI client not configured")
	}

	// Build property contexts for the prompt
	propContexts := make([]prompts.MemoPropertyContext, len(properties))
	for i, prop := range properties {
		pc := prompts.MemoPropertyContext{
			Index:        i,
			Address:      prop.Address,
			City:         prop.City,
			State:        prop.State,
			Price:        prop.Price,
			Rent:         prop.EstimatedRent,
			Beds:         prop.Beds,
			Baths:        prop.Baths,
			Sqft:         prop.Sqft,
			YearBuilt:    prop.YearBuilt,
			PropertyType: prop.PropertyType,
			HasComps:     false,
		}

		// Add financials from calculation
		if calc := calculations[i]; calc != nil {
			pc.Financials = map[string]float64{
				"capRate":          calc.KeyFinancials.CapRate,
				"cashOnCashYr1":    calc.KeyFinancials.CashOnCashYr1,
				"dscr":            calc.KeyFinancials.DSCR,
				"noi":             calc.KeyFinancials.NOI,
				"irr5Yr":          calc.KeyFinancials.IRR5Yr,
				"equityMultiple10": calc.KeyFinancials.EquityMultiple10,
				"expenseRatio":     calc.KeyFinancials.ExpenseRatio,
				"monthlyPayment":   calc.KeyFinancials.MonthlyPayment,
			}

			// Add scenario data
			pc.Scenarios = make([]map[string]any, len(calc.Scenarios))
			for j, sc := range calc.Scenarios {
				pc.Scenarios[j] = map[string]any{
					"name":           sc.Name,
					"irr":            sc.IRR,
					"cashOnCash":     sc.CashOnCash,
					"equityMultiple": sc.EquityMultiple,
					"dscr":           sc.DSCR,
					"riskLevel":      sc.RiskLevel,
				}
			}
		}

		// Add market averages
		if marketData != nil {
			pc.MarketAvg = map[string]any{
				"medianPrice": marketData.MedianHomePrice,
				"medianRent":  marketData.MedianRent,
				"capRate":     marketData.CapRate,
				"yoyChange":   marketData.YearOverYearPct,
			}
		}

		propContexts[i] = pc
	}

	userPrompt := prompts.BuildMemoUserPrompt(propContexts, strategy, city, state, hasInvestmentPlan)
	response, err := s.aiClient.CompleteWithMaxTokens(ctx, prompts.MemoSystemPrompt, userPrompt, 8192)
	if err != nil {
		return nil, fmt.Errorf("AI call failed: %w", err)
	}

	// Parse JSON response
	output := &AIBatchOutput{}

	// Extract JSON from response (may be wrapped in markdown code block)
	jsonStr := extractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), output); err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w (raw: %.200s)", err, response)
	}

	return output, nil
}

// extractJSON finds JSON object in a response that may contain markdown formatting.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Remove markdown code block if present
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	// Find first { and last }
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

// buildSubtitle creates the property subtitle string.
func buildSubtitle(prop BatchPropertyInput) string {
	parts := []string{}
	if prop.PropertyType != "" {
		parts = append(parts, strings.ToUpper(prop.PropertyType))
	} else {
		parts = append(parts, "SFR")
	}
	parts = append(parts, fmt.Sprintf("$%s", formatNumber(prop.Price)))
	if prop.YearBuilt > 0 {
		parts = append(parts, fmt.Sprintf("Built %d", prop.YearBuilt))
	}
	if prop.Beds > 0 || prop.Baths > 0 {
		parts = append(parts, fmt.Sprintf("%d/%g", prop.Beds, prop.Baths))
	}
	if prop.Sqft > 0 {
		parts = append(parts, fmt.Sprintf("%s sqft", formatNumber(prop.Sqft)))
	}
	return strings.Join(parts, " · ")
}

// formatNumber adds comma separators to an integer.
func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result.WriteByte(',')
		}
		result.WriteRune(c)
	}
	return result.String()
}

// pow delegates to math.Pow.
func pow(base, exp float64) float64 {
	return math.Pow(base, exp)
}
