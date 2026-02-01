package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/ai/compliance"
	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/cache"
)

// ChatRequest represents a request to the evaluation chat agent
type ChatRequest struct {
	SessionID       string                    `json:"session_id,omitempty"`
	UserID          string                    `json:"user_id"`
	Message         string                    `json:"message"`
	Properties      []prompts.PropertyContext `json:"properties"`
	Portfolio       *prompts.PortfolioContext `json:"portfolio,omitempty"`
	InvestorProfile *prompts.InvestorProfile  `json:"investor_profile,omitempty"`
	MarketContext   string                    `json:"market_context,omitempty"` // Market expense characteristics
	History         []ChatMessage             `json:"history,omitempty"`
}

// ChatMessage represents a message in the chat history
type ChatMessage struct {
	Role      string      `json:"role"` // "user" or "assistant"
	Content   string      `json:"content"`
	Blocks    []ChatBlock `json:"blocks,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// ChatBlock represents a structured block in a chat response
type ChatBlock struct {
	Type    string                 `json:"type"` // "insight", "stress_test", "metrics", "comparison", "disclaimer", "text"
	Content map[string]interface{} `json:"content"`
	Raw     string                 `json:"raw,omitempty"`
}

// ChatResponse represents the response from the evaluation chat agent
type ChatResponse struct {
	SessionID   string      `json:"session_id"`
	Message     string      `json:"message"`
	Blocks      []ChatBlock `json:"blocks"`
	TokenUsage  *TokenUsage `json:"token_usage,omitempty"`
	GeneratedAt time.Time   `json:"generated_at"`
}

// TokenUsage represents token usage information
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ChatEvent represents a streaming event during chat
type ChatEvent struct {
	Type      string      `json:"type"` // "text", "insight", "stress_test", "metrics", "comparison", "disclaimer", "complete", "error"
	Content   interface{} `json:"content,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// EvaluationChatAgent handles property evaluation chat
type EvaluationChatAgent struct {
	client     *anthropic.Client
	cache      *cache.HybridCache
	compliance *compliance.Filter
	logger     *slog.Logger
}

// NewEvaluationChatAgent creates a new evaluation chat agent
func NewEvaluationChatAgent(
	client *anthropic.Client,
	cache *cache.HybridCache,
	complianceFilter *compliance.Filter,
) *EvaluationChatAgent {
	return &EvaluationChatAgent{
		client:     client,
		cache:      cache,
		compliance: complianceFilter,
		logger:     slog.Default().With("component", "evaluation_chat_agent"),
	}
}

// Chat processes a chat message and returns structured response
func (a *EvaluationChatAgent) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	startTime := time.Now()

	// Build context
	propertiesContext := prompts.BuildPropertyContext(req.Properties)
	portfolioContext := prompts.BuildPortfolioContext(req.Portfolio)

	// Build messages for multi-turn conversation
	messages := a.buildMessages(req, propertiesContext, portfolioContext)

	// Send to Claude
	response, err := a.client.CompleteWithMessages(ctx, prompts.EvaluationChatSystemPrompt, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to get response: %w", err)
	}

	// Apply compliance filter
	filteredResponse, violations := a.compliance.FilterContent(response)
	if len(violations) > 0 {
		a.logger.Warn("compliance violations detected and filtered",
			"violations", len(violations),
		)
	}

	// Ensure disclaimer is present
	if !hasComplianceDisclaimer(filteredResponse) {
		filteredResponse = a.compliance.AddDisclaimer(filteredResponse)
	}

	// Parse structured blocks
	blocks := parseBlocks(filteredResponse)

	// Generate session ID if not provided
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = generateSessionID()
	}

	result := &ChatResponse{
		SessionID:   sessionID,
		Message:     filteredResponse,
		Blocks:      blocks,
		GeneratedAt: time.Now(),
	}

	a.logger.Info("chat completed",
		"session_id", sessionID,
		"duration", time.Since(startTime),
		"blocks", len(blocks),
	)

	return result, nil
}

