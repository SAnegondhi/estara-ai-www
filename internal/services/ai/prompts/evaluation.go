package prompts

// EvaluationChatSystemPrompt is the system prompt for the evaluation chat agent
const EvaluationChatSystemPrompt = `You are Estara's Property Evaluation Analyst, an AI assistant helping users evaluate investment properties. Your role is to provide detailed, data-driven analysis while maintaining strict compliance with SEC regulations.

CAPABILITIES:
- Analyze property investment potential
- Compare properties against user's existing portfolio
- Run stress tests under various economic scenarios
- Calculate key investment metrics
- Provide market context and insights

OUTPUT FORMAT - Use these tagged blocks for structured output:

[INSIGHT]
Title: <headline>
Type: opportunity|risk|neutral
Confidence: high|medium|low
Summary: <1-2 sentences>
Details: <detailed analysis>
[/INSIGHT]

[STRESS_TEST]
Scenario: mild_recession|severe_recession|interest_rate_shock|local_downturn|custom
ValueImpact: <percentage, e.g., -15%>
RentImpact: <percentage, e.g., -10%>
CashFlowImpact: <monthly dollar change, e.g., -$350/mo>
Narrative: <explanation>
[/STRESS_TEST]

STRESS TEST CALCULATION GUIDANCE:
For each scenario, calculate realistic impacts using these assumptions:
- Default financing: 80% LTV, 30-year fixed mortgage
- Current mortgage rate: Use market rate from context, or assume 7%

Scenario-specific calculations:
- mild_recession: ValueImpact -10%, RentImpact -5%, calculate reduced NOI
- severe_recession: ValueImpact -20%, RentImpact -15%, VacancyIncrease +10%
- interest_rate_shock: ValueImpact 0%, RentImpact 0%, CashFlowImpact = increased monthly payment
  * Calculate: For +200bps rate increase, recalculate monthly P&I at new rate
  * Example: $200K property at 80% LTV ($160K loan): 7% vs 9% = ~$180/mo increase
- local_downturn: ValueImpact -15%, RentImpact -10%, based on local market factors

IMPORTANT: Always provide non-zero values for CashFlowImpact. For interest_rate_shock,
calculate the increased debt service cost per month even if value/rent unchanged.

[METRICS]
| Metric | Value | Rating |
|--------|-------|--------|
| Cap Rate | X.X% | good|fair|poor |
| Cash on Cash | X.X% | good|fair|poor |
| DSCR | X.XX | good|fair|poor |
| Gross Yield | X.X% | good|fair|poor |
| Price/SqFt | $XXX | good|fair|poor |
[/METRICS]

[COMPARISON]
Property: <address>
vs Portfolio Average:
- Cap Rate: +/-X.X%
- Cash Flow: +/-$XXX/mo
- Risk Level: higher|similar|lower
[/COMPARISON]

[DISCLAIMER]
<SEC-compliant disclaimer>
[/DISCLAIMER]

COMPLIANCE REQUIREMENTS (ADR-044):
- Use neutral language: "data indicates", "analysis shows", "trends suggest"
- NEVER use: "I recommend", "you should", "guaranteed", "risk-free"
- Include disclaimers for all projections and forward-looking statements
- Present as decision-support information, not investment advice
- All investment metrics must include appropriate caveats

RESPONSE FORMATTING:
- Start responses with a clear, concise TITLE (e.g., "Investment Scenario Overview", "Stress Test Results", "Cash Flow Analysis")
- NEVER start with verbose phrases like "Based on the analysis of..." or "Here's your..." or "Let me provide..."
- Get straight to the point with actionable insights
- Use the structured output blocks ([INSIGHT], [METRICS], etc.) to organize information
- Keep narrative text brief and data-focused

CONVERSATION GUIDELINES:
- Be helpful and informative while staying compliant
- Ask clarifying questions when needed
- Provide context for all numbers and metrics
- Explain methodology when asked
- Acknowledge uncertainty where appropriate`

// EvaluationChatUserPromptTemplate is the template for building user prompts
const EvaluationChatUserPromptTemplate = `
PROPERTIES BEING EVALUATED:
{{PROPERTIES}}

{{#PORTFOLIO}}
EXISTING PORTFOLIO:
{{PORTFOLIO}}
{{/PORTFOLIO}}

{{#INVESTOR_PROFILE}}
INVESTOR PROFILE:
- Risk Tolerance: {{RISK_TOLERANCE}}
- Investment Horizon: {{INVESTMENT_HORIZON}}
{{#AVAILABLE_CAPITAL}}- Available Capital: ${{AVAILABLE_CAPITAL}}{{/AVAILABLE_CAPITAL}}
{{/INVESTOR_PROFILE}}

{{#MARKET_DATA}}
MARKET CONTEXT:
{{MARKET_DATA}}
{{/MARKET_DATA}}

USER MESSAGE:
{{MESSAGE}}`

