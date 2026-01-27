package compliance

import (
	"log/slog"
	"regexp"
	"strings"
)

// ViolationType represents the type of compliance violation
type ViolationType string

const (
	// ViolationTypeProhibitedPhrase indicates use of prohibited language
	ViolationTypeProhibitedPhrase ViolationType = "prohibited_phrase"
	// ViolationTypeGuarantee indicates guarantee language
	ViolationTypeGuarantee ViolationType = "guarantee"
	// ViolationTypeAdvice indicates investment advice language
	ViolationTypeAdvice ViolationType = "investment_advice"
	// ViolationTypeMissingDisclaimer indicates missing required disclaimer
	ViolationTypeMissingDisclaimer ViolationType = "missing_disclaimer"
	// ViolationTypeExaggeration indicates exaggerated claims
	ViolationTypeExaggeration ViolationType = "exaggeration"
)

// Violation represents a compliance violation found in content
type Violation struct {
	Type        ViolationType `json:"type"`
	Severity    string        `json:"severity"` // "high", "medium", "low"
	Description string        `json:"description"`
	Match       string        `json:"match,omitempty"`
	Position    int           `json:"position,omitempty"`
	Suggestion  string        `json:"suggestion,omitempty"`
}

// FilterResult represents the result of content filtering
type FilterResult struct {
	Original    string      `json:"original"`
	Filtered    string      `json:"filtered"`
	Violations  []Violation `json:"violations"`
	IsCompliant bool        `json:"is_compliant"`
	Score       float64     `json:"score"` // 0-1, higher is more compliant
}

// Filter provides SEC-compliant content filtering
type Filter struct {
	prohibitedPatterns  []*regexp.Regexp
	guaranteePatterns   []*regexp.Regexp
	advicePatterns      []*regexp.Regexp
	exaggerationPatterns []*regexp.Regexp
	replacements        map[string]string
	logger              *slog.Logger
}

// FilterConfig holds configuration for the filter
type FilterConfig struct {
	StrictMode bool // More aggressive filtering
}

// NewFilter creates a new compliance filter
func NewFilter(cfg FilterConfig) *Filter {
	f := &Filter{
		logger:       slog.Default().With("component", "compliance_filter"),
		replacements: defaultReplacements(),
	}

	f.prohibitedPatterns = compilePatterns(prohibitedPhrases)
	f.guaranteePatterns = compilePatterns(guaranteePhrases)
	f.advicePatterns = compilePatterns(advicePhrases)
	f.exaggerationPatterns = compilePatterns(exaggerationPhrases)

	return f
}

// FilterContent checks and sanitizes AI output
func (f *Filter) FilterContent(content string) (string, []Violation) {
	violations := make([]Violation, 0)
	filtered := content

	// Check for prohibited phrases
	for _, pattern := range f.prohibitedPatterns {
		if matches := pattern.FindAllStringIndex(content, -1); matches != nil {
			for _, match := range matches {
				matchStr := content[match[0]:match[1]]
				violations = append(violations, Violation{
					Type:        ViolationTypeProhibitedPhrase,
					Severity:    "high",
					Description: "Prohibited phrase detected",
					Match:       matchStr,
					Position:    match[0],
					Suggestion:  f.getSuggestion(matchStr),
				})
				filtered = strings.Replace(filtered, matchStr, f.getSuggestion(matchStr), 1)
			}
		}
	}

	// Check for guarantee language
	for _, pattern := range f.guaranteePatterns {
		if matches := pattern.FindAllStringIndex(content, -1); matches != nil {
			for _, match := range matches {
				matchStr := content[match[0]:match[1]]
				violations = append(violations, Violation{
					Type:        ViolationTypeGuarantee,
					Severity:    "high",
					Description: "Guarantee language detected",
					Match:       matchStr,
					Position:    match[0],
					Suggestion:  "Remove or rephrase guarantee language",
				})
				filtered = pattern.ReplaceAllString(filtered, "[potential]")
			}
		}
	}

	// Check for advice language
	for _, pattern := range f.advicePatterns {
		if matches := pattern.FindAllStringIndex(content, -1); matches != nil {
			for _, match := range matches {
				matchStr := content[match[0]:match[1]]
				violations = append(violations, Violation{
					Type:        ViolationTypeAdvice,
					Severity:    "medium",
					Description: "Investment advice language detected",
					Match:       matchStr,
					Position:    match[0],
					Suggestion:  f.getAdviceSuggestion(matchStr),
				})
				filtered = strings.Replace(filtered, matchStr, f.getAdviceSuggestion(matchStr), 1)
			}
		}
	}

	// Check for exaggeration
	for _, pattern := range f.exaggerationPatterns {
		if matches := pattern.FindAllStringIndex(content, -1); matches != nil {
			for _, match := range matches {
				matchStr := content[match[0]:match[1]]
				violations = append(violations, Violation{
					Type:        ViolationTypeExaggeration,
					Severity:    "low",
					Description: "Exaggerated claim detected",
					Match:       matchStr,
					Position:    match[0],
					Suggestion:  "Use more measured language",
				})
			}
		}
	}

	return filtered, violations
}