// StreamingBlockParser incrementally parses blocks from streaming text (www_v1 parity)
type StreamingBlockParser struct {
	buffer          string
	completedBlocks []ChatBlock
}

// blockPattern defines a block type with its markers
type blockPattern struct {
	blockType string
	start     string
	end       string
}

var blockPatterns = []blockPattern{
	{blockType: "insight", start: "[INSIGHT]", end: "[/INSIGHT]"},
	{blockType: "stress_test", start: "[STRESS_TEST]", end: "[/STRESS_TEST]"},
	{blockType: "metrics", start: "[METRICS]", end: "[/METRICS]"},
	{blockType: "comparison", start: "[COMPARISON]", end: "[/COMPARISON]"},
	{blockType: "disclaimer", start: "[DISCLAIMER]", end: "[/DISCLAIMER]"},
}

// AddChunk adds a chunk to the buffer and extracts complete blocks
func (p *StreamingBlockParser) AddChunk(chunk string) (newBlocks []ChatBlock, partialText string) {
	p.buffer += chunk

	// Try to extract complete blocks
	foundBlock := true
	for foundBlock {
		foundBlock = false

		for _, pattern := range blockPatterns {
			startIdx := strings.Index(p.buffer, pattern.start)
			if startIdx == -1 {
				continue
			}

			endIdx := strings.Index(p.buffer[startIdx:], pattern.end)
			if endIdx == -1 {
				continue
			}
			endIdx += startIdx // Adjust to absolute position

			// Extract text before block (if any)
			if startIdx > 0 {
				textBefore := strings.TrimSpace(p.buffer[:startIdx])
				if len(textBefore) > 0 {
					newBlocks = append(newBlocks, ChatBlock{
						Type:    "text",
						Content: map[string]interface{}{"text": textBefore},
						Raw:     textBefore,
					})
				}
			}

			// Extract block content
			blockContent := strings.TrimSpace(p.buffer[startIdx+len(pattern.start) : endIdx])
			rawBlock := p.buffer[startIdx : endIdx+len(pattern.end)]

			// Parse and add block with raw content
			block := parseBlock(pattern.blockType, blockContent)
			block.Raw = rawBlock // Use full raw block including markers
			newBlocks = append(newBlocks, block)

			// Update buffer - remove processed content
			p.buffer = p.buffer[endIdx+len(pattern.end):]
			foundBlock = true
			break
		}
	}

	p.completedBlocks = append(p.completedBlocks, newBlocks...)

	// Calculate partial text (text safe to emit)
	// Find earliest potential block start marker
	minBlockStart := -1
	for _, pattern := range blockPatterns {
		idx := strings.Index(p.buffer, pattern.start)
		if idx != -1 && (minBlockStart == -1 || idx < minBlockStart) {
			minBlockStart = idx
		}
	}

	if minBlockStart > 0 {
		// Text before next block marker is safe
		partialText = p.buffer[:minBlockStart]
	} else if minBlockStart == -1 {
		// No block markers found - check for partial marker starts
		markerStarts := []string{"[IN", "[ST", "[ME", "[DI", "[CO", "["}
		endsWithPartialMarker := false
		for _, start := range markerStarts {
			if strings.HasSuffix(p.buffer, start) {
				endsWithPartialMarker = true
				partialText = p.buffer[:len(p.buffer)-len(start)]
				break
			}
		}
		if !endsWithPartialMarker {
			partialText = p.buffer
		}
	}

	return newBlocks, partialText
}

// Finalize returns all blocks, treating remaining buffer as text
func (p *StreamingBlockParser) Finalize() []ChatBlock {
	if trimmed := strings.TrimSpace(p.buffer); len(trimmed) > 0 {
		p.completedBlocks = append(p.completedBlocks, ChatBlock{
			Type:    "text",
			Content: map[string]interface{}{"text": trimmed},
			Raw:     trimmed,
		})
	}
	return p.completedBlocks
}

