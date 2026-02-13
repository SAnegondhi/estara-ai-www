package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://api.anthropic.com/v1"
	defaultModel       = "claude-sonnet-4-20250514"
	defaultMaxTokens   = 4096
	defaultTimeout     = 120 * time.Second
	anthropicVersion   = "2023-06-01"
)

// Client is an Anthropic API client
type Client struct {
	apiKey     string
	baseURL    string
	model      string
	maxTokens  int
	httpClient *http.Client
	logger     *slog.Logger
	onAPIError func(APIErrorInfo)
}

// APIErrorInfo contains information about an API error for alerting
type APIErrorInfo struct {
	StatusCode int
	ErrorType  string
	Message    string
}

// IsBillingError returns true if this is a billing/credit error
func (e APIErrorInfo) IsBillingError() bool {
	return e.StatusCode == 400 && strings.Contains(e.Message, "credit balance")
}

// IsAuthError returns true if this is an authentication error
func (e APIErrorInfo) IsAuthError() bool {
	return e.StatusCode == 401 || e.ErrorType == "authentication_error"
}

// IsRateLimitError returns true if this is a rate limit error
func (e APIErrorInfo) IsRateLimitError() bool {
	return e.StatusCode == 429
}

// ClientConfig holds configuration for the Anthropic client
type ClientConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	Timeout    time.Duration
	OnAPIError func(APIErrorInfo) // Called on critical API errors (billing, auth, rate limit)
}

// Message represents a message in a conversation
type Message struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// MessageRequest represents a request to the messages API
type MessageRequest struct {
	Model         string        `json:"model"`
	MaxTokens     int           `json:"max_tokens"`
	System        string        `json:"system,omitempty"`
	Messages      []Message     `json:"messages"`
	Stream        bool          `json:"stream,omitempty"`
	Temperature   float64       `json:"temperature,omitempty"`
	TopP          float64       `json:"top_p,omitempty"`
	TopK          int           `json:"top_k,omitempty"`
	StopSequences []string      `json:"stop_sequences,omitempty"`
	Tools         []interface{} `json:"tools,omitempty"` // Supports both regular and web_search tools
}

// WebSearchTool represents the web_search_20250305 tool
type WebSearchTool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses,omitempty"`
}

// MessageResponse represents a response from the messages API
type MessageResponse struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Role         string        `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string        `json:"model"`
	StopReason   string        `json:"stop_reason"`
	StopSequence string        `json:"stop_sequence,omitempty"`
	Usage        Usage         `json:"usage"`
}

// ContentBlock represents a content block in a response
type ContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`  // For server_tool_use blocks
	ID    string `json:"id,omitempty"`    // For tool use blocks
	Input interface{} `json:"input,omitempty"` // For tool use input
}

// Usage represents token usage information
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamEvent represents an event from the streaming API
type StreamEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index,omitempty"`
	ContentBlock ContentBlock `json:"content_block,omitempty"`
	Delta        Delta        `json:"delta,omitempty"`
	Message      *MessageResponse `json:"message,omitempty"`
	Usage        *Usage       `json:"usage,omitempty"`
	Error        *APIError    `json:"error,omitempty"`
}

// Delta represents incremental content in a stream
type Delta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// APIError represents an error from the API
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ToolDefinition defines a tool for the API
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolUse represents a tool call from the model
type ToolUse struct {
	Type  string                 `json:"type"`
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

// ToolResult represents the result of a tool call
type ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// NewClient creates a new Anthropic client
func NewClient(cfg ClientConfig) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	model := cfg.Model
	if model == "" {
		model = defaultModel
	}

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	return &Client{
		apiKey:    cfg.APIKey,
		baseURL:   baseURL,
		model:     model,
		maxTokens: maxTokens,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger:     slog.Default().With("component", "anthropic_client"),
		onAPIError: cfg.OnAPIError,
	}
}

// Complete sends a message and returns the full response
func (c *Client) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	req := MessageRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages: []Message{
			{Role: "user", Content: userPrompt},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return "", err
	}

	// Extract text from response
	var text strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	c.logger.Debug("completion finished",
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
	)

	return text.String(), nil
}

// CompleteWithMessages sends a multi-turn conversation
func (c *Client) CompleteWithMessages(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	req := MessageRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages:  messages,
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return "", err
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	return text.String(), nil
}