// BuildEvaluationUserPrompt constructs the user prompt for evaluation chat
func BuildEvaluationUserPrompt(params EvaluationPromptParams) string {
	prompt := "PROPERTIES BEING EVALUATED:\n"
	prompt += params.PropertiesContext + "\n"

	if params.PortfolioContext != "" {
		prompt += "\nEXISTING PORTFOLIO:\n"
		prompt += params.PortfolioContext + "\n"
	}

	if params.InvestorProfile != nil {
		prompt += "\nINVESTOR PROFILE:\n"
		prompt += "- Risk Tolerance: " + params.InvestorProfile.RiskTolerance + "\n"
		prompt += "- Investment Horizon: " + params.InvestorProfile.InvestmentHorizon + "\n"
		if params.InvestorProfile.AvailableCapital > 0 {
			prompt += "- Available Capital: $" + formatInt(params.InvestorProfile.AvailableCapital) + "\n"
		}
	}

	if params.MarketContext != "" {
		prompt += "\nMARKET CONTEXT:\n"
		prompt += params.MarketContext + "\n"
	}

	prompt += "\nUSER MESSAGE:\n"
	prompt += params.UserMessage

	return prompt
}

// EvaluationPromptParams holds parameters for building evaluation prompts
type EvaluationPromptParams struct {
	PropertiesContext string
	PortfolioContext  string
	InvestorProfile   *InvestorProfile
	MarketContext     string
	UserMessage       string
}

// InvestorProfile represents the user's investment profile
type InvestorProfile struct {
	RiskTolerance     string // "conservative", "moderate", "aggressive"
	InvestmentHorizon string // e.g., "5-10 years", "10+ years"
	AvailableCapital  int
}

// BuildPropertyContext creates XML-formatted property context
func BuildPropertyContext(properties []PropertyContext) string {
	if len(properties) == 0 {
		return "<PROPERTIES count=\"0\">No properties selected</PROPERTIES>"
	}

	result := "<PROPERTIES count=\"" + formatInt(len(properties)) + "\">\n"
	for _, prop := range properties {
		result += "  <property id=\"" + prop.ID + "\">\n"
		result += "    <address>" + prop.Address + "</address>\n"
		result += "    <city>" + prop.City + "</city>\n"
		result += "    <state>" + prop.State + "</state>\n"
		result += "    <price>$" + formatInt(prop.Price) + "</price>\n"
		if prop.Beds > 0 {
			result += "    <beds>" + formatInt(prop.Beds) + "</beds>\n"
		}
		if prop.Baths > 0 {
			result += "    <baths>" + formatFloat(prop.Baths) + "</baths>\n"
		}
		if prop.Sqft > 0 {
			result += "    <sqft>" + formatInt(prop.Sqft) + "</sqft>\n"
		}
		if prop.EstimatedRent > 0 {
			result += "    <estimated_rent>$" + formatInt(prop.EstimatedRent) + "</estimated_rent>\n"
		}
		if prop.CapRate != "" {
			result += "    <cap_rate_range>" + prop.CapRate + "</cap_rate_range>\n"
		}
		if prop.YearBuilt > 0 {
			result += "    <year_built>" + formatInt(prop.YearBuilt) + "</year_built>\n"
		}
		if prop.PropertyType != "" {
			result += "    <property_type>" + prop.PropertyType + "</property_type>\n"
		}
		// Include calculated operating expenses if available
		if prop.OperatingExpenses != nil {
			exp := prop.OperatingExpenses
			result += "    <operating_expenses>\n"
			result += "      <property_tax>$" + formatFloat(exp.PropertyTax) + "/year</property_tax>\n"
			result += "      <insurance>$" + formatFloat(exp.Insurance) + "/year</insurance>\n"
			result += "      <maintenance>$" + formatFloat(exp.Maintenance) + "/year</maintenance>\n"
			result += "      <vacancy_allowance>$" + formatFloat(exp.VacancyAllowance) + "/year</vacancy_allowance>\n"
			result += "      <property_mgmt>$" + formatFloat(exp.PropertyMgmt) + "/year</property_mgmt>\n"
			result += "      <total_annual>$" + formatFloat(exp.TotalAnnual) + "/year</total_annual>\n"
			result += "      <total_monthly>$" + formatFloat(exp.TotalMonthly) + "/month</total_monthly>\n"
			result += "      <expense_ratio>" + formatFloat(exp.ExpenseRatio) + "% of rent</expense_ratio>\n"
			result += "      <noi>$" + formatFloat(exp.NOI) + "/year</noi>\n"
			result += "      <calculated_cap_rate>" + formatFloat(exp.CapRate) + "%</calculated_cap_rate>\n"
			result += "    </operating_expenses>\n"
		}
		result += "  </property>\n"
	}
	result += "</PROPERTIES>"

	return result
}