// GetBlocks returns all completed blocks
func (p *StreamingBlockParser) GetBlocks() []ChatBlock {
	return p.completedBlocks
}

// Stream processes chat with real-time streaming (www_v1 parity)
func (a *EvaluationChatAgent) Stream(ctx context.Context, req ChatRequest, events chan<- ChatEvent) error {
	defer close(events)

	// Build context
	propertiesContext := prompts.BuildPropertyContext(req.Properties)
	portfolioContext := prompts.BuildPortfolioContext(req.Portfolio)

	// Build user prompt (including market context with expense characteristics)
	userPrompt := prompts.BuildEvaluationUserPrompt(prompts.EvaluationPromptParams{
		PropertiesContext: propertiesContext,
		PortfolioContext:  portfolioContext,
		InvestorProfile:   req.InvestorProfile,
		MarketContext:     req.MarketContext,
		UserMessage:       req.Message,
	})

	// For streaming, we need to handle the full conversation with context
	var fullPrompt string
	if len(req.History) > 0 {
		// Include history context
		fullPrompt = "Previous conversation:\n"
		for _, msg := range req.History {
			fullPrompt += fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content)
		}
		fullPrompt += "Current context and question:\n" + userPrompt
	} else {
		fullPrompt = userPrompt
	}

	// Start streaming
	streamEvents, err := a.client.Stream(ctx, prompts.EvaluationChatSystemPrompt, fullPrompt)
	if err != nil {
		sendChatEvent(events, ChatEvent{
			Type:  "error",
			Error: err.Error(),
		})
		return err
	}

	// Initialize streaming block parser (www_v1 parity)
	parser := &StreamingBlockParser{}
	var fullResponse strings.Builder
	var lastEmittedText string

	for event := range streamEvents {
		switch event.Type {
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				text := event.Delta.Text
				fullResponse.WriteString(text)

				// Add chunk to parser and get results
				newBlocks, partialText := parser.AddChunk(text)

				// Emit each new complete block immediately
				for _, block := range newBlocks {
					if block.Type != "text" {
						// Emit structured block
						sendChatEvent(events, ChatEvent{
							Type: block.Type,
							Content: map[string]interface{}{
								"content": block.Content,
								"raw":     block.Raw,
							},
						})
					}
				}

				// Emit partial text (text that's safe to display)
				// Only emit if there's new text to show
				if len(partialText) > len(lastEmittedText) {
					newText := partialText[len(lastEmittedText):]
					if len(newText) > 0 {
						sendChatEvent(events, ChatEvent{
							Type:    "text",
							Content: newText,
						})
						lastEmittedText = partialText
					}
				}
			}

		case "error":
			if event.Error != nil {
				sendChatEvent(events, ChatEvent{
					Type:  "error",
					Error: event.Error.Message,
				})
				return fmt.Errorf("stream error: %s", event.Error.Message)
			}
		}
	}

	// Finalize parser to get all blocks including remaining text
	allBlocks := parser.Finalize()

	// Apply compliance filter to full response
	response := fullResponse.String()
	filteredResponse, _ := a.compliance.FilterContent(response)

	// Generate session ID
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = generateSessionID()
	}

	// Send complete event with all parsed blocks
	sendChatEvent(events, ChatEvent{
		Type:      "complete",
		SessionID: sessionID,
		Content: map[string]interface{}{
			"content":      filteredResponse,
			"parsedBlocks": allBlocks,
		},
	})

	return nil
}

