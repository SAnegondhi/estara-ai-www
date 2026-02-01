# Operating Expense Calculator

State-specific operating expense calculation for investment property analysis.

## Usage

```go
import "github.com/estara-ai/www/internal/services/investment/expenses"

calc := expenses.NewCalculator()

// Calculate expenses for a specific property
exp, err := calc.Calculate(expenses.PropertyInput{
    Price:         200000,
    State:         "TX",
    YearBuilt:     2010,
    EstimatedRent: 1500,
    PropertyType:  "single_family",
})

// Calculate with market-specific vacancy rate (from FRED)
vacancyRate := 6.8 // From aggregator.GetMarketData()
exp, err := calc.Calculate(expenses.PropertyInput{
    Price:               200000,
    State:               "TX",
    YearBuilt:           2010,
    EstimatedRent:       1500,
    PropertyType:        "single_family",
    VacancyRateOverride: &vacancyRate, // Use FRED data instead of default
})

// Get market defaults for a state
defaults := calc.GetMarketDefaults("TX")
```

## Data Sources

- **Property Tax Rates**: Tax Foundation 2024
  - https://taxfoundation.org/data/all/state/property-taxes-by-state-county-2024/
- **Insurance Rates**: Insurance Information Institute / NAIC 2024
  - https://www.iii.org/fact-statistic/facts-statistics-homeowners-and-renters-insurance
- **Vacancy Rate**: FRED (Federal Reserve Economic Data)
  - Series: `RRVRUSQ156N` (Rental Vacancy Rate, quarterly)
  - Fetched via market aggregator, falls back to 6.5% default

## Current Integration Points

The expense calculator is currently integrated in:

1. **AI Chat (`/api/ai/evaluate/chat`)** - `www/internal/api/handlers/ai/handler.go`
   - Calculates operating expenses for each property in chat request
   - Builds market context with expense characteristics
   - Passes expense data to AI prompt for more accurate analysis

2. **Market Defaults (`/api/v2/discover/market-defaults`)** - `www/internal/api/handlers/discover/handler.go`
   - Returns state-specific expense rate defaults in API response
   - Includes risk factors and special notes

## Recommended Future Integration Points

Consider using the expense calculator in:

1. **Property Enrichment** (`www/internal/services/property/enrichment/`)
   - Add calculated expenses to enriched property data
   - Include in property cards displayed to users

2. **Investment Projections** (`www/internal/services/investment/projection/`)
   - Use actual state-specific rates instead of fixed estimates
   - More accurate ROI and cash flow projections

3. **Investor Reports** (`www/internal/services/reports/`)
   - Include expense breakdown in PDF reports
   - Show state-specific rate comparisons

4. **Portfolio Analysis** (`www/internal/services/investment/`)
   - Calculate portfolio-wide operating expense totals
   - Compare expense ratios across properties in different states

5. **Batch Property Evaluation** (`/api/v2/discover/batch-evaluate`)
   - Include expense calculations in batch results
   - Help users compare properties across states

## Operating Expense Components

| Component | Default Rate | Basis | Notes |
|-----------|-------------|-------|-------|
| Property Tax | State-specific | % of home value | Ranges 0.28% (HI) to 2.47% (NJ) |
| Insurance | State-specific | % of home value | Ranges 0.18% (HI) to 2.40% (OK) |
| Maintenance | 1.0% | % of home value | Age-adjusted (0.5x-1.5x) |
| Vacancy | FRED or 6.5% | % of rent | Uses FRED `RRVRUSQ156N` when available |
| Property Mgmt | 8.0% | % of rent | Typical range 8-10% |

## Maintenance Age Factors

| Property Age | Factor | Effective Rate |
|--------------|--------|----------------|
| 0-5 years (new) | 0.5x | 0.5% |
| 5-15 years (modern) | 0.75x | 0.75% |
| 15-30 years (mature) | 1.0x | 1.0% |
| 30-50 years (older) | 1.25x | 1.25% |
| 50+ years (historic) | 1.5x | 1.5% |