// Stream sends a message and returns a channel of events
func (c *Client) Stream(ctx context.Context, systemPrompt, userPrompt string) (<-chan StreamEvent, error) {
	req := MessageRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages: []Message{
			{Role: "user", Content: userPrompt},
		},
		Stream: true,
	}

	return c.streamRequest(ctx, req)
}

// StreamWithMessages streams a multi-turn conversation
func (c *Client) StreamWithMessages(ctx context.Context, systemPrompt string, messages []Message) (<-chan StreamEvent, error) {
	req := MessageRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages:  messages,
		Stream:    true,
	}

	return c.streamRequest(ctx, req)
}

// sendRequest sends a non-streaming request
func (c *Client) sendRequest(ctx context.Context, req MessageRequest) (*MessageResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error APIError `json:"error"`
		}
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			c.notifyAPIError(resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
			return nil, fmt.Errorf("API error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
		}
		c.notifyAPIError(resp.StatusCode, "unknown", string(respBody))
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var msgResp MessageResponse
	if err := json.Unmarshal(respBody, &msgResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &msgResp, nil
}

// streamRequest sends a streaming request
func (c *Client) streamRequest(ctx context.Context, req MessageRequest) (<-chan StreamEvent, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var apiErr struct {
			Error APIError `json:"error"`
		}
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			c.notifyAPIError(resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		} else {
			c.notifyAPIError(resp.StatusCode, "unknown", string(respBody))
		}
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	events := make(chan StreamEvent, 100)

	go func() {
		defer close(events)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// Parse SSE data
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")

				// Check for end of stream
				if data == "[DONE]" {
					return
				}

				var event StreamEvent
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					c.logger.Warn("failed to parse stream event", "error", err, "data", data)
					continue
				}

				select {
				case events <- event:
				case <-ctx.Done():
					return
				}

				// Check for message_stop
				if event.Type == "message_stop" {
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			c.logger.Error("stream scanner error", "error", err)
		}
	}()

	return events, nil
}

// notifyAPIError calls the error callback for critical API errors
func (c *Client) notifyAPIError(statusCode int, errType, message string) {
	if c.onAPIError == nil {
		return
	}
	info := APIErrorInfo{StatusCode: statusCode, ErrorType: errType, Message: message}
	if info.IsBillingError() || info.IsAuthError() || info.IsRateLimitError() {
		c.onAPIError(info)
	}
}

// setHeaders sets required headers for Anthropic API
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
}

// IsConfigured returns true if the client has an API key
func (c *Client) IsConfigured() bool {
	return c.apiKey != ""
}

// CompleteWithTools sends a message with tools and returns the response
func (c *Client) CompleteWithTools(ctx context.Context, systemPrompt, userPrompt string, tools []interface{}) (*MessageResponse, error) {
	req := MessageRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages: []Message{
			{Role: "user", Content: userPrompt},
		},
		Tools: tools,
	}

	return c.sendRequest(ctx, req)
}

// GetModel returns the current model
func (c *Client) GetModel() string {
	return c.model
}

// SetModel changes the model
func (c *Client) SetModel(model string) {
	c.model = model
}

// SetMaxTokens changes the max tokens
func (c *Client) SetMaxTokens(maxTokens int) {
	c.maxTokens = maxTokens
}

// CompleteWithMaxTokens sends a completion with a per-call max_tokens override.
// This is thread-safe (unlike SetMaxTokens) since it doesn't mutate client state.
func (c *Client) CompleteWithMaxTokens(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	req := MessageRequest{
		Model:     c.model,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages: []Message{
			{Role: "user", Content: userPrompt},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return "", err
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	c.logger.Debug("completion finished",
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
		"max_tokens", maxTokens,
	)

	return text.String(), nil
}

// ExtractText extracts all text content from a MessageResponse.
// Web search responses contain mixed content blocks (server_tool_use, text, etc.).
// This helper concatenates only the text blocks.
func ExtractText(resp *MessageResponse) string {
	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// CollectStreamText collects all text from a stream
func CollectStreamText(events <-chan StreamEvent) (string, *Usage, error) {
	var text strings.Builder
	var usage *Usage
	var lastErr error

	for event := range events {
		switch event.Type {
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				text.WriteString(event.Delta.Text)
			}
		case "message_delta":
			if event.Usage != nil {
				usage = event.Usage
			}
		case "error":
			if event.Error != nil {
				lastErr = fmt.Errorf("stream error (%s): %s", event.Error.Type, event.Error.Message)
			}
		}
	}

	if lastErr != nil {
		return "", nil, lastErr
	}

	return text.String(), usage, nil
}
