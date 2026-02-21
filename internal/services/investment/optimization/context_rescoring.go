package optimization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/estara-ai/www/internal/services/investment"
)

// ContextRescorer handles Phase 2b context-aware re-scoring
// ADR-088 Phase 4: Injects portfolio context into AI prompts for selected properties
type ContextRescorer struct {
	logger *slog.Logger
	// TODO Phase 4: Add AI service for thesis generation
}

// NewContextRescorer creates a new context-aware re-scoring service
func NewContextRescorer(logger *slog.Logger) *ContextRescorer {
	return &ContextRescorer{
		logger: logger,
	}
}

// PortfolioContext holds contextual information about a portfolio configuration
// Used to inject context into AI re-scoring prompts
type PortfolioContext struct {
	// Portfolio composition
	PropertyCount int                  `json:"propertyCount"`
	TotalValue    int                  `json:"totalValue"`
	Markets       []MarketAllocation   `json:"markets"`       // Market breakdown
	PropertyTypes []PropertyTypeAlloc  `json:"propertyTypes"` // Type breakdown

	// Portfolio metrics
	AvgCapRate         float64 `json:"avgCapRate"`
	AvgExpectedReturn  float64 `json:"avgExpectedReturn"`
	PortfolioVolatility float64 `json:"portfolioVolatility"`

	// Concentration metrics
	MarketHHI      float64 `json:"marketHHI"`
	SpatialConc    float64 `json:"spatialConc"`
	TypeHHI        float64 `json:"typeHHI"`
	ConcentrationIndex float64 `json:"concentrationIndex"`

	// Hash for caching
	ContextHash string `json:"contextHash"`
}

// MarketAllocation represents allocation to a specific market
type MarketAllocation struct {
	Market     string  `json:"market"`     // "Austin, TX"
	Properties int     `json:"properties"` // Number of properties
	Value      int     `json:"value"`      // Total value in market
	Percentage float64 `json:"percentage"` // % of portfolio value
}

// PropertyTypeAlloc represents allocation to a property type
type PropertyTypeAlloc struct {
	Type       string  `json:"type"`       // "Single Family"
	Properties int     `json:"properties"` // Number of properties
	Value      int     `json:"value"`      // Total value of this type
	Percentage float64 `json:"percentage"` // % of portfolio value
}

// RescoreConfiguration performs context-aware re-scoring for a frontier configuration
// ADR-088 Phase 4: Phase 2b re-scoring with portfolio context injection
func (cr *ContextRescorer) RescoreConfiguration(
	ctx context.Context,
	config *investment.FrontierPoint,
	profile investment.InvestorProfile,
) (*investment.FrontierPoint, error) {
	cr.logger.Info("rescoring configuration with portfolio context",
		"configIndex", config.ConfigIndex,
		"propertyCount", len(config.Properties),
	)

	// Step 1: Build portfolio context
	portfolioContext := cr.buildPortfolioContext(config)

	// Step 2: Re-score each property with context (parallel execution)
	rescored, err := cr.rescorePropertiesParallel(ctx, config.Properties, portfolioContext, profile)
	if err != nil {
		return nil, fmt.Errorf("failed to re-score properties: %w", err)
	}

	// Step 3: Create updated configuration with context-aware theses
	updated := *config // Copy
	updated.Properties = rescored

	cr.logger.Info("configuration re-scoring complete",
		"configIndex", config.ConfigIndex,
		"contextHash", portfolioContext.ContextHash,
	)

	return &updated, nil
}

