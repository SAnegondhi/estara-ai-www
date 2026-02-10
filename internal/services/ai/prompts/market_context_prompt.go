package prompts

import "fmt"

// MarketContextSystemPrompt is the system prompt for Stage 1 context enrichment (ADR-074)
const MarketContextSystemPrompt = `You are a real estate market research assistant with access to web search.

RULES:
- Provide ONLY factual, currently-verifiable information
- Use web search to verify specific data points (tax rates, regulations, credit ratings)
- If you cannot verify a specific detail via web search, mark it as "UNVERIFIED — user should confirm"
- Do NOT fabricate statute numbers, tax rates, or policy details
- When you find information via web search, tag it as "SOURCED — [url]"
- Format output as structured key-value pairs
- No narrative. No opinions. No recommendations.`

// BuildMarketContextUserPrompt creates the Stage 1 user prompt for tax/regulatory/insurance/fiscal context
func BuildMarketContextUserPrompt(city, state string) string {
	return fmt.Sprintf(`For the real estate investment market: %s, %s

Use web search to verify specific data points where possible. Provide factual
information for these 4 categories:

1. PROPERTY_TAX_SYSTEM:
   - Assessment methodology (market value, fractional, classified, etc.)
   - Assessment ratio(s) by property class (residential, commercial, multifamily)
   - Reassessment cycle (annual, biennial, triennial, etc.)
   - Next reassessment year for this jurisdiction
   - State equalization multiplier (if applicable)
   - Known recent assessment or tax bill changes (last 2 years)
   - Homestead exemptions and whether they apply to investor-owned property
   - Appeal process characteristics (ease, typical outcomes)
   - Any tax shift dynamics (e.g., commercial-to-residential burden shifts)
   - Effective tax rate range if known (as %% of market value)

2. REGULATORY_ENVIRONMENT:
   - State rent control status (banned / allowed / in effect)
   - Local rent stabilization or rent control ordinances (if any)
   - Eviction process: judicial vs non-judicial, typical timeline
   - Just cause eviction requirements (if any)
   - Landlord licensing or rental registration requirements
   - Landlord-tenant act name and key provisions affecting investors
   - Security deposit limits and rules
   - Required disclosures
   - Any pending legislation that could affect rental property investors
   - Short-term rental (Airbnb) regulations

3. INSURANCE_CONTEXT:
   - Primary climate/disaster exposures: flood, hurricane, tornado, wildfire, earthquake, hail, severe winter
   - FEMA flood zone prevalence in the metro
   - Predominant building construction type (frame, masonry, mixed)
   - Age of housing stock and how it affects premiums
   - Any state-level insurance market issues (carrier withdrawals, last-resort pools, rate approval processes)
   - Known premium trend direction (stable, rising, accelerating)
   - Any disaster declarations in last 3 years affecting this market

4. FISCAL_CONTEXT:
   - State credit rating (Moody's / S&P / Fitch)
   - Municipal/county credit rating if known
   - Pension funding ratio (state level)
   - Known fiscal pressures (unfunded liabilities, structural deficits)
   - Tax increment financing (TIF) districts — prevalence and impact
   - Any recent or proposed tax policy changes
   - Home rule status of primary municipality
   - Bond ratings trend (stable / negative outlook / improving)

Format each category as:
CATEGORY_NAME:
  key: value
  key: value
  ...

Mark verified items as: key: SOURCED — [url] value
Mark uncertain items as: key: UNVERIFIED — [brief reason]`, city, state)
}
