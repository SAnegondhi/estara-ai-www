package memo

import (
	"fmt"
	"math"

	"github.com/estara-ai/www/internal/services/investment/expenses"
	"github.com/estara-ai/www/internal/services/market/aggregator"
)

// Calculator computes enhanced financial metrics for Decision Memos.
type Calculator struct {
	expCalc *expenses.Calculator
}

// NewCalculator creates a new memo financial calculator.
func NewCalculator() *Calculator {
	return &Calculator{
		expCalc: expenses.NewCalculator(),
	}
}

// CalculationInput holds all inputs needed to compute memo financials.
type CalculationInput struct {
	Property       BatchPropertyInput
	MarketData     *aggregator.MarketData
	MortgageRate   float64 // Current 30yr fixed from FRED
	MarketVacancy  float64 // Market vacancy rate from FRED (%)
}

// CalculationOutput holds computed financials for a single property.
type CalculationOutput struct {
	KeyFinancials       KeyFinancials
	Assumptions         []AssumptionRow
	CashFlowProjections []YearCashFlow
	Scenarios           []ScenarioRow
}

// scenarioParams defines assumptions for one scenario.
type scenarioParams struct {
	Name             string
	AppreciationRate float64
	VacancyRate      float64
	RentGrowthRate   float64
	ExpenseGrowth    float64
	RateAdjust       float64 // refi rate adjustment (bps, for downside)
}

// Compute calculates all financial metrics for one property.
func (c *Calculator) Compute(input CalculationInput) *CalculationOutput {
	prop := input.Property
	if prop.Price <= 0 || prop.EstimatedRent <= 0 {
		return nil
	}

	// Financing assumptions
	downPaymentPct := 0.25
	mortgageRate := input.MortgageRate
	if mortgageRate <= 0 {
		mortgageRate = 7.0
	}
	loanTermYears := 30

	price := float64(prop.Price)
	downPayment := price * downPaymentPct
	loanAmount := price - downPayment
	monthlyRent := float64(prop.EstimatedRent)

	// Calculate expenses via expenses.Calculator
	expInput := expenses.PropertyInput{
		Price:         prop.Price,
		State:         prop.State,
		City:          prop.City,
		YearBuilt:     prop.YearBuilt,
		EstimatedRent: prop.EstimatedRent,
		PropertyType:  prop.PropertyType,
	}
	if input.MarketVacancy > 0 {
		v := input.MarketVacancy
		expInput.VacancyRateOverride = &v
	}
	opex, err := c.expCalc.Calculate(expInput)
	if err != nil {
		return nil
	}

	// Monthly mortgage (P&I)
	monthlyRate := (mortgageRate / 100) / 12
	numPayments := float64(loanTermYears * 12)
	monthlyPayment := loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) / (math.Pow(1+monthlyRate, numPayments) - 1)
	annualDebtService := monthlyPayment * 12

	// Key financials
	noi := opex.NOI
	capRate := opex.CapRate
	cashOnCashYr1 := 0.0
	annualCashFlow := noi - annualDebtService
	if downPayment > 0 {
		cashOnCashYr1 = (annualCashFlow / downPayment) * 100
	}
	dscr := 0.0
	if annualDebtService > 0 {
		dscr = noi / annualDebtService
	}

	// Base scenario assumptions
	appreciationRate := 0.03
	rentGrowthRate := 0.02

	// 10-year projections
	projections := c.project10Year(price, monthlyRent, opex.TotalAnnual, annualDebtService, appreciationRate, rentGrowthRate, 0.025, downPayment, loanAmount, mortgageRate/100, loanTermYears)

	// 5-year IRR (approximation)
	irr5Yr := c.approximateIRR(projections[:5], downPayment)

	// 10-year equity multiple
	equityMultiple10 := 1.0
	if downPayment > 0 && len(projections) == 10 {
		totalCF := 0.0
		for _, yr := range projections {
			totalCF += yr.NetCashFlow
		}
		finalEquity := projections[9].Equity
		equityMultiple10 = (totalCF + finalEquity) / downPayment
	}

	kf := KeyFinancials{
		PurchasePrice:    prop.Price,
		DownPayment:      int(downPayment),
		LoanAmount:       int(loanAmount),
		InterestRate:     mortgageRate,
		MonthlyPayment:   math.Round(monthlyPayment*100) / 100,
		MonthlyRent:      prop.EstimatedRent,
		NOI:              math.Round(noi*100) / 100,
		CapRate:          math.Round(capRate*100) / 100,
		CashOnCashYr1:    math.Round(cashOnCashYr1*100) / 100,
		DSCR:             math.Round(dscr*100) / 100,
		IRR5Yr:           math.Round(irr5Yr*100) / 100,
		EquityMultiple10: math.Round(equityMultiple10*100) / 100,
		TotalExpenses:    math.Round(opex.TotalAnnual*100) / 100,
		ExpenseRatio:     math.Round(opex.ExpenseRatio*100) / 100,
	}

	// Underwriting assumptions vs market
	assumptions := c.buildAssumptions(input, *opex, mortgageRate)

	// Stress-test scenarios
	scenarios := c.computeScenarios(price, monthlyRent, downPayment, loanAmount, *opex, mortgageRate, loanTermYears)

	return &CalculationOutput{
		KeyFinancials:       kf,
		Assumptions:         assumptions,
		CashFlowProjections: projections,
		Scenarios:           scenarios,
	}
}

