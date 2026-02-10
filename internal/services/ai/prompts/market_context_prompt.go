package prompts

import "fmt"

// MarketContextSystemPrompt is the system prompt for Stage 1 context enrichment (ADR-074)
const MarketContextSystemPrompt = `You are a real estate market research assistant with access to web search.

RULES:
- Provide ONLY factual, currently-verifiable information
- Use web search to verify specific data points (tax rates, regulations, insurance, permits)
- If you cannot verify a specific detail via web search, mark it as "UNVERIFIED — user should confirm"
- Do NOT fabricate statute numbers, tax rates, or policy details
- When you find information via web search, tag it as "SOURCED — [url]"
- Format output as structured key-value pairs
- No narrative. No opinions. No recommendations.
- PRIORITY items MUST be web-searched. Do NOT skip them or mark as UNVERIFIED without attempting a search.`

// BuildMarketContextUserPrompt creates the Stage 1 user prompt for market context enrichment.
// The prompt now includes 6 categories (up from 4) with explicit PRIORITY markers on
// critical numeric data that investors need for underwriting decisions.
func BuildMarketContextUserPrompt(city, state string) string {
	return fmt.Sprintf(`For the real estate investment market: %s, %s

Use web search to verify specific data points. You have up to 12 web searches — use them
strategically on PRIORITY items first, then fill in remaining categories.

Items marked [PRIORITY] MUST be web-searched. Do not skip them.

1. PROPERTY_TAX_SYSTEM:
   - [PRIORITY] Effective property tax rate for this jurisdiction (as %% of market value) — search for "%s %s property tax rate"
   - [PRIORITY] Assessment methodology (market value, fractional, classified, etc.)
   - Assessment ratio(s) by property class (residential, commercial, multifamily)
   - Reassessment cycle (annual, biennial, triennial, etc.)
   - Homestead exemptions and whether they apply to investor-owned property
   - Appeal process characteristics (ease, typical outcomes)
   - Known recent assessment or tax bill changes (last 2 years)

2. REGULATORY_ENVIRONMENT:
   - State rent control status (banned / allowed / in effect)
   - Local rent stabilization or rent control ordinances (if any)
   - Eviction process: judicial vs non-judicial, typical timeline
   - Landlord licensing or rental registration requirements
   - Security deposit limits and rules
   - Short-term rental (Airbnb) regulations
   - Any pending legislation affecting rental property investors

3. INSURANCE_CONTEXT:
   - [PRIORITY] Average annual homeowners insurance premium for this market — search for "average home insurance cost %s %s"
   - [PRIORITY] Primary climate/disaster exposures (flood, hurricane, tornado, wildfire, hail, etc.)
   - FEMA flood zone prevalence in the metro
   - Any state-level insurance market issues (carrier withdrawals, rate increases)
   - Known premium trend direction (stable, rising, accelerating)
   - Any disaster declarations in last 3 years affecting this market

4. FISCAL_CONTEXT:
   - State credit rating (Moody's / S&P / Fitch)
   - Municipal/county credit rating if known
   - Pension funding ratio (state level)
   - Known fiscal pressures (unfunded liabilities, structural deficits)
   - Bond ratings trend (stable / negative outlook / improving)

5. ECONOMIC_INDICATORS:
   - [PRIORITY] Current unemployment rate for the metro area or county — search for "%s %s unemployment rate"
   - [PRIORITY] Major employers and any recent major employer relocations/expansions/closures
   - Population growth trend (growing/stable/declining) and approximate rate
   - Job market characterization (tech-heavy, diversified, government, etc.)

6. SUPPLY_PIPELINE:
   - [PRIORITY] Residential building permits issued recently — search for "%s %s building permits" or "%s %s new construction"
   - Major planned developments or master-planned communities
   - Any construction moratoriums or growth boundaries
   - New housing starts trend (increasing/stable/declining)

Format each category as:
CATEGORY_NAME:
  key: value
  key: value
  ...

Mark verified items as: key: SOURCED — [url] value
Mark uncertain items as: key: UNVERIFIED — [brief reason]`,
		city, state,
		city, state, // property tax search
		city, state, // insurance search
		city, state, // unemployment search
		city, state, city, state, // building permits search
	)
}