// buildMessages constructs the message array for multi-turn conversation
func (a *EvaluationChatAgent) buildMessages(req ChatRequest, propertiesContext, portfolioContext string) []anthropic.Message {
	messages := make([]anthropic.Message, 0)

	// Add history messages
	for _, msg := range req.History {
		messages = append(messages, anthropic.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Build current user message with context (including market expense characteristics)
	userPrompt := prompts.BuildEvaluationUserPrompt(prompts.EvaluationPromptParams{
		PropertiesContext: propertiesContext,
		PortfolioContext:  portfolioContext,
		InvestorProfile:   req.InvestorProfile,
		MarketContext:     req.MarketContext,
		UserMessage:       req.Message,
	})

	messages = append(messages, anthropic.Message{
		Role:    "user",
		Content: userPrompt,
	})

	return messages
}

// parseBlocks extracts structured blocks from the response
func parseBlocks(response string) []ChatBlock {
	blocks := make([]ChatBlock, 0)

	// Define block patterns
	blockPatterns := map[string]*regexp.Regexp{
		"insight":     regexp.MustCompile(`(?s)\[INSIGHT\](.*?)\[/INSIGHT\]`),
		"stress_test": regexp.MustCompile(`(?s)\[STRESS_TEST\](.*?)\[/STRESS_TEST\]`),
		"metrics":     regexp.MustCompile(`(?s)\[METRICS\](.*?)\[/METRICS\]`),
		"comparison":  regexp.MustCompile(`(?s)\[COMPARISON\](.*?)\[/COMPARISON\]`),
		"disclaimer":  regexp.MustCompile(`(?s)\[DISCLAIMER\](.*?)\[/DISCLAIMER\]`),
	}

	for blockType, pattern := range blockPatterns {
		matches := pattern.FindAllStringSubmatch(response, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				block := parseBlock(blockType, match[1])
				blocks = append(blocks, block)
			}
		}
	}

	return blocks
}

// parseBlock parses a single block's content
func parseBlock(blockType, content string) ChatBlock {
	block := ChatBlock{
		Type:    blockType,
		Content: make(map[string]interface{}),
		Raw:     strings.TrimSpace(content),
	}

	switch blockType {
	case "insight":
		block.Content = parseInsightBlock(content)
	case "stress_test":
		block.Content = parseStressTestBlock(content)
	case "metrics":
		block.Content = parseMetricsBlock(content)
	case "comparison":
		block.Content = parseComparisonBlock(content)
	case "disclaimer":
		block.Content = map[string]interface{}{
			"text": strings.TrimSpace(content),
		}
	}

	return block
}

// parseInsightBlock parses an insight block
func parseInsightBlock(content string) map[string]interface{} {
	result := make(map[string]interface{})

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Title:") {
			result["title"] = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
		} else if strings.HasPrefix(line, "Type:") {
			result["type"] = strings.TrimSpace(strings.TrimPrefix(line, "Type:"))
		} else if strings.HasPrefix(line, "Confidence:") {
			result["confidence"] = strings.TrimSpace(strings.TrimPrefix(line, "Confidence:"))
		} else if strings.HasPrefix(line, "Summary:") {
			result["summary"] = strings.TrimSpace(strings.TrimPrefix(line, "Summary:"))
		} else if strings.HasPrefix(line, "Details:") {
			result["details"] = strings.TrimSpace(strings.TrimPrefix(line, "Details:"))
		}
	}

	return result
}