// buildPortfolioContext constructs contextual information for a portfolio
func (cr *ContextRescorer) buildPortfolioContext(config *investment.FrontierPoint) *PortfolioContext {
	totalValue := 0
	marketMap := make(map[string]*MarketAllocation)
	typeMap := make(map[string]*PropertyTypeAlloc)

	// Aggregate by market and type
	for _, propInPort := range config.Properties {
		prop := propInPort.Property
		totalValue += prop.Price

		// Market aggregation
		market := prop.City + ", " + prop.State
		if marketMap[market] == nil {
			marketMap[market] = &MarketAllocation{
				Market:     market,
				Properties: 0,
				Value:      0,
			}
		}
		marketMap[market].Properties++
		marketMap[market].Value += prop.Price

		// Type aggregation
		propType := prop.PropertyType
		if propType == "" {
			propType = "Single Family" // Default
		}
		if typeMap[propType] == nil {
			typeMap[propType] = &PropertyTypeAlloc{
				Type:       propType,
				Properties: 0,
				Value:      0,
			}
		}
		typeMap[propType].Properties++
		typeMap[propType].Value += prop.Price
	}

	// Calculate percentages
	markets := make([]MarketAllocation, 0, len(marketMap))
	for _, m := range marketMap {
		m.Percentage = float64(m.Value) / float64(totalValue) * 100
		markets = append(markets, *m)
	}
	sort.Slice(markets, func(i, j int) bool {
		return markets[i].Value > markets[j].Value // Sort by value descending
	})

	propertyTypes := make([]PropertyTypeAlloc, 0, len(typeMap))
	for _, t := range typeMap {
		t.Percentage = float64(t.Value) / float64(totalValue) * 100
		propertyTypes = append(propertyTypes, *t)
	}
	sort.Slice(propertyTypes, func(i, j int) bool {
		return propertyTypes[i].Value > propertyTypes[j].Value
	})

	// Calculate average cap rate
	avgCapRate := 0.0
	for _, propInPort := range config.Properties {
		avgCapRate += propInPort.CapRate
	}
	avgCapRate /= float64(len(config.Properties))

	// Generate context hash
	contextHash := cr.generateContextHash(config.Properties)

	return &PortfolioContext{
		PropertyCount:       len(config.Properties),
		TotalValue:          totalValue,
		Markets:             markets,
		PropertyTypes:       propertyTypes,
		AvgCapRate:          avgCapRate,
		AvgExpectedReturn:   config.ExpectedReturn,
		PortfolioVolatility: config.PortfolioVolatility,
		MarketHHI:           0, // TODO: Extract from config
		SpatialConc:         0, // TODO: Extract from config
		TypeHHI:             0, // TODO: Extract from config
		ConcentrationIndex:  config.ConcentrationIndex,
		ContextHash:         contextHash,
	}
}

// generateContextHash creates SHA-256 hash of sorted property IDs
// Used as part of Phase 2b cache key
func (cr *ContextRescorer) generateContextHash(properties []investment.PropertyInPortfolio) string {
	// Extract property IDs
	ids := make([]string, len(properties))
	for i, prop := range properties {
		ids[i] = prop.Property.ID
	}

	// Sort for consistent hashing
	sort.Strings(ids)

	// Create hash
	joined := strings.Join(ids, ",")
	hash := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(hash[:])
}

// rescorePropertiesParallel re-scores all properties in parallel with portfolio context
func (cr *ContextRescorer) rescorePropertiesParallel(
	ctx context.Context,
	properties []investment.PropertyInPortfolio,
	portfolioContext *PortfolioContext,
	profile investment.InvestorProfile,
) ([]investment.PropertyInPortfolio, error) {
	// Phase 4: Parallel execution for 1-3s total latency
	rescored := make([]investment.PropertyInPortfolio, len(properties))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := range properties {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Re-score this property with portfolio context
			thesis, err := cr.rescorePropertyWithContext(
				ctx,
				properties[idx].Property,
				portfolioContext,
				profile,
			)

			mu.Lock()
			defer mu.Unlock()

			if err != nil && firstErr == nil {
				firstErr = err
				return
			}

			// Update property with context-aware thesis
			rescored[idx] = properties[idx] // Copy
			// TODO Phase 4: Add thesis field to PropertyInPortfolio
			// rescored[idx].Thesis = thesis

			cr.logger.Debug("property re-scored with context",
				"propertyId", properties[idx].Property.ID,
				"thesis", thesis != nil,
			)
		}(i)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return rescored, nil
}

// rescorePropertyWithContext generates context-aware thesis for a single property
func (cr *ContextRescorer) rescorePropertyWithContext(
	ctx context.Context,
	property investment.Property,
	portfolioContext *PortfolioContext,
	profile investment.InvestorProfile,
) (*investment.PropertyThesis, error) {
	// Build context-aware prompt
	prompt := cr.buildContextAwarePrompt(property, portfolioContext, profile)

	// TODO Phase 4: Call AI service to generate thesis
	// For now, return placeholder thesis
	_ = prompt // Silence unused warning

	// Placeholder thesis (Phase 4: will be replaced with actual AI generation)
	thesis := &investment.PropertyThesis{
		InvestmentThesis: fmt.Sprintf("Context-aware analysis for %s, %s within %d-property portfolio.",
			property.City, property.State, portfolioContext.PropertyCount),
		KeyStrengths: []string{
			fmt.Sprintf("Part of diversified portfolio across %d markets", len(portfolioContext.Markets)),
			"Contributes to portfolio risk reduction through geographic diversification",
		},
		KeyRisks: []string{
			"Portfolio context may amplify correlated market risks",
			"Concentration in specific property type requires monitoring",
		},
		MarketContext: fmt.Sprintf("One of %d properties in %s market (%.1f%% of portfolio)",
			cr.countPropertiesInMarket(property, portfolioContext),
			property.City,
			cr.getMarketAllocation(property, portfolioContext),
		),
		CapexAlert: nil,
	}

	return thesis, nil
}

