// Package expenses provides operating expense calculation for investment properties.
// Expense calculations are based on state-specific property tax and insurance rates,
// plus standard estimates for maintenance, vacancy, and property management.
package expenses

import (
	"fmt"
	"math"
	"time"
)

// Calculator computes operating expenses for investment properties
type Calculator struct{}

// NewCalculator creates a new expense calculator
func NewCalculator() *Calculator {
	return &Calculator{}
}

// PropertyInput contains the property data needed for expense calculation
type PropertyInput struct {
	Price         int     // Purchase price
	State         string  // State abbreviation (e.g., "TX")
	City          string  // City name (for management fee calculation)
	YearBuilt     int     // Year built (for maintenance adjustment)
	EstimatedRent int     // Monthly rent estimate
	PropertyType  string  // "single_family", "condo", "townhouse", etc.

	// Optional market-specific overrides (from FRED or local data)
	VacancyRateOverride *float64 // Market-specific vacancy rate (%)
}

// OperatingExpenses contains calculated operating expenses
type OperatingExpenses struct {
	// Annual expenses
	PropertyTax       float64 `json:"propertyTax"`       // Annual property tax
	Insurance         float64 `json:"insurance"`         // Annual insurance
	Maintenance       float64 `json:"maintenance"`       // Annual maintenance
	VacancyAllowance  float64 `json:"vacancyAllowance"`  // Annual vacancy reserve
	PropertyMgmt      float64 `json:"propertyMgmt"`      // Annual property management
	TotalAnnual       float64 `json:"totalAnnual"`       // Total annual expenses
	TotalMonthly      float64 `json:"totalMonthly"`      // Total monthly expenses

	// Rates used (for transparency)
	PropertyTaxRate   float64 `json:"propertyTaxRate"`   // % of home value
	InsuranceRate     float64 `json:"insuranceRate"`     // % of home value
	MaintenanceRate   float64 `json:"maintenanceRate"`   // % of home value
	VacancyRate       float64 `json:"vacancyRate"`       // % of rent
	PropertyMgmtRate  float64 `json:"propertyMgmtRate"`  // % of rent

	// Derived metrics
	ExpenseRatio      float64 `json:"expenseRatio"`      // Total expenses / Gross rent
	NOI               float64 `json:"noi"`               // Net Operating Income (annual)
	CapRate           float64 `json:"capRate"`           // NOI / Price * 100
}

// MarketExpenseDefaults contains typical expense rates for a market
type MarketExpenseDefaults struct {
	State             string   `json:"state"`
	PropertyTaxRate   float64  `json:"propertyTaxRate"`
	InsuranceRate     float64  `json:"insuranceRate"`
	InsuranceRisk     []string `json:"insuranceRisk,omitempty"`
	MaintenanceRate   float64  `json:"maintenanceRate"`
	VacancyRate       float64  `json:"vacancyRate"`
	PropertyMgmtRate  float64  `json:"propertyMgmtRate"`
	Notes             string   `json:"notes,omitempty"`
}

// Default rates (when state-specific data is unavailable)
const (
	DefaultPropertyTaxRate  = 1.1  // 1.1% of home value (national avg ~1.07%)
	DefaultInsuranceRate    = 0.60 // 0.6% of home value (national avg)
	DefaultMaintenanceRate  = 1.0  // 1.0% of home value (industry standard)
	DefaultVacancyRate      = 6.5  // 6.5% of rent (national avg ~7%)
	DefaultPropertyMgmtRate = 8.0  // 8.0% of rent (typical range 8-10%)
)

// MarketClass represents the property management market tier
// Source: Industry standards from NARPM, Buildium, AppFolio research
// https://www.narpm.org/
type MarketClass string

const (
	MarketClassA MarketClass = "A" // Major metros, high competition
	MarketClassB MarketClass = "B" // Secondary/tertiary metros
	MarketClassC MarketClass = "C" // Smaller cities, rural areas
)