// ValidateReport ensures a report meets SEC guidelines
func (f *Filter) ValidateReport(report string) (bool, []string) {
	issues := make([]string, 0)

	// Check for required disclaimer
	if !hasDisclaimer(report) {
		issues = append(issues, "Missing required investment disclaimer")
	}

	// Check for forward-looking statement caveat
	if hasForwardLookingStatements(report) && !hasForwardLookingCaveat(report) {
		issues = append(issues, "Forward-looking statements require appropriate caveats")
	}

	// Check for prohibited content
	_, violations := f.FilterContent(report)
	for _, v := range violations {
		if v.Severity == "high" {
			issues = append(issues, v.Description+": "+v.Match)
		}
	}

	return len(issues) == 0, issues
}

// GetComplianceScore calculates a compliance score for content
func (f *Filter) GetComplianceScore(content string) float64 {
	_, violations := f.FilterContent(content)

	score := 1.0

	for _, v := range violations {
		switch v.Severity {
		case "high":
			score -= 0.3
		case "medium":
			score -= 0.15
		case "low":
			score -= 0.05
		}
	}

	// Check for positive compliance indicators
	if hasDisclaimer(content) {
		score += 0.1
	}
	if hasNeutralLanguage(content) {
		score += 0.05
	}

	// Clamp to 0-1 range
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return score
}

// AddDisclaimer adds a standard SEC-compliant disclaimer to content
func (f *Filter) AddDisclaimer(content string) string {
	if hasDisclaimer(content) {
		return content
	}

	return content + "\n\n" + StandardDisclaimer
}

// getSuggestion returns a compliant alternative for a prohibited phrase
func (f *Filter) getSuggestion(phrase string) string {
	lowerPhrase := strings.ToLower(phrase)
	if replacement, ok := f.replacements[lowerPhrase]; ok {
		return replacement
	}
	return "[analysis suggests]"
}

// getAdviceSuggestion returns a compliant alternative for advice language
func (f *Filter) getAdviceSuggestion(phrase string) string {
	lowerPhrase := strings.ToLower(phrase)

	adviceReplacements := map[string]string{
		"you should buy":           "data may support considering",
		"you should sell":          "data may support evaluating",
		"i recommend":              "analysis indicates",
		"i advise":                 "data suggests",
		"you must":                 "investors may want to consider",
		"you need to":              "it may be worth considering",
		"definitely":               "potentially",
		"certainly":                "possibly",
		"i suggest":                "analysis suggests",
		"my recommendation is":     "the analysis indicates",
		"the best investment is":   "one option to consider is",
		"you should invest in":     "data supports considering",
		"don't miss this":          "this may warrant attention",
		"act now":                  "timing may be relevant",
	}

	if replacement, ok := adviceReplacements[lowerPhrase]; ok {
		return replacement
	}

	return "analysis suggests"
}

// Helper functions