// project10Year builds a 10-year cash-flow projection.
func (c *Calculator) project10Year(
	price, monthlyRent, annualExpenses, annualDebtService float64,
	appreciationRate, rentGrowthRate, expenseGrowthRate float64,
	downPayment, loanAmount, annualRate float64,
	loanTermYears int,
) []YearCashFlow {
	projections := make([]YearCashFlow, 10)
	cumulativeCF := 0.0
	currentPropertyValue := price
	currentRent := monthlyRent
	currentExpenses := annualExpenses

	// Calculate remaining loan balance over time
	monthlyRate := annualRate / 12
	numPayments := float64(loanTermYears * 12)
	monthlyPayment := loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) / (math.Pow(1+monthlyRate, numPayments) - 1)

	for yr := 0; yr < 10; yr++ {
		if yr > 0 {
			currentPropertyValue *= (1 + appreciationRate)
			currentRent *= (1 + rentGrowthRate)
			currentExpenses *= (1 + expenseGrowthRate)
		}

		grossRent := currentRent * 12
		noi := grossRent - currentExpenses
		netCashFlow := noi - annualDebtService
		cumulativeCF += netCashFlow

		// Remaining loan balance after yr+1 years of payments
		paymentsMade := float64((yr + 1) * 12)
		remainingBalance := loanAmount * (math.Pow(1+monthlyRate, numPayments) - math.Pow(1+monthlyRate, paymentsMade)) / (math.Pow(1+monthlyRate, numPayments) - 1)
		equity := currentPropertyValue - remainingBalance

		projections[yr] = YearCashFlow{
			Year:          yr + 1,
			GrossRent:     math.Round(grossRent),
			Expenses:      math.Round(currentExpenses),
			NOI:           math.Round(noi),
			DebtService:   math.Round(annualDebtService),
			NetCashFlow:   math.Round(netCashFlow),
			PropertyValue: math.Round(currentPropertyValue),
			Equity:        math.Round(equity),
			CumulativeCF:  math.Round(cumulativeCF),
		}

		_ = monthlyPayment // used for annualDebtService consistency
	}
	return projections
}

// approximateIRR computes an approximate IRR from year-end cash flows.
func (c *Calculator) approximateIRR(projections []YearCashFlow, initialInvestment float64) float64 {
	if initialInvestment <= 0 || len(projections) == 0 {
		return 0
	}
	n := len(projections)
	// Cash flows: -initial, cf1, cf2, ..., cfn + equity_n
	cashFlows := make([]float64, n+1)
	cashFlows[0] = -initialInvestment
	for i, yr := range projections {
		cf := yr.NetCashFlow
		if i == n-1 {
			cf += yr.Equity // Terminal value
		}
		cashFlows[i+1] = cf
	}
	return newtonIRR(cashFlows)
}

// newtonIRR uses Newton's method to solve for IRR.
func newtonIRR(cashFlows []float64) float64 {
	rate := 0.10 // Initial guess: 10%
	for iter := 0; iter < 100; iter++ {
		npv := 0.0
		dnpv := 0.0
		for t, cf := range cashFlows {
			disc := math.Pow(1+rate, float64(t))
			npv += cf / disc
			if t > 0 {
				dnpv -= float64(t) * cf / math.Pow(1+rate, float64(t)+1)
			}
		}
		if math.Abs(dnpv) < 1e-10 {
			break
		}
		newRate := rate - npv/dnpv
		if math.Abs(newRate-rate) < 1e-7 {
			rate = newRate
			break
		}
		rate = newRate
		// Clamp to reasonable range
		if rate < -0.5 {
			rate = -0.5
		} else if rate > 2.0 {
			rate = 2.0
		}
	}
	return rate * 100 // Return as percentage
}

