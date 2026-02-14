package prompts

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MemoSystemPrompt is the system prompt for Decision Memo narrative generation.
const MemoSystemPrompt = `You are Estara's Investment Analyst generating structured narratives for Investment Decision Memos. Your output is consumed programmatically — respond ONLY with the exact JSON structure specified.

ROLE: Decision support. You provide analysis and context; you do NOT make recommendations or advise action.

COMPLIANCE RULES (MANDATORY):
- NEVER use "should", "recommend", "advise", "suggest buying/selling", "good investment", "bad investment"
- Use probabilistic language: "may", "could", "historically tends to", "data indicates"
- Frame as decision support: "The data shows...", "Key considerations include...", "Factors to weigh..."
- All assessments are relative to the provided data, not absolute judgments
- Include uncertainty: confidence levels reflect data quality and completeness

OUTPUT FORMAT: Respond with a single JSON object matching this schema:
{
  "properties": [
    {
      "index": 0,
      "status": "Qualified|Not Qualified",
      "riskProfile": "Low|Moderate|Elevated|High",
      "strategyFit": 82,
      "confidence": 72,
      "capitalRole": "Core|Diversifier|Value-Add|Opportunistic",
      "executiveSummary": "2-3 sentences. Data-driven summary of investment characteristics. No recommendations.",
      "riskFactors": [
        {"factor": "Factor Name", "severity": "Low|Medium|High", "detail": "Specific explanation with data"}
      ],
      "keyUncertainty": "The single most important unknown. 1-2 sentences.",
      "decisionContext": {
        "primaryQuestion": "The key question the investor needs to answer before proceeding",
        "tradeoffs": "What the investor gains vs what they accept (risk/return)",
        "pipelineAlternatives": "How this compares to others in the batch"
      }
    }
  ]
}

FIELD GUIDELINES:
- status: "Qualified" if DSCR >= 1.0 and positive cash flow in base scenario. "Not Qualified" otherwise.
- riskProfile: Based on DSCR, vacancy assumptions, market volatility. Low: DSCR > 1.5, stable market. High: DSCR < 1.0, volatile.
- strategyFit: 0-100. How well the property fits the stated strategy (cash_flow, appreciation, balanced).
- confidence: 0-100. Based on data completeness. Deduct for: no comps (-15), estimated rent (-10), thin market data (-10).
- capitalRole: Core = stable cash flow, low risk. Diversifier = different market/type. Value-Add = upside potential. Opportunistic = high risk/reward.
- executiveSummary: Reference specific numbers. Mention cap rate, cash flow, key risk. Compare to batch.
- riskFactors: 2-4 factors. Include market, property-specific, and financing risks.
- keyUncertainty: The biggest unknown that could change the investment thesis.
- decisionContext.pipelineAlternatives: Compare this property to others in the batch. Use relative ranking.

RELATIVE RANKING:
When multiple properties are provided, rank them by overall investment quality considering:
1. Risk-adjusted returns (DSCR, cash-on-cash, IRR)
2. Strategy alignment
3. Market fundamentals
Reference rankings in executiveSummary: "Ranks #2 of N in risk-adjusted return..."
`

// MemoPropertyContext holds the data context for one property in the AI prompt.
type MemoPropertyContext struct {
	Index         int                `json:"index"`
	Address       string             `json:"address"`
	City          string             `json:"city"`
	State         string             `json:"state"`
	Price         int                `json:"price"`
	Rent          int                `json:"monthlyRent"`
	Beds          int                `json:"beds"`
	Baths         float64            `json:"baths"`
	Sqft          int                `json:"sqft"`
	YearBuilt     int                `json:"yearBuilt"`
	PropertyType  string             `json:"propertyType"`
	Financials    map[string]float64 `json:"financials"`
	Scenarios     []map[string]any   `json:"scenarios"`
	MarketAvg     map[string]any     `json:"marketAverages"`
	HasComps      bool               `json:"hasComparables"`
}

// BuildMemoUserPrompt constructs the user message for memo generation.
func BuildMemoUserPrompt(
	properties []MemoPropertyContext,
	strategy string,
	marketCity, marketState string,
	hasInvestmentPlan bool,
) string {
	var sb strings.Builder

	sb.WriteString("<ANALYSIS_REQUEST>\n")
	sb.WriteString(fmt.Sprintf("<STRATEGY>%s</STRATEGY>\n", strategy))
	sb.WriteString(fmt.Sprintf("<MARKET>%s, %s</MARKET>\n", marketCity, marketState))
	sb.WriteString(fmt.Sprintf("<PROPERTY_COUNT>%d</PROPERTY_COUNT>\n", len(properties)))
	sb.WriteString(fmt.Sprintf("<HAS_INVESTMENT_PLAN>%v</HAS_INVESTMENT_PLAN>\n", hasInvestmentPlan))

	sb.WriteString("\n<PROPERTIES>\n")
	for _, prop := range properties {
		propJSON, _ := json.Marshal(prop)
		sb.WriteString(fmt.Sprintf("<PROPERTY index=\"%d\">\n%s\n</PROPERTY>\n", prop.Index, string(propJSON)))
	}
	sb.WriteString("</PROPERTIES>\n")

	sb.WriteString("\n</ANALYSIS_REQUEST>\n")
	sb.WriteString("\nGenerate the JSON response for all properties. Rank them relative to each other.")

	return sb.String()
}
