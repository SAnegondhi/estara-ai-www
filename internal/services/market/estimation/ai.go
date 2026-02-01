package estimation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// AIEstimatedData represents market data estimated by AI
type AIEstimatedData struct {
	City            string    `json:"city"`
	State           string    `json:"state"`
	MedianHomePrice int       `json:"medianHomePrice"`
	MedianRent      int       `json:"medianRent"`
	CapRate         float64   `json:"capRate"`
	YearOverYearPct float64   `json:"yearOverYearPct"`
	Confidence      float64   `json:"confidence"`
	Reasoning       string    `json:"reasoning"`
	GeneratedAt     time.Time `json:"generatedAt"`
}

// AnthropicClient defines the interface for AI interactions
type AnthropicClient interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// AIEstimator uses AI to estimate market data when sources are unavailable
type AIEstimator struct {
	client AnthropicClient
	logger *slog.Logger
}

// NewAIEstimator creates a new AI estimator
func NewAIEstimator(client AnthropicClient) *AIEstimator {
	return &AIEstimator{
		client: client,
		logger: slog.Default().With("component", "ai_estimator"),
	}
}

const systemPrompt = `You are a real estate market data analyst. When asked about a city's real estate market, provide your best estimate based on your knowledge.

IMPORTANT: You must respond with ONLY a valid JSON object, no additional text or explanation.

The JSON must have this exact structure:
{
  "medianHomePrice": <integer in dollars>,
  "medianRent": <integer in dollars per month>,
  "capRate": <float, percentage between 3-12>,
  "yearOverYearPct": <float, percentage change, can be negative>,
  "confidence": <float between 0.3 and 0.9>,
  "reasoning": "<brief explanation of your estimate>"
}

Guidelines:
- Use your knowledge of the area's economy, population, and historical trends
- Cap rate = (NOI / Home Price) * 100, where NOI = Annual Rent - Operating Expenses (~40% of rent)
- Confidence should be lower for smaller or less-known cities
- Year-over-year should reflect typical market conditions
- Be conservative in estimates - it's better to underestimate than overestimate
- Include SEC-compliant language noting these are estimates, not guarantees`

const userPromptTemplate = `Estimate the current real estate market data for %s, %s.

Provide your best estimate for:
1. Median home price
2. Median monthly rent
3. Cap rate
4. Year-over-year price change

Remember to respond with ONLY the JSON object, no other text.`

// EstimateMarketData generates AI-estimated market data for a location
func (e *AIEstimator) EstimateMarketData(ctx context.Context, city, state string) (*AIEstimatedData, error) {
	if e.client == nil {
		return nil, fmt.Errorf("AI client not configured")
	}

	userPrompt := fmt.Sprintf(userPromptTemplate, city, state)

	e.logger.Info("requesting AI market estimation",
		"city", city,
		"state", state,
	)

	response, err := e.client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Parse the JSON response
	data, err := e.parseResponse(response, city, state)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	e.logger.Info("AI estimation complete",
		"city", city,
		"state", state,
		"medianHomePrice", data.MedianHomePrice,
		"confidence", data.Confidence,
	)

	return data, nil
}

// parseResponse extracts market data from AI response
func (e *AIEstimator) parseResponse(response, city, state string) (*AIEstimatedData, error) {
	// Clean up response - remove markdown code blocks if present
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	// Parse JSON
	var raw struct {
		MedianHomePrice int     `json:"medianHomePrice"`
		MedianRent      int     `json:"medianRent"`
		CapRate         float64 `json:"capRate"`
		YearOverYearPct float64 `json:"yearOverYearPct"`
		Confidence      float64 `json:"confidence"`
		Reasoning       string  `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(response), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Validate ranges
	data := &AIEstimatedData{
		City:            city,
		State:           state,
		MedianHomePrice: raw.MedianHomePrice,
		MedianRent:      raw.MedianRent,
		CapRate:         raw.CapRate,
		YearOverYearPct: raw.YearOverYearPct,
		Confidence:      raw.Confidence,
		Reasoning:       raw.Reasoning,
		GeneratedAt:     time.Now(),
	}

	// Validate and adjust ranges
	if data.MedianHomePrice < 50000 {
		data.MedianHomePrice = 50000 // Minimum reasonable price
		data.Confidence *= 0.8
	}
	if data.MedianHomePrice > 5000000 {
		data.MedianHomePrice = 5000000 // Maximum reasonable price
		data.Confidence *= 0.8
	}

	if data.MedianRent < 500 {
		data.MedianRent = 500
		data.Confidence *= 0.8
	}
	if data.MedianRent > 10000 {
		data.MedianRent = 10000
		data.Confidence *= 0.8
	}

	if data.CapRate < 2 {
		data.CapRate = 2
	}
	if data.CapRate > 15 {
		data.CapRate = 15
	}

	if data.YearOverYearPct < -30 {
		data.YearOverYearPct = -30
	}
	if data.YearOverYearPct > 30 {
		data.YearOverYearPct = 30
	}

	if data.Confidence < 0.3 {
		data.Confidence = 0.3
	}
	if data.Confidence > 0.9 {
		data.Confidence = 0.9 // AI estimates shouldn't have full confidence
	}

	return data, nil
}

// IsConfigured returns true if the AI estimator has a client
func (e *AIEstimator) IsConfigured() bool {
	return e.client != nil
}