// computeScenarios calculates Base, Conservative, and Downside scenarios.
func (c *Calculator) computeScenarios(
	price, monthlyRent, downPayment, loanAmount float64,
	opex expenses.OperatingExpenses,
	mortgageRate float64,
	loanTermYears int,
) []ScenarioRow {
	scenarios := []scenarioParams{
		{
			Name:             "Base",
			AppreciationRate: 0.03,
			VacancyRate:      opex.VacancyRate / 100, // Already calculated from market
			RentGrowthRate:   0.02,
			ExpenseGrowth:    0.025,
		},
		{
			Name:             "Conservative",
			AppreciationRate: 0.015,
			VacancyRate:      math.Min((opex.VacancyRate+3)/100, 0.15), // +3% above market
			RentGrowthRate:   0.01,
			ExpenseGrowth:    0.03,
		},
		{
			Name:             "Downside",
			AppreciationRate: 0.0,
			VacancyRate:      math.Min((opex.VacancyRate+5)/100, 0.20), // +5% above market
			RentGrowthRate:   0.0,
			ExpenseGrowth:    0.035,
			RateAdjust:       1.0, // +1% on refinance
		},
	}

	rows := make([]ScenarioRow, len(scenarios))
	for i, s := range scenarios {
		// Override vacancy in expenses for this scenario
		adjustedAnnualRent := monthlyRent * 12 * (1 - s.VacancyRate)
		nonVacancyExpenses := opex.TotalAnnual - opex.VacancyAllowance
		adjustedExpenses := nonVacancyExpenses + (monthlyRent * 12 * s.VacancyRate)
		noi := adjustedAnnualRent - (adjustedExpenses - monthlyRent*12*s.VacancyRate)
		// Simpler: NOI = grossRent * (1 - vacancyRate) - nonVacancyExpenses
		noi = monthlyRent*12*(1-s.VacancyRate) - nonVacancyExpenses

		// Debt service (may adjust rate for downside refi)
		rate := mortgageRate / 100
		if s.RateAdjust > 0 {
			rate += s.RateAdjust / 100
		}
		monthlyRate := rate / 12
		numPayments := float64(loanTermYears * 12)
		mp := loanAmount * (monthlyRate * math.Pow(1+monthlyRate, numPayments)) / (math.Pow(1+monthlyRate, numPayments) - 1)
		annualDS := mp * 12

		annualCF := noi - annualDS
		dscr := 0.0
		if annualDS > 0 {
			dscr = noi / annualDS
		}
		cashOnCash := 0.0
		if downPayment > 0 {
			cashOnCash = (annualCF / downPayment) * 100
		}

		// 5-year projection for IRR + equity multiple
		projections := c.project10Year(
			price, monthlyRent, nonVacancyExpenses+(monthlyRent*12*s.VacancyRate),
			annualDS, s.AppreciationRate, s.RentGrowthRate, s.ExpenseGrowth,
			downPayment, loanAmount, rate, loanTermYears,
		)

		irr5 := c.approximateIRR(projections[:5], downPayment)
		em := 1.0
		if downPayment > 0 && len(projections) >= 5 {
			totalCF := 0.0
			for _, yr := range projections[:5] {
				totalCF += yr.NetCashFlow
			}
			em = (totalCF + projections[4].Equity) / downPayment
		}

		riskLevel := "Low"
		if dscr < 1.0 {
			riskLevel = "High"
		} else if dscr < 1.25 {
			riskLevel = "Moderate"
		}

		rows[i] = ScenarioRow{
			Name:           s.Name,
			IRR:            math.Round(irr5*100) / 100,
			CashOnCash:     math.Round(cashOnCash*100) / 100,
			EquityMultiple: math.Round(em*100) / 100,
			DSCR:           math.Round(dscr*100) / 100,
			RiskLevel:      riskLevel,
		}
	}
	return rows
}

