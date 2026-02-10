// Package expenses - capex.go provides capital expenditure reserve calculations.
//
// CapEx reserves cover FULL REPLACEMENT of major building systems at end-of-life
// (roof, HVAC, water heater, appliances, flooring, paint, plumbing, electrical).
// This is separate from routine maintenance (fixing leaks, patching drywall, HVAC filters)
// which is covered by the Maintenance line item in OperatingExpenses.
//
// Industry references:
//   - REI Glossary: https://glossary.reiprime.com/terms/capital-expenditure-budget
//   - Stessa: https://www.stessa.com/blog/capital-expenditures-rental-property/
//   - Baselane: https://www.baselane.com/resources/best-capital-reserve-account-rental-real-estate
package expenses

// CapExComponent represents a major building system that requires periodic replacement.
type CapExComponent struct {
	Name          string  // Component name (e.g., "Roof")
	LifespanYears int     // Expected useful life in years
	CostPctValue  float64 // Replacement cost as fraction of property value (e.g., 0.040 = 4%)
}

// StandardCapExComponents defines the major building systems and their replacement costs.
// Annualized base rate = Sum of (CostPct / Lifespan) ~ 0.83% of property value/year.
var StandardCapExComponents = []CapExComponent{
	{"Roof", 25, 0.040},           // ~$10K on $250K home
	{"HVAC", 15, 0.025},           // ~$6.25K
	{"Water Heater", 10, 0.005},   // ~$1.25K
	{"Appliances", 12, 0.015},     // ~$3.75K
	{"Flooring", 15, 0.020},       // ~$5K
	{"Exterior Paint", 12, 0.012}, // ~$3K
	{"Plumbing", 35, 0.020},       // ~$5K
	{"Electrical", 40, 0.015},     // ~$3.75K
}

// baseAnnualCapExRate is the sum of (CostPct / Lifespan) for all components.
// Computed once; approximately 0.83%.
var baseAnnualCapExRate float64

func init() {
	for _, c := range StandardCapExComponents {
		baseAnnualCapExRate += c.CostPctValue / float64(c.LifespanYears) * 100
	}
}

// capExAgeFactor returns the age multiplier for CapEx reserves.
// Mirrors MaintenanceAgeFactors from calculator.go.
func capExAgeFactor(propertyAge int) float64 {
	switch {
	case propertyAge <= 5:
		return 0.5 // New construction: lower CapEx needs
	case propertyAge <= 15:
		return 0.75 // Modern: most systems still under warranty
	case propertyAge <= 30:
		return 1.0 // Mature: baseline rate
	case propertyAge <= 50:
		return 1.25 // Older: approaching end-of-life for many systems
	default:
		return 1.5 // Historic: multiple systems past expected life
	}
}

// CapExReserveRate returns the age-adjusted annual CapEx reserve rate
// as a percentage of property value (e.g., 0.83 for 0.83%).
func CapExReserveRate(propertyAge int) float64 {
	return baseAnnualCapExRate * capExAgeFactor(propertyAge)
}

// CapExComponentRisk holds risk assessment for a single building component.
type CapExComponentRisk struct {
	Name             string `json:"name"`
	LifespanYears    int    `json:"lifespanYears"`
	EstRemainingLife int    `json:"estRemainingLife"` // max(0, lifespan - propertyAge)
	AnnualReserve    int    `json:"annualReserve"`    // $ amount per year
	RiskLevel        string `json:"riskLevel"`        // "low", "medium", "high"
}

// ComponentRiskAssessment returns per-component detail for a property.
//
// Limitation: Assumes components were installed when the property was built.
// In reality, previous owners may have replaced them.
func ComponentRiskAssessment(propertyAge int, propertyValue int) []CapExComponentRisk {
	results := make([]CapExComponentRisk, len(StandardCapExComponents))
	for i, c := range StandardCapExComponents {
		remaining := max(0, c.LifespanYears-propertyAge)

		// Annual reserve for this component
		annualReserve := int(float64(propertyValue) * c.CostPctValue / float64(c.LifespanYears))

		// Risk level based on remaining life
		var riskLevel string
		switch {
		case remaining > 10:
			riskLevel = "low"
		case remaining >= 3:
			riskLevel = "medium"
		default:
			riskLevel = "high"
		}

		results[i] = CapExComponentRisk{
			Name:             c.Name,
			LifespanYears:    c.LifespanYears,
			EstRemainingLife: remaining,
			AnnualReserve:    annualReserve,
			RiskLevel:        riskLevel,
		}
	}
	return results
}