// PropertyContext holds property data for prompt building
type PropertyContext struct {
	ID            string
	Address       string
	City          string
	State         string
	Price         int
	Beds          int
	Baths         float64
	Sqft          int
	EstimatedRent int
	CapRate       string
	YearBuilt     int
	PropertyType  string

	// Calculated operating expenses (optional, populated by handler)
	OperatingExpenses *PropertyExpenses
}

// PropertyExpenses holds calculated operating expense data
type PropertyExpenses struct {
	PropertyTax      float64 `json:"propertyTax"`      // Annual property tax
	Insurance        float64 `json:"insurance"`        // Annual insurance
	Maintenance      float64 `json:"maintenance"`      // Annual maintenance
	VacancyAllowance float64 `json:"vacancyAllowance"` // Annual vacancy reserve
	PropertyMgmt     float64 `json:"propertyMgmt"`     // Annual property management
	TotalAnnual      float64 `json:"totalAnnual"`      // Total annual expenses
	TotalMonthly     float64 `json:"totalMonthly"`     // Total monthly expenses
	ExpenseRatio     float64 `json:"expenseRatio"`     // % of gross rent
	NOI              float64 `json:"noi"`              // Net Operating Income
	CapRate          float64 `json:"capRate"`          // Calculated cap rate
}

// BuildPortfolioContext creates XML-formatted portfolio context
func BuildPortfolioContext(portfolio *PortfolioContext) string {
	if portfolio == nil {
		return ""
	}

	result := "<EXISTING_PORTFOLIO>\n"
	result += "  <property_count>" + formatInt(portfolio.PropertyCount) + "</property_count>\n"
	result += "  <total_value>$" + formatInt(portfolio.TotalValue) + "</total_value>\n"
	result += "  <total_equity>$" + formatInt(portfolio.TotalEquity) + "</total_equity>\n"
	if portfolio.TotalDebt > 0 {
		result += "  <total_debt>$" + formatInt(portfolio.TotalDebt) + "</total_debt>\n"
	}
	result += "  <monthly_cash_flow>$" + formatInt(portfolio.MonthlyCashFlow) + "</monthly_cash_flow>\n"
	if portfolio.AvgCapRate > 0 {
		result += "  <avg_cap_rate>" + formatFloat(portfolio.AvgCapRate) + "%</avg_cap_rate>\n"
	}
	if len(portfolio.Locations) > 0 {
		result += "  <locations>" + joinStrings(portfolio.Locations, ", ") + "</locations>\n"
	}
	result += "</EXISTING_PORTFOLIO>"

	return result
}

// PortfolioContext holds portfolio data for prompt building
type PortfolioContext struct {
	PropertyCount   int
	TotalValue      int
	TotalEquity     int
	TotalDebt       int
	MonthlyCashFlow int
	AvgCapRate      float64
	Locations       []string
}

// StressTestPrompt creates a prompt for stress test analysis
func StressTestPrompt(scenario string, params *StressTestParams) string {
	prompt := "Run a stress test analysis on the selected properties using the following scenario:\n\n"
	prompt += "SCENARIO: " + scenario + "\n"

	if params != nil {
		prompt += "\nPARAMETERS:\n"
		if params.RentDecline > 0 {
			prompt += "- Rent Decline: " + formatFloat(params.RentDecline) + "%\n"
		}
		if params.VacancyIncrease > 0 {
			prompt += "- Vacancy Increase: " + formatFloat(params.VacancyIncrease) + "%\n"
		}
		if params.ExpenseIncrease > 0 {
			prompt += "- Expense Increase: " + formatFloat(params.ExpenseIncrease) + "%\n"
		}
		if params.CapRateExpansion > 0 {
			prompt += "- Cap Rate Expansion: " + formatFloat(params.CapRateExpansion) + " bps\n"
		}
		if params.InterestRateChange != 0 {
			prompt += "- Interest Rate Change: " + formatFloat(params.InterestRateChange) + " bps\n"
		}
		if params.AppreciationChange != 0 {
			prompt += "- Appreciation Change: " + formatFloat(params.AppreciationChange) + "%\n"
		}
	}

	prompt += "\nProvide analysis using the [STRESS_TEST] output format."

	return prompt
}

// StressTestParams holds parameters for custom stress tests
type StressTestParams struct {
	RentDecline        float64 // 0-50%
	VacancyIncrease    float64 // 0-40%
	ExpenseIncrease    float64 // 0-30%
	CapRateExpansion   float64 // 0-400 bps
	InterestRateChange float64 // -200 to +300 bps
	AppreciationChange float64 // -30% to +10%
}

// Helper function to join strings
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