// buildAssumptions creates the assumption vs market comparison rows.
func (c *Calculator) buildAssumptions(input CalculationInput, opex expenses.OperatingExpenses, mortgageRate float64) []AssumptionRow {
	rows := []AssumptionRow{
		{
			Parameter:  "Vacancy Rate",
			Assumption: fmt.Sprintf("%.1f%%", opex.VacancyRate),
			MarketAvg:  c.marketVacancy(input),
			Variance:   c.vacancyVariance(opex.VacancyRate, input),
		},
		{
			Parameter:  "Property Tax Rate",
			Assumption: fmt.Sprintf("%.2f%%", opex.PropertyTaxRate),
			MarketAvg:  fmt.Sprintf("%.2f%% (nat'l avg)", 1.07),
			Variance:   c.rateVariance(opex.PropertyTaxRate, 1.07),
		},
		{
			Parameter:  "Insurance Rate",
			Assumption: fmt.Sprintf("%.2f%%", opex.InsuranceRate),
			MarketAvg:  fmt.Sprintf("%.2f%% (nat'l avg)", 0.60),
			Variance:   c.rateVariance(opex.InsuranceRate, 0.60),
		},
		{
			Parameter:  "Maintenance Rate",
			Assumption: fmt.Sprintf("%.2f%%", opex.MaintenanceRate),
			MarketAvg:  fmt.Sprintf("%.2f%% (standard)", 1.0),
			Variance:   c.rateVariance(opex.MaintenanceRate, 1.0),
		},
		{
			Parameter:  "Mgmt Fee",
			Assumption: fmt.Sprintf("%.1f%%", opex.PropertyMgmtRate),
			MarketAvg:  "8-10% (typical)",
			Variance:   c.rateVariance(opex.PropertyMgmtRate, 9.0),
		},
		{
			Parameter:  "Mortgage Rate",
			Assumption: fmt.Sprintf("%.2f%%", mortgageRate),
			MarketAvg:  c.marketMortgageRate(input),
			Variance:   c.mortgageVariance(mortgageRate, input),
		},
		{
			Parameter:  "Appreciation Rate",
			Assumption: "3.0%",
			MarketAvg:  c.marketAppreciation(input),
			Variance:   c.appreciationVariance(input),
		},
		{
			Parameter:  "Rent Growth Rate",
			Assumption: "2.0%",
			MarketAvg:  c.marketRentGrowth(input),
			Variance:   c.rentGrowthVariance(input),
		},
	}

	// Set variance direction
	for i := range rows {
		if rows[i].VarianceDirection == "" {
			rows[i].VarianceDirection = "neutral"
		}
	}
	return rows
}

func (c *Calculator) marketVacancy(input CalculationInput) string {
	if input.MarketVacancy > 0 {
		return fmt.Sprintf("%.1f%% (FRED)", input.MarketVacancy)
	}
	return "6.5% (nat'l avg)"
}

func (c *Calculator) vacancyVariance(used float64, input CalculationInput) string {
	avg := 6.5
	if input.MarketVacancy > 0 {
		avg = input.MarketVacancy
	}
	diff := used - avg
	if math.Abs(diff) < 0.5 {
		return "In line"
	}
	return fmt.Sprintf("%+.1f%%", diff)
}

func (c *Calculator) rateVariance(used, avg float64) string {
	diff := used - avg
	if math.Abs(diff) < 0.05 {
		return "In line"
	}
	return fmt.Sprintf("%+.2f%%", diff)
}

func (c *Calculator) marketMortgageRate(input CalculationInput) string {
	if input.MortgageRate > 0 {
		return fmt.Sprintf("%.2f%% (FRED)", input.MortgageRate)
	}
	return "7.00% (est.)"
}

func (c *Calculator) mortgageVariance(used float64, input CalculationInput) string {
	avg := 7.0
	if input.MortgageRate > 0 {
		avg = input.MortgageRate
	}
	diff := used - avg
	if math.Abs(diff) < 0.05 {
		return "In line"
	}
	return fmt.Sprintf("%+.2f%%", diff)
}

func (c *Calculator) marketAppreciation(input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.YearOverYearPct != 0 {
		return fmt.Sprintf("%.1f%% (YoY)", input.MarketData.YearOverYearPct)
	}
	return "3.0% (nat'l avg)"
}

func (c *Calculator) appreciationVariance(input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.YearOverYearPct != 0 {
		diff := 3.0 - input.MarketData.YearOverYearPct
		if math.Abs(diff) < 0.5 {
			return "In line"
		}
		return fmt.Sprintf("%+.1f%%", -diff) // Negative if market is higher
	}
	return "In line"
}

func (c *Calculator) marketRentGrowth(input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.RentYearOverYear != 0 {
		return fmt.Sprintf("%.1f%% (YoY)", input.MarketData.RentYearOverYear)
	}
	return "2.0% (est.)"
}

func (c *Calculator) rentGrowthVariance(input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.RentYearOverYear != 0 {
		diff := 2.0 - input.MarketData.RentYearOverYear
		if math.Abs(diff) < 0.5 {
			return "In line"
		}
		return fmt.Sprintf("%+.1f%%", -diff)
	}
	return "In line"
}