// ManagementFeeByClass returns property management fee % based on market class
// Source: Property management industry surveys (Buildium, NARPM 2024)
// Class A (major metros): 6-8% - high competition, economies of scale
// Class B (secondary markets): 8-10% - moderate competition
// Class C (smaller/rural): 10-12% - fewer options, more effort per property
var managementFeeByClass = map[MarketClass]float64{
	MarketClassA: 7.0,  // Average for major metros
	MarketClassB: 9.0,  // Average for secondary markets
	MarketClassC: 11.0, // Average for smaller/rural markets
}

// majorMetros lists cities classified as Class A markets
// Based on population > 1M metro area and high rental market activity
var majorMetros = map[string]bool{
	// Top 30 metros
	"New York": true, "Los Angeles": true, "Chicago": true, "Houston": true,
	"Phoenix": true, "Philadelphia": true, "San Antonio": true, "San Diego": true,
	"Dallas": true, "San Jose": true, "Austin": true, "Jacksonville": true,
	"Fort Worth": true, "Columbus": true, "Charlotte": true, "San Francisco": true,
	"Indianapolis": true, "Seattle": true, "Denver": true, "Washington": true,
	"Boston": true, "El Paso": true, "Nashville": true, "Detroit": true,
	"Oklahoma City": true, "Portland": true, "Las Vegas": true, "Memphis": true,
	"Louisville": true, "Baltimore": true, "Milwaukee": true, "Albuquerque": true,
	"Tucson": true, "Fresno": true, "Sacramento": true, "Kansas City": true,
	"Mesa": true, "Atlanta": true, "Omaha": true, "Colorado Springs": true,
	"Raleigh": true, "Long Beach": true, "Virginia Beach": true, "Miami": true,
	"Oakland": true, "Minneapolis": true, "Tulsa": true, "Tampa": true,
	"Arlington": true, "New Orleans": true,
}

// secondaryMarkets lists cities classified as Class B markets
// Based on population 250K-1M metro area
var secondaryMarkets = map[string]bool{
	"Peoria": true, "Springfield": true, "Rockford": true, "Naperville": true,
	"Evansville": true, "Fort Wayne": true, "South Bend": true,
	"Cedar Rapids": true, "Des Moines": true, "Davenport": true,
	"Wichita": true, "Topeka": true, "Lexington": true, "Bowling Green": true,
	"Baton Rouge": true, "Shreveport": true, "Bangor": true,
	"Worcester": true, "Ann Arbor": true, "Lansing": true,
	"Grand Rapids": true, "Rochester": true, "Duluth": true, "St. Cloud": true,
	"Jackson": true, "Biloxi": true, "St. Louis": true, "Columbia": true,
	"Billings": true, "Missoula": true, "Lincoln": true, "Reno": true,
	"Manchester": true, "Concord": true, "Trenton": true, "Newark": true,
	"Santa Fe": true, "Buffalo": true, "Syracuse": true, "Fargo": true,
	"Akron": true, "Toledo": true, "Cincinnati": true, "Cleveland": true,
	"Eugene": true, "Salem": true, "Pittsburgh": true,
	"Providence": true, "Charleston": true, "Sioux Falls": true,
	"Chattanooga": true, "Knoxville": true, "Amarillo": true, "Lubbock": true,
	"Provo": true, "Burlington": true, "Richmond": true, "Norfolk": true,
	"Spokane": true, "Tacoma": true, "Huntington": true, "Madison": true,
	"Green Bay": true, "Cheyenne": true, "Casper": true,
}

// MaintenanceAgeFactors adjusts maintenance by property age
var MaintenanceAgeFactors = map[string]float64{
	"new":      0.5,  // 0-5 years: 50% of base
	"modern":   0.75, // 5-15 years: 75% of base
	"mature":   1.0,  // 15-30 years: 100% of base (baseline)
	"older":    1.25, // 30-50 years: 125% of base
	"historic": 1.5,  // 50+ years: 150% of base
}

