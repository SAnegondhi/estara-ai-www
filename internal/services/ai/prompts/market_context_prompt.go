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
// The prompt includes 8 categories with explicit PRIORITY markers on critical numeric data
// that investors need for underwriting decisions (ADR-111 Phase 1: added categories 7 and 8,
// improved search strategies for bond ratings and STR ordinances, increased searches to 18).
func BuildMarketContextUserPrompt(city, state string) string {
	return fmt.Sprintf(`For the real estate investment market: %s, %s

Use web search to verify specific data points. You have up to 18 web searches — use them
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
   - [PRIORITY] STR ordinance — search in this order: (1) "%s short-term rental ordinance 2024 2025", (2) "site:municode.com %s short-term rental", (3) "%s Airbnb regulations permit license"
   - STR specifics if found: registration required Y/N, owner-occupancy requirement Y/N, density cap per block/building, prohibited zones, nightly/annual night cap, penalty amount
   - [PRIORITY] State rent control preemption — search "%s state rent control preemption law investor"
   - Local rent stabilization or rent control ordinance currently in effect (city AND county level)
   - Eviction process: judicial (court order required) or non-judicial; typical timeline in months
   - Landlord licensing or rental registration requirements
   - Security deposit limits and rules
   - Any pending legislation affecting rental property investors

3. INSURANCE_CONTEXT:
   - [PRIORITY] Average annual homeowners insurance premium for this market — search for "average home insurance cost %s %s"
   - [PRIORITY] Primary climate/disaster exposures (flood, hurricane, tornado, wildfire, hail, etc.)
   - FEMA flood zone prevalence in the metro
   - Any state-level insurance market issues (carrier withdrawals, rate increases)
   - Known premium trend direction (stable, rising, accelerating)
   - Any disaster declarations in last 3 years affecting this market

4. FISCAL_CONTEXT:
   - [PRIORITY] Municipal/city bond rating — search in this order: (1) "site:emma.msrb.org %s %s bond rating", (2) "%s %s municipal bond rating Moody's OR S&P OR Fitch 2024 2025"
   - [PRIORITY] State credit rating — search "%s state credit rating Moody's S&P Fitch 2024 2025"
   - Rating outlook for city and state (stable / positive watch / negative watch)
   - [PRIORITY] Pension funding ratio — search "%s state pension funded ratio GASB 2024"
   - Known fiscal pressures (structural deficits, unfunded liabilities, recent downgrades)
   - Bond ratings trend (stable / improving / deteriorating)

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

7. LANDLORD_TENANT_LAW:
   - [PRIORITY] Just cause eviction requirement — search "%s %s just cause eviction law landlord investor"
   - Does this state or city require just cause for eviction of month-to-month tenants? Y/N
   - Relocation assistance required: when triggered (no-fault, renovation, owner move-in, redevelopment), dollar amount if specified
   - Owner move-in (OMI) eviction: allowed Y/N, notice period required (days), per-building frequency limit
   - Source-of-income protection: prohibited from rejecting Section 8 / housing voucher holders Y/N
   - Required lease disclosures specific to this state
   - Security deposit: maximum limit (months of rent), interest-bearing requirement Y/N, return timeline (days)

8. REAL_ESTATE_INVESTMENT_RESTRICTIONS:
   - [PRIORITY] Pending state legislation affecting investors — search "%s 2025 landlord legislation rent control eviction investor"
   - Corporate or institutional landlord restrictions (any state bills limiting bulk investor purchases of SFH?)
   - Anti-algorithmic rent pricing legislation pending in this state (RealPage-type bills)
   - Condo conversion restrictions relevant to multifamily-to-condo strategy
   - Owner-occupancy requirements that restrict investor purchase (specific zones, HOA rules)

Format each category as:
CATEGORY_NAME:
  key: value
  key: value
  ...

Mark verified items as: key: SOURCED — [url] value
Mark uncertain items as: key: UNVERIFIED — [brief reason]`,
		city, state,
		city, state,       // property tax search
		city, city, city,  // STR ordinance searches (3 search strings)
		state,             // rent control preemption search
		city, state,       // insurance search
		city, state,       // EMMA bond rating search
		city, state,       // municipal bond rating search
		state,             // state credit rating search
		state,             // pension funded ratio search
		city, state,       // unemployment search
		city, state, city, state, // building permits search
		city, state,       // just cause eviction search
		state,             // pending legislation search
	)
}