// buildContextAwarePrompt creates AI prompt with portfolio context injection
func (cr *ContextRescorer) buildContextAwarePrompt(
	property investment.Property,
	portfolioContext *PortfolioContext,
	profile investment.InvestorProfile,
) string {
	var prompt strings.Builder

	prompt.WriteString(fmt.Sprintf(`Analyze this property within the context of a %d-property portfolio.

INVESTOR PROFILE:
- Strategy: %s
- Risk Tolerance: %s
- Investment Horizon: %s

PORTFOLIO CONTEXT:
- Total Properties: %d
- Total Portfolio Value: $%d
- Average Cap Rate: %.2f%%
- Portfolio Volatility: %.2f%%
- Expected Return: %.2f%%

MARKET DIVERSIFICATION:
`, portfolioContext.PropertyCount, profile.Strategy, profile.RiskTolerance, profile.InvestmentHorizon,
		portfolioContext.PropertyCount, portfolioContext.TotalValue,
		portfolioContext.AvgCapRate, portfolioContext.PortfolioVolatility, portfolioContext.AvgExpectedReturn))

	for i, market := range portfolioContext.Markets {
		if i < 5 { // Show top 5 markets
			prompt.WriteString(fmt.Sprintf("- %s: %d properties (%.1f%% of portfolio)\n",
				market.Market, market.Properties, market.Percentage))
		}
	}

	prompt.WriteString("\nPROPERTY TYPE ALLOCATION:\n")
	for _, ptype := range portfolioContext.PropertyTypes {
		prompt.WriteString(fmt.Sprintf("- %s: %d properties (%.1f%% of portfolio)\n",
			ptype.Type, ptype.Properties, ptype.Percentage))
	}

	prompt.WriteString(fmt.Sprintf(`
PROPERTY TO ANALYZE:
- Address: %s, %s, %s
- Price: $%d
- Beds: %d, Baths: %.1f, SqFt: %d
- Estimated Rent: $%d/mo
- Days on Market: %d

TASK:
Generate a context-aware investment thesis that explicitly considers:
1. How this property fits within the existing portfolio
2. Diversification benefits or risks it adds
3. Whether it complements or duplicates existing holdings
4. Its contribution to portfolio risk/return profile

OUTPUT FORMAT (JSON):
{
  "investmentThesis": "2-3 sentences highlighting portfolio context",
  "keyStrengths": [
    "Strength 1 (mention diversification contribution)",
    "Strength 2 (mention portfolio fit)"
  ],
  "keyRisks": [
    "Risk 1 (mention portfolio concentration concerns)",
    "Risk 2 (mention correlation risks)"
  ],
  "marketContext": "1 sentence on market conditions + portfolio allocation",
  "capexAlert": null
}

GUIDELINES:
- Explicitly mention portfolio allocation (e.g., "one of 3 properties in Austin")
- Highlight diversification contributions (e.g., "adds exposure to new market")
- Flag concentration risks (e.g., "increases Austin allocation to 40%% of portfolio")
- Consider correlation with existing holdings
`,
		property.Address, property.City, property.State, property.Price,
		property.Beds, property.Baths, property.Sqft, property.EstimatedRent, property.DaysOnMarket))

	return prompt.String()
}

// countPropertiesInMarket counts how many properties are in the same market
func (cr *ContextRescorer) countPropertiesInMarket(
	property investment.Property,
	portfolioContext *PortfolioContext,
) int {
	market := property.City + ", " + property.State
	for _, m := range portfolioContext.Markets {
		if m.Market == market {
			return m.Properties
		}
	}
	return 1 // Default to 1 (this property)
}

// getMarketAllocation returns the percentage allocation to this property's market
func (cr *ContextRescorer) getMarketAllocation(
	property investment.Property,
	portfolioContext *PortfolioContext,
) float64 {
	market := property.City + ", " + property.State
	for _, m := range portfolioContext.Markets {
		if m.Market == market {
			return m.Percentage
		}
	}
	return 0.0
}

// RescoreAllConfigurations re-scores all frontier configurations in parallel
// ADR-088 Phase 4: Parallel execution for minimal latency
func (cr *ContextRescorer) RescoreAllConfigurations(
	ctx context.Context,
	configs []investment.FrontierPoint,
	profile investment.InvestorProfile,
) ([]investment.FrontierPoint, error) {
	cr.logger.Info("rescoring all frontier configurations",
		"configCount", len(configs),
	)

	rescored := make([]investment.FrontierPoint, len(configs))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := range configs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			config, err := cr.RescoreConfiguration(ctx, &configs[idx], profile)

			mu.Lock()
			defer mu.Unlock()

			if err != nil && firstErr == nil {
				firstErr = err
				return
			}

			if config != nil {
				rescored[idx] = *config
			}
		}(i)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	cr.logger.Info("all configurations re-scored",
		"configCount", len(rescored),
	)

	return rescored, nil
}