// Calculate computes operating expenses for a property
func (c *Calculator) Calculate(input PropertyInput) (*OperatingExpenses, error) {
	if input.Price <= 0 {
		return nil, fmt.Errorf("price must be positive")
	}
	if input.State == "" {
		return nil, fmt.Errorf("state is required")
	}

	// Get state-specific rates
	taxRate := c.getPropertyTaxRate(input.State)
	insuranceRate := c.getInsuranceRate(input.State)
	maintenanceRate := c.getMaintenanceRate(input.YearBuilt)
	propertyMgmtRate := c.getPropertyMgmtRate(input.City)

	// Use market-specific vacancy rate if provided, otherwise use default
	vacancyRate := DefaultVacancyRate
	if input.VacancyRateOverride != nil && *input.VacancyRateOverride > 0 {
		vacancyRate = *input.VacancyRateOverride
	}

	// Calculate annual expenses
	homeValue := float64(input.Price)
	annualRent := float64(input.EstimatedRent) * 12

	propertyTax := homeValue * (taxRate / 100)
	insurance := homeValue * (insuranceRate / 100)
	maintenance := homeValue * (maintenanceRate / 100)

	// Vacancy and management are % of rent
	vacancyAllowance := 0.0
	propertyMgmt := 0.0
	if annualRent > 0 {
		vacancyAllowance = annualRent * (vacancyRate / 100)
		propertyMgmt = annualRent * (propertyMgmtRate / 100)
	}

	totalAnnual := propertyTax + insurance + maintenance + vacancyAllowance + propertyMgmt
	totalMonthly := totalAnnual / 12

	// Calculate expense ratio and NOI
	expenseRatio := 0.0
	noi := 0.0
	capRate := 0.0
	if annualRent > 0 {
		expenseRatio = (totalAnnual / annualRent) * 100
		noi = annualRent - totalAnnual
		if homeValue > 0 {
			capRate = (noi / homeValue) * 100
		}
	}

	return &OperatingExpenses{
		PropertyTax:      math.Round(propertyTax),
		Insurance:        math.Round(insurance),
		Maintenance:      math.Round(maintenance),
		VacancyAllowance: math.Round(vacancyAllowance),
		PropertyMgmt:     math.Round(propertyMgmt),
		TotalAnnual:      math.Round(totalAnnual),
		TotalMonthly:     math.Round(totalMonthly),

		PropertyTaxRate:  taxRate,
		InsuranceRate:    insuranceRate,
		MaintenanceRate:  maintenanceRate,
		VacancyRate:      vacancyRate,
		PropertyMgmtRate: propertyMgmtRate,

		ExpenseRatio: math.Round(expenseRatio*10) / 10,
		NOI:          math.Round(noi),
		CapRate:      math.Round(capRate*100) / 100,
	}, nil
}

// GetMarketDefaults returns typical expense rates for a state/market
func (c *Calculator) GetMarketDefaults(state string) *MarketExpenseDefaults {
	taxRate := c.getPropertyTaxRate(state)
	insuranceRate := c.getInsuranceRate(state)
	riskFactors := c.getInsuranceRiskFactors(state)
	notes := c.getMarketNotes(state)

	return &MarketExpenseDefaults{
		State:            state,
		PropertyTaxRate:  taxRate,
		InsuranceRate:    insuranceRate,
		InsuranceRisk:    riskFactors,
		MaintenanceRate:  DefaultMaintenanceRate,
		VacancyRate:      DefaultVacancyRate,
		PropertyMgmtRate: DefaultPropertyMgmtRate,
		Notes:            notes,
	}
}

// getPropertyTaxRate returns the effective property tax rate for a state
func (c *Calculator) getPropertyTaxRate(state string) float64 {
	if rate, ok := propertyTaxRates[state]; ok {
		return rate.Rate
	}
	return DefaultPropertyTaxRate
}

// getInsuranceRate returns the insurance rate as % of home value
func (c *Calculator) getInsuranceRate(state string) float64 {
	if rate, ok := insuranceRates[state]; ok {
		return rate.RatePercent
	}
	return DefaultInsuranceRate
}

// getInsuranceRiskFactors returns risk factors for a state
func (c *Calculator) getInsuranceRiskFactors(state string) []string {
	if rate, ok := insuranceRates[state]; ok {
		return rate.RiskFactors
	}
	return nil
}