// parseStressTestBlock parses a stress test block
// Fields use camelCase to match client expectations (www_v1 parity)
func parseStressTestBlock(content string) map[string]interface{} {
	result := make(map[string]interface{})

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Scenario:") {
			result["scenario"] = strings.TrimSpace(strings.TrimPrefix(line, "Scenario:"))
		} else if strings.HasPrefix(line, "Name:") {
			result["name"] = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		} else if strings.HasPrefix(line, "PropertyAddress:") {
			result["propertyAddress"] = strings.TrimSpace(strings.TrimPrefix(line, "PropertyAddress:"))
		} else if strings.HasPrefix(line, "ValueImpact:") {
			result["valueImpact"] = strings.TrimSpace(strings.TrimPrefix(line, "ValueImpact:"))
		} else if strings.HasPrefix(line, "RentImpact:") {
			result["rentImpact"] = strings.TrimSpace(strings.TrimPrefix(line, "RentImpact:"))
		} else if strings.HasPrefix(line, "CashFlowImpact:") {
			result["cashFlowImpact"] = strings.TrimSpace(strings.TrimPrefix(line, "CashFlowImpact:"))
		} else if strings.HasPrefix(line, "OccupancyImpact:") {
			result["occupancyImpact"] = strings.TrimSpace(strings.TrimPrefix(line, "OccupancyImpact:"))
		} else if strings.HasPrefix(line, "DscrUnderStress:") {
			result["dscrUnderStress"] = strings.TrimSpace(strings.TrimPrefix(line, "DscrUnderStress:"))
		} else if strings.HasPrefix(line, "BreakEvenOccupancy:") {
			result["breakEvenOccupancy"] = strings.TrimSpace(strings.TrimPrefix(line, "BreakEvenOccupancy:"))
		} else if strings.HasPrefix(line, "Narrative:") {
			result["narrative"] = strings.TrimSpace(strings.TrimPrefix(line, "Narrative:"))
		}
	}

	return result
}

// parseMetricsBlock parses a metrics table block
func parseMetricsBlock(content string) map[string]interface{} {
	result := make(map[string]interface{})
	metrics := make([]map[string]string, 0)

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "|") && !strings.Contains(line, "---") {
			parts := strings.Split(line, "|")
			if len(parts) >= 4 {
				metric := strings.TrimSpace(parts[1])
				value := strings.TrimSpace(parts[2])
				rating := strings.TrimSpace(parts[3])

				if metric != "" && metric != "Metric" {
					metrics = append(metrics, map[string]string{
						"metric": metric,
						"value":  value,
						"rating": rating,
					})
				}
			}
		}
	}

	result["rows"] = metrics // Client expects "rows", not "metrics" (www_v1 parity)
	return result
}

// parseComparisonBlock parses a comparison block
func parseComparisonBlock(content string) map[string]interface{} {
	result := make(map[string]interface{})

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Property:") {
			result["property"] = strings.TrimSpace(strings.TrimPrefix(line, "Property:"))
		} else if strings.HasPrefix(line, "- Cap Rate:") {
			result["cap_rate_diff"] = strings.TrimSpace(strings.TrimPrefix(line, "- Cap Rate:"))
		} else if strings.HasPrefix(line, "- Cash Flow:") {
			result["cash_flow_diff"] = strings.TrimSpace(strings.TrimPrefix(line, "- Cash Flow:"))
		} else if strings.HasPrefix(line, "- Risk Level:") {
			result["risk_level"] = strings.TrimSpace(strings.TrimPrefix(line, "- Risk Level:"))
		}
	}

	return result
}

// hasComplianceDisclaimer checks if the response contains a compliance disclaimer
func hasComplianceDisclaimer(content string) bool {
	indicators := []string{
		"[DISCLAIMER]",
		"not investment advice",
		"informational purposes only",
		"consult with",
	}

	lowerContent := strings.ToLower(content)
	for _, indicator := range indicators {
		if strings.Contains(lowerContent, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}

// generateSessionID creates a unique session ID
func generateSessionID() string {
	return "chat_" + time.Now().Format("20060102150405") + "_" + randomStr(8)
}

// randomStr generates a random string
func randomStr(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

// sendChatEvent sends an event to the channel
func sendChatEvent(events chan<- ChatEvent, event ChatEvent) {
	select {
	case events <- event:
	default:
		// Channel full, skip
	}
}

// SerializeMessage serializes a chat message for storage
func SerializeMessage(msg ChatMessage) ([]byte, error) {
	return json.Marshal(msg)
}

// DeserializeMessage deserializes a chat message from storage
func DeserializeMessage(data []byte) (*ChatMessage, error) {
	var msg ChatMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