func compilePatterns(phrases []string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, len(phrases))
	for _, phrase := range phrases {
		pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(phrase))
		if err == nil {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

func defaultReplacements() map[string]string {
	return map[string]string{
		"you should buy":         "data may support considering",
		"you should sell":        "data may support evaluating",
		"i recommend":            "analysis indicates",
		"i advise":               "data suggests",
		"guaranteed return":      "potential return",
		"guaranteed profit":      "potential profit",
		"guaranteed income":      "potential income",
		"risk-free":              "reduced risk",
		"no risk":                "managed risk",
		"sure thing":             "opportunity",
		"can't lose":             "potential opportunity",
		"you will make money":    "potential returns exist",
		"you will profit":        "profits are possible",
		"safe investment":        "investment with considerations",
	}
}

func hasDisclaimer(content string) bool {
	disclaimerIndicators := []string{
		"not investment advice",
		"not financial advice",
		"consult with",
		"professional advice",
		"informational purposes only",
		"does not constitute",
		"past performance",
		"no guarantee",
	}

	lowerContent := strings.ToLower(content)
	for _, indicator := range disclaimerIndicators {
		if strings.Contains(lowerContent, indicator) {
			return true
		}
	}
	return false
}

func hasForwardLookingStatements(content string) bool {
	forwardIndicators := []string{
		"will increase",
		"will decrease",
		"expected to",
		"projected to",
		"forecast",
		"predict",
		"anticipate",
		"outlook",
		"future",
	}

	lowerContent := strings.ToLower(content)
	for _, indicator := range forwardIndicators {
		if strings.Contains(lowerContent, indicator) {
			return true
		}
	}
	return false
}

func hasForwardLookingCaveat(content string) bool {
	caveatIndicators := []string{
		"may",
		"might",
		"could",
		"subject to change",
		"no guarantee",
		"uncertainty",
		"risk",
	}

	lowerContent := strings.ToLower(content)
	count := 0
	for _, indicator := range caveatIndicators {
		if strings.Contains(lowerContent, indicator) {
			count++
		}
	}
	return count >= 2
}

func hasNeutralLanguage(content string) bool {
	neutralIndicators := []string{
		"data indicates",
		"analysis shows",
		"trends suggest",
		"metrics reveal",
		"information suggests",
		"based on the data",
		"according to",
	}

	lowerContent := strings.ToLower(content)
	count := 0
	for _, indicator := range neutralIndicators {
		if strings.Contains(lowerContent, indicator) {
			count++
		}
	}
	return count >= 2
}

// Phrase lists for compliance checking

var prohibitedPhrases = []string{
	"guaranteed return",
	"guaranteed profit",
	"guaranteed income",
	"guaranteed success",
	"risk-free investment",
	"no risk",
	"sure thing",
	"can't lose",
	"you will make money",
	"you will profit",
	"safe investment",
	"100% safe",
	"get rich",
	"double your money",
	"triple your money",
	"instant wealth",
	"passive income guaranteed",
}

var guaranteePhrases = []string{
	"guaranteed",
	"certainty",
	"certain to",
	"will definitely",
	"without fail",
	"foolproof",
	"no-fail",
	"fail-safe",
	"assured",
	"promise",
}

var advicePhrases = []string{
	"you should buy",
	"you should sell",
	"you should invest",
	"i recommend",
	"i advise",
	"i suggest you",
	"my recommendation is",
	"the best investment is",
	"you must",
	"you need to buy",
	"you need to sell",
	"don't miss this",
	"act now",
	"buy now",
	"sell now",
	"invest now",
}

var exaggerationPhrases = []string{
	"best investment ever",
	"once in a lifetime",
	"never before seen",
	"unprecedented opportunity",
	"amazing returns",
	"incredible profits",
	"unbelievable",
	"spectacular",
	"extraordinary",
	"phenomenal",
}

// StandardDisclaimer is the standard SEC-compliant disclaimer
const StandardDisclaimer = `DISCLAIMER: This analysis is provided for informational purposes only and does not constitute investment advice, financial advice, or any other type of professional advice. Past performance does not guarantee future results. All investments involve risk, including the potential loss of principal. The information presented is based on data believed to be reliable, but no guarantee is made as to its accuracy or completeness. Before making any investment decisions, please consult with qualified financial, legal, and tax professionals who can provide advice tailored to your specific situation and objectives.`

// ShortDisclaimer is a shorter version for inline use
const ShortDisclaimer = `This analysis is for informational purposes only and does not constitute investment advice. All investments involve risk. Consult with qualified professionals before making investment decisions.`
