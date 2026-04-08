package memo

import (
	"fmt"
	"math"

	"github.com/estara-ai/www/internal/db/queries"
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
	PriceCAGR      float64 // 5-year price CAGR from time series (%)
	RentCAGR       float64 // 5-year rent CAGR from time series (%)
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

// deriveRentFromUnitMix computes the total monthly rent from a unit mix,
// preferring current rent, falling back to pro forma, then market.
func deriveRentFromUnitMix(mix []UnitMixInput) int {
	total := 0
	for _, u := range mix {
		r := u.RentCurrent
		if r == 0 {
			r = u.RentProForma
		}
		if r == 0 {
			r = u.RentMarket
		}
		total += r * u.Count
	}
	return total
}

// Compute calculates all financial metrics for one property.
func (c *Calculator) Compute(input CalculationInput) *CalculationOutput {
	prop := input.Property

	// Resolve effective price — prefer Price, fall back to TargetPrice
	effectivePrice := prop.Price
	if effectivePrice <= 0 && prop.TargetPrice > 0 {
		effectivePrice = prop.TargetPrice
	}

	// Resolve effective rent — prefer EstimatedRent, fall back to unit mix aggregate
	effectiveRent := prop.EstimatedRent
	if effectiveRent <= 0 && len(prop.UnitMix) > 0 {
		effectiveRent = deriveRentFromUnitMix(prop.UnitMix)
	}

	if effectivePrice <= 0 || effectiveRent <= 0 {
		return nil
	}

	// Financing — use user-provided values when available, else fall back to defaults
	downPaymentPct := 0.25
	if prop.DownPaymentPct > 0 && prop.DownPaymentPct <= 1.0 {
		downPaymentPct = prop.DownPaymentPct
	}

	// Interest rate priority: user-provided → FRED current rate → hardcoded fallback
	mortgageRate := input.MortgageRate
	if prop.InterestRate > 0 && prop.InterestRate < 30 {
		mortgageRate = prop.InterestRate
	}
	if mortgageRate <= 0 {
		mortgageRate = 7.0
	}
	loanTermYears := 30

	price := float64(effectivePrice)
	downPayment := price * downPaymentPct
	loanAmount := price - downPayment
	monthlyRent := float64(effectiveRent)

	// Calculate expenses via expenses.Calculator
	expInput := expenses.PropertyInput{
		Price:         effectivePrice,
		State:         prop.State,
		City:          prop.City,
		YearBuilt:     prop.YearBuilt,
		EstimatedRent: effectiveRent,
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

	// Base scenario assumptions — use market data when available
	appreciationRate := c.resolveAppreciationRate(input)
	rentGrowthRate := c.resolveRentGrowthRate(input)

	// 10-year projections
	projections := c.project10Year(price, monthlyRent, opex.TotalAnnual, annualDebtService, appreciationRate, rentGrowthRate, 0.025, loanAmount, mortgageRate/100, loanTermYears)

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
		PurchasePrice:    effectivePrice,
		DownPayment:      int(downPayment),
		LoanAmount:       int(loanAmount),
		InterestRate:     mortgageRate,
		MonthlyPayment:   math.Round(monthlyPayment*100) / 100,
		MonthlyRent:      effectiveRent,
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
	assumptions := c.buildAssumptions(input, *opex, mortgageRate, appreciationRate, rentGrowthRate)

	// Stress-test scenarios
	scenarios := c.computeScenarios(price, monthlyRent, downPayment, loanAmount, *opex, mortgageRate, loanTermYears, appreciationRate, rentGrowthRate)

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
	loanAmount, annualRate float64,
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

// resolveAppreciationRate returns a data-driven appreciation rate, falling back to 3%.
func (c *Calculator) resolveAppreciationRate(input CalculationInput) float64 {
	// Prefer 5yr CAGR (more stable than 1yr YoY)
	if input.PriceCAGR != 0 {
		rate := input.PriceCAGR / 100
		return math.Max(-0.05, math.Min(rate, 0.10))
	}
	// Fall back to dampened 1yr YoY (70% to account for mean reversion)
	if input.MarketData != nil && input.MarketData.YearOverYearPct != 0 {
		rate := input.MarketData.YearOverYearPct / 100 * 0.70
		return math.Max(-0.05, math.Min(rate, 0.10))
	}
	return 0.03 // national fallback
}

// resolveRentGrowthRate returns a data-driven rent growth rate, falling back to 2%.
func (c *Calculator) resolveRentGrowthRate(input CalculationInput) float64 {
	if input.RentCAGR != 0 {
		rate := input.RentCAGR / 100
		return math.Max(-0.03, math.Min(rate, 0.08))
	}
	if input.MarketData != nil && input.MarketData.RentYearOverYear != 0 {
		rate := input.MarketData.RentYearOverYear / 100 * 0.70
		return math.Max(-0.03, math.Min(rate, 0.08))
	}
	return 0.02 // national fallback
}

// computeScenarios calculates Base, Conservative, and Downside scenarios.
func (c *Calculator) computeScenarios(
	price, monthlyRent, downPayment, loanAmount float64,
	opex expenses.OperatingExpenses,
	mortgageRate float64,
	loanTermYears int,
	baseAppreciation, baseRentGrowth float64,
) []ScenarioRow {
	scenarios := []scenarioParams{
		{
			Name:             "Base",
			AppreciationRate: baseAppreciation,
			VacancyRate:      opex.VacancyRate / 100, // Already calculated from market
			RentGrowthRate:   baseRentGrowth,
			ExpenseGrowth:    0.025,
		},
		{
			Name:             "Conservative",
			AppreciationRate: baseAppreciation * 0.50, // 50% of base
			VacancyRate:      math.Min((opex.VacancyRate+3)/100, 0.15), // +3% above market
			RentGrowthRate:   baseRentGrowth * 0.50,
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
			loanAmount, rate, loanTermYears,
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
func (c *Calculator) buildAssumptions(input CalculationInput, opex expenses.OperatingExpenses, mortgageRate float64, appreciationRate, rentGrowthRate float64) []AssumptionRow {
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
			MarketAvg:  c.statePropertyTaxAvg(input.Property.State),
			Variance:   c.rateVariance(opex.PropertyTaxRate, expenses.GetPropertyTaxRate(input.Property.State)),
		},
		{
			Parameter:  "Insurance Rate",
			Assumption: fmt.Sprintf("%.2f%%", opex.InsuranceRate),
			MarketAvg:  c.stateInsuranceAvg(input.Property.State),
			Variance:   c.rateVariance(opex.InsuranceRate, expenses.GetInsuranceRate(input.Property.State)),
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
			Assumption: fmt.Sprintf("%.1f%%", appreciationRate*100),
			MarketAvg:  c.marketAppreciation(input),
			Variance:   c.appreciationVarianceActual(appreciationRate*100, input),
		},
		{
			Parameter:  "Rent Growth Rate",
			Assumption: fmt.Sprintf("%.1f%%", rentGrowthRate*100),
			MarketAvg:  c.marketRentGrowth(input),
			Variance:   c.rentGrowthVarianceActual(rentGrowthRate*100, input),
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

func (c *Calculator) statePropertyTaxAvg(state string) string {
	rate := expenses.GetPropertyTaxRate(state)
	if state != "" {
		return fmt.Sprintf("%.2f%% (%s avg)", rate, state)
	}
	return fmt.Sprintf("%.2f%% (nat'l avg)", rate)
}

func (c *Calculator) stateInsuranceAvg(state string) string {
	rate := expenses.GetInsuranceRate(state)
	if state != "" {
		return fmt.Sprintf("%.2f%% (%s avg)", rate, state)
	}
	return fmt.Sprintf("%.2f%% (nat'l avg)", rate)
}

// appreciationVarianceActual computes variance between actual used rate and market YoY.
func (c *Calculator) appreciationVarianceActual(usedPct float64, input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.YearOverYearPct != 0 {
		diff := usedPct - input.MarketData.YearOverYearPct
		if math.Abs(diff) < 0.5 {
			return "In line"
		}
		return fmt.Sprintf("%+.1f%%", diff)
	}
	return "In line"
}

// rentGrowthVarianceActual computes variance between actual used rate and market YoY.
func (c *Calculator) rentGrowthVarianceActual(usedPct float64, input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.RentYearOverYear != 0 {
		diff := usedPct - input.MarketData.RentYearOverYear
		if math.Abs(diff) < 0.5 {
			return "In line"
		}
		return fmt.Sprintf("%+.1f%%", diff)
	}
	return "In line"
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

func (c *Calculator) marketRentGrowth(input CalculationInput) string {
	if input.MarketData != nil && input.MarketData.RentYearOverYear != 0 {
		return fmt.Sprintf("%.1f%% (YoY)", input.MarketData.RentYearOverYear)
	}
	return "2.0% (est.)"
}

// ComputePortfolioImpacts calculates how acquiring each candidate property
// would change the user's portfolio metrics relative to the current snapshot.
func (c *Calculator) ComputePortfolioImpacts(snapshot queries.V2PortfolioSnapshot, properties []BatchPropertyInput) []*PortfolioImpactData {
	results := make([]*PortfolioImpactData, len(properties))

	currentValue := snapshot.TotalValue
	currentEquity := snapshot.TotalEquity
	currentCapRate := snapshot.PortfolioCapRate
	propCount := int(snapshot.PropertyCount)
	currentCF := snapshot.MonthlyCashFlow * 12 // annualize

	for i, prop := range properties {
		if prop.Price <= 0 {
			continue
		}
		price := float64(prop.Price)
		downPayment := price * 0.25
		annualRent := float64(prop.EstimatedRent) * 12
		// Rough NOI estimate: 55% of gross rent (conservative)
		newNOI := annualRent * 0.55

		newValue := currentValue + price
		newEquity := currentEquity + downPayment
		newCapRate := 0.0
		if newValue > 0 {
			// Blend: (currentNOI + newNOI) / newValue * 100
			currentNOI := (currentCapRate / 100) * currentValue
			newCapRate = (currentNOI + newNOI) / newValue * 100
		}
		newCount := propCount + 1

		// Cash flow impact (rough: NOI minus rough debt service)
		monthlyRate := 0.07 / 12
		numPay := 360.0
		loanAmt := price - downPayment
		monthlyPayment := loanAmt * (monthlyRate * math.Pow(1+monthlyRate, numPay)) / (math.Pow(1+monthlyRate, numPay) - 1)
		newAnnualCF := currentCF + newNOI - (monthlyPayment * 12)

		rows := []PortfolioImpactRow{
			{
				Dimension:       "Total Value",
				Current:         fmt.Sprintf("$%s", formatNumber(int(currentValue))),
				PostAcquisition: fmt.Sprintf("$%s", formatNumber(int(newValue))),
				Threshold:       "—",
			},
			{
				Dimension:       "Total Equity",
				Current:         fmt.Sprintf("$%s", formatNumber(int(currentEquity))),
				PostAcquisition: fmt.Sprintf("$%s", formatNumber(int(newEquity))),
				Threshold:       "—",
			},
			{
				Dimension:       "Portfolio Cap Rate",
				Current:         fmt.Sprintf("%.2f%%", currentCapRate),
				PostAcquisition: fmt.Sprintf("%.2f%%", newCapRate),
				Threshold:       "> 5.0%",
				IsWarning:       newCapRate < 5.0,
			},
			{
				Dimension:       "Property Count",
				Current:         fmt.Sprintf("%d", propCount),
				PostAcquisition: fmt.Sprintf("%d", newCount),
				Threshold:       "—",
			},
			{
				Dimension:       "Annual Cash Flow",
				Current:         fmt.Sprintf("$%s", formatNumber(int(currentCF))),
				PostAcquisition: fmt.Sprintf("$%s", formatNumber(int(newAnnualCF))),
				Threshold:       "> $0",
				IsWarning:       newAnnualCF < 0,
			},
		}

		impact := &PortfolioImpactData{Rows: rows}

		// Concentration warning if one property becomes >40% of portfolio value
		propPct := price / newValue * 100
		if propPct > 40 {
			impact.ConcentrationWarning = fmt.Sprintf("This acquisition would represent %.0f%% of total portfolio value", propPct)
		}
		if newCapRate > currentCapRate+0.25 {
			impact.DiversificationNote = fmt.Sprintf("Improves blended cap rate by %.2f%%", newCapRate-currentCapRate)
		}

		results[i] = impact
	}

	return results
}