// getMarketNotes returns any special notes for a state
func (c *Calculator) getMarketNotes(state string) string {
	var notes []string

	if taxInfo, ok := propertyTaxRates[state]; ok && taxInfo.Notes != "" {
		notes = append(notes, taxInfo.Notes)
	}
	if insInfo, ok := insuranceRates[state]; ok && insInfo.Notes != "" {
		notes = append(notes, insInfo.Notes)
	}

	if len(notes) > 0 {
		return notes[0] // Return first note for simplicity
	}
	return ""
}

// getMaintenanceRate calculates maintenance rate based on property age
func (c *Calculator) getMaintenanceRate(yearBuilt int) float64 {
	if yearBuilt <= 0 {
		return DefaultMaintenanceRate
	}

	currentYear := time.Now().Year()
	age := currentYear - yearBuilt

	var factor float64
	switch {
	case age <= 5:
		factor = MaintenanceAgeFactors["new"]
	case age <= 15:
		factor = MaintenanceAgeFactors["modern"]
	case age <= 30:
		factor = MaintenanceAgeFactors["mature"]
	case age <= 50:
		factor = MaintenanceAgeFactors["older"]
	default:
		factor = MaintenanceAgeFactors["historic"]
	}

	return DefaultMaintenanceRate * factor
}

// getPropertyMgmtRate returns property management fee based on market class
// Major metros have more competition = lower fees
// Smaller markets have fewer options = higher fees
func (c *Calculator) getPropertyMgmtRate(city string) float64 {
	marketClass := c.getMarketClass(city)
	if rate, ok := managementFeeByClass[marketClass]; ok {
		return rate
	}
	return DefaultPropertyMgmtRate
}

// getMarketClass determines the market class for a city
func (c *Calculator) getMarketClass(city string) MarketClass {
	if city == "" {
		return MarketClassB // Default to Class B if no city provided
	}

	// Check major metros first (Class A)
	if majorMetros[city] {
		return MarketClassA
	}

	// Check secondary markets (Class B)
	if secondaryMarkets[city] {
		return MarketClassB
	}

	// Default to Class C for smaller/unknown cities
	return MarketClassC
}

// GetMarketClassInfo returns market class details for a city (for transparency)
func (c *Calculator) GetMarketClassInfo(city string) (MarketClass, float64, string) {
	class := c.getMarketClass(city)
	rate := managementFeeByClass[class]
	var description string
	switch class {
	case MarketClassA:
		description = "Major metro - high competition, lower fees"
	case MarketClassB:
		description = "Secondary market - moderate fees"
	case MarketClassC:
		description = "Smaller market - fewer options, higher fees"
	}
	return class, rate, description
}

// FormatExpenseContext creates a text summary for AI prompt context
func FormatExpenseContext(exp *OperatingExpenses, state string) string {
	return fmt.Sprintf(`OPERATING EXPENSE ESTIMATES (State: %s):
- Property Tax: $%.0f/year (%.2f%% of value)
- Insurance: $%.0f/year (%.2f%% of value)
- Maintenance: $%.0f/year (%.2f%% of value, age-adjusted)
- Vacancy Reserve: $%.0f/year (%.1f%% of rent)
- Property Management: $%.0f/year (%.1f%% of rent)
- Total Operating Expenses: $%.0f/year ($%.0f/month)
- Expense Ratio: %.1f%% of gross rent
- Net Operating Income (NOI): $%.0f/year
- Cap Rate: %.2f%%`,
		state,
		exp.PropertyTax, exp.PropertyTaxRate,
		exp.Insurance, exp.InsuranceRate,
		exp.Maintenance, exp.MaintenanceRate,
		exp.VacancyAllowance, exp.VacancyRate,
		exp.PropertyMgmt, exp.PropertyMgmtRate,
		exp.TotalAnnual, exp.TotalMonthly,
		exp.ExpenseRatio,
		exp.NOI,
		exp.CapRate,
	)
}
