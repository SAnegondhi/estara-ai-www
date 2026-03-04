// Package pipeline — OM validation workflow (ADR-106).
package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// omQuestion is the structured question Claude generates for a missing OM field.
// Stored as JSONB in om_questions column.
type omQuestion struct {
	ID             string      `json:"id"`             // stable slug, e.g. "unit_count_studio"
	Category       string      `json:"category"`       // "unit_mix" | "financials" | "valuation" | "lease"
	Field          string      `json:"field"`          // JSON path of the missing field
	Required       bool        `json:"required"`       // true = blocks validation
	Question       string      `json:"question"`       // investor-direct phrasing
	BrokerQuestion string      `json:"brokerQuestion"` // broker-professional phrasing
	Context        string      `json:"context"`        // why this field matters for analysis
	Answer         interface{} `json:"answer"`         // null until answered
	AnsweredAt     *string     `json:"answeredAt"`     // ISO timestamp, null until answered
}

// omValidationResult is the Claude response for the validate endpoint.
type omValidationResult struct {
	Questions       []omQuestion `json:"questions"`
	MissingRequired []string     `json:"missingRequired"` // list of required field names that are absent
	Summary         string       `json:"summary"`         // one-paragraph summary of what was found and what's missing
}

// ---------------------------------------------------------------------------
// ValidateOM — POST /api/pipeline/deals/{dealId}/properties/{propId}/om-validate
// ---------------------------------------------------------------------------

// ValidateOM analyzes the extracted OM data, identifies missing fields, generates
// structured questions, and stores them. Called automatically after upload and
// on-demand from the om-review page.
func (h *Handler) ValidateOM(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	row, err := h.store.Q().GetPipelinePropertyOMValidation(r.Context(), queries.GetPipelinePropertyOMValidationParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	if len(row.OmData) == 0 || string(row.OmData) == "null" {
		httputil.BadRequest(w, "no OM data to validate — upload an OM first")
		return
	}

	result, err := h.runOMValidation(r.Context(), propID, userID, row.OmData)
	if err != nil {
		h.logger.Error("ValidateOM failed", "propId", propID, "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Fetch the updated row to return current status.
	updated, _ := h.store.Q().GetPipelinePropertyOMValidation(r.Context(), queries.GetPipelinePropertyOMValidationParams{
		ID:     propID,
		UserID: userID,
	})

	httputil.Success(w, map[string]any{
		"status":          updated.OmValidationStatus,
		"questions":       result.Questions,
		"missingRequired": result.MissingRequired,
		"summary":         result.Summary,
	})
}

// runOMValidation calls Claude to validate OM data, stores the result, and returns it.
// Called by ValidateOM handler and auto-triggered from UploadOMFile.
func (h *Handler) runOMValidation(ctx context.Context, propID uuid.UUID, _ string, omDataJSON []byte) (*omValidationResult, error) {
	if h.cfg.AI.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("Anthropic API key not configured")
	}

	result, err := h.callClaudeForOMValidation(ctx, omDataJSON)
	if err != nil {
		return nil, fmt.Errorf("Claude validation call: %w", err)
	}

	questionsJSON, err := json.Marshal(result.Questions)
	if err != nil {
		return nil, fmt.Errorf("marshal questions: %w", err)
	}

	status := "incomplete"
	if len(result.MissingRequired) == 0 {
		status = "validated"
	}

	_, err = h.store.Q().UpdatePipelinePropertyOMValidation(ctx, queries.UpdatePipelinePropertyOMValidationParams{
		ID:                 propID,
		OmValidationStatus: status,
		Column3:            questionsJSON,
		Column4:            nil,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// AnswerOMQuestions — PUT /api/pipeline/deals/{dealId}/properties/{propId}/om-questions
// ---------------------------------------------------------------------------

type answerOMQuestionsRequest struct {
	Answers map[string]interface{} `json:"answers"` // questionId → value
}

// AnswerOMQuestions merges user answers into the stored questions and checks
// if all required questions are now answered.
func (h *Handler) AnswerOMQuestions(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	var req answerOMQuestionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	row, err := h.store.Q().GetPipelinePropertyOMValidation(r.Context(), queries.GetPipelinePropertyOMValidationParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	// Decode existing questions.
	var questions []omQuestion
	if len(row.OmQuestions) > 0 && string(row.OmQuestions) != "null" {
		if err := json.Unmarshal(row.OmQuestions, &questions); err != nil {
			httputil.InternalError(w, fmt.Errorf("decode questions: %w", err))
			return
		}
	}

	// Merge answers.
	now := time.Now().UTC().Format(time.RFC3339)
	for i, q := range questions {
		if val, ok := req.Answers[q.ID]; ok {
			questions[i].Answer = val
			questions[i].AnsweredAt = &now
		}
	}

	// Check if all required questions are answered.
	isValidated := true
	for _, q := range questions {
		if q.Required && q.Answer == nil {
			isValidated = false
			break
		}
	}

	status := row.OmValidationStatus
	if isValidated {
		status = "validated"
	}

	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("marshal questions: %w", err))
		return
	}

	// Build validated data: merge om_data + answers.
	var validatedData json.RawMessage
	if isValidated {
		validatedData, _ = buildValidatedData(row.OmData, questions)
	}

	updated, err := h.store.Q().UpdatePipelinePropertyOMValidation(r.Context(), queries.UpdatePipelinePropertyOMValidationParams{
		ID:                 propID,
		OmValidationStatus: status,
		Column3:            questionsJSON,
		Column4:            validatedData,
	})
	if err != nil {
		httputil.InternalError(w, err)
		return
	}

	httputil.Success(w, map[string]any{
		"status":      updated.OmValidationStatus,
		"isValidated": isValidated,
		"questions":   questions,
	})
}

// buildValidatedData merges the raw om_data JSONB with user answers from questions.
// ADR-107: applies each answered question's field path as an override onto the OM data map.
func buildValidatedData(omDataJSON []byte, questions []omQuestion) (json.RawMessage, error) {
	var merged map[string]interface{}
	if len(omDataJSON) > 0 && string(omDataJSON) != "null" {
		if err := json.Unmarshal(omDataJSON, &merged); err != nil {
			return nil, err
		}
	}
	if merged == nil {
		merged = make(map[string]interface{})
	}

	// Apply each answered question's value at its declared field path.
	for _, q := range questions {
		if q.Answer != nil && q.Field != "" {
			setNestedField(merged, q.Field, q.Answer)
		}
	}

	merged["_validatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return json.Marshal(merged)
}

// setNestedField applies a dot-separated field path onto a nested map.
// Supports simple paths (e.g. "askingPrice"), dotted paths ("brokerContact.name"),
// and array-indexed paths ("rentByUnitType.0.count").
// For array indices, the value is set on the element at that index if the slice exists.
func setNestedField(data map[string]interface{}, path string, value interface{}) {
	parts := strings.SplitN(path, ".", 2)
	key := parts[0]

	// Strip bracket notation from key (treat "rentByUnitType[0]" same as "rentByUnitType.0").
	if bracket := strings.Index(key, "["); bracket != -1 {
		// e.g. "rentByUnitType[0]" → key="rentByUnitType", idx="0"
		idx := strings.TrimRight(key[bracket+1:], "]")
		key = key[:bracket]
		if len(parts) == 2 {
			// Nested: "rentByUnitType[0].count"
			parts = []string{key, idx + "." + parts[1]}
		} else {
			parts = []string{key, idx}
		}
	}

	if len(parts) == 1 {
		// Leaf: set the value directly.
		data[key] = value
		return
	}

	// Check if the next segment is an array index.
	next := parts[1]
	nextParts := strings.SplitN(next, ".", 2)
	if isDigits(nextParts[0]) {
		// Array index: data[key] must be a []interface{}.
		idx := 0
		for _, c := range nextParts[0] {
			idx = idx*10 + int(c-'0')
		}
		arr, _ := data[key].([]interface{})
		if idx < len(arr) {
			if len(nextParts) == 1 {
				arr[idx] = value
			} else if nested, ok := arr[idx].(map[string]interface{}); ok {
				setNestedField(nested, nextParts[1], value)
			}
		}
		return
	}

	// Nested map.
	nested, ok := data[key].(map[string]interface{})
	if !ok {
		nested = make(map[string]interface{})
		data[key] = nested
	}
	setNestedField(nested, next, value)
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ---------------------------------------------------------------------------
// GetOMBrokerReport — GET /api/pipeline/deals/{dealId}/properties/{propId}/om-broker-report
// ---------------------------------------------------------------------------

// GetOMBrokerReport returns a markdown-formatted Information Request document
// the user can copy/send to the broker.
func (h *Handler) GetOMBrokerReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	propID, err := uuid.Parse(chi.URLParam(r, "propId"))
	if err != nil {
		httputil.BadRequest(w, "invalid property ID")
		return
	}

	prop, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	row, err := h.store.Q().GetPipelinePropertyOMValidation(r.Context(), queries.GetPipelinePropertyOMValidationParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	var questions []omQuestion
	if len(row.OmQuestions) > 0 && string(row.OmQuestions) != "null" {
		if err := json.Unmarshal(row.OmQuestions, &questions); err != nil {
			httputil.InternalError(w, fmt.Errorf("decode questions: %w", err))
			return
		}
	}

	report := buildBrokerReport(prop, questions)

	httputil.Success(w, map[string]any{
		"markdown": report,
	})
}

// buildBrokerReport generates the markdown Information Request document.
func buildBrokerReport(prop queries.PipelineProperty, questions []omQuestion) string {
	var sb strings.Builder

	sb.WriteString("# Information Request — ")
	sb.WriteString(prop.Address)
	sb.WriteString("\n\n")
	sb.WriteString("*Generated by Estara AI Pipeline on ")
	sb.WriteString(time.Now().UTC().Format("January 2, 2006"))
	sb.WriteString("*\n\n")
	sb.WriteString("---\n\n")

	sb.WriteString("## Overview\n\n")
	sb.WriteString("We have received and reviewed your Offering Memorandum for the above property. ")
	sb.WriteString("To complete our underwriting and advance our analysis, we require the following additional information. ")
	sb.WriteString("Please provide responses at your earliest convenience.\n\n")

	// Required questions.
	var required, recommended []omQuestion
	for _, q := range questions {
		if q.Answer == nil {
			if q.Required {
				required = append(required, q)
			} else {
				recommended = append(recommended, q)
			}
		}
	}

	if len(required) > 0 {
		sb.WriteString("## Required Information\n\n")
		sb.WriteString("The following items are essential to proceed with our analysis:\n\n")
		for i, q := range required {
			sb.WriteString(fmt.Sprintf("%d. **%s**  \n", i+1, q.BrokerQuestion))
			if q.Context != "" {
				sb.WriteString(fmt.Sprintf("   *Context: %s*\n", q.Context))
			}
			sb.WriteString("\n")
		}
	}

	if len(recommended) > 0 {
		sb.WriteString("## Additional Information Requested\n\n")
		sb.WriteString("The following items would strengthen our analysis:\n\n")
		for i, q := range recommended {
			sb.WriteString(fmt.Sprintf("%d. **%s**  \n", i+1, q.BrokerQuestion))
			if q.Context != "" {
				sb.WriteString(fmt.Sprintf("   *Context: %s*\n", q.Context))
			}
			sb.WriteString("\n")
		}
	}

	if len(required) == 0 && len(recommended) == 0 {
		sb.WriteString("All required information has been provided. No further requests at this time.\n\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString("Please respond to this request or contact us if you have any questions.\n")

	return sb.String()
}

// ---------------------------------------------------------------------------
// Claude call for OM validation
// ---------------------------------------------------------------------------

const omValidationSystemPrompt = `You are a real estate investment analyst reviewing extracted OM data for completeness.

Given the extracted OM fields in JSON, your job is to:
1. Identify which required and recommended fields for investment analysis are missing or null
2. Generate structured questions an investor can answer themselves or ask the broker
3. Write each question in two forms: investor-direct (plain, direct) and broker-professional (formal, courteous)
4. Provide context explaining why each field matters for deal analysis

REQUIRED FIELDS (analysis cannot proceed reliably without these):
- askingPrice — no cap rate, no return calculation possible
- capRate OR brokerNOI — need at least one path to yield
- rentByUnitType entries with count, OR aggregate unit count — needed for GPR calculation
- Total unit count (units field)

RECOMMENDED FIELDS (improve accuracy):
- Full expense schedule (taxes, insurance, maintenance, utilities)
- vacancyAssumption + vacancyLabel (distinguish "bad debt" from physical vacancy)
- capRateProForma (for value-add OMs that stabilize to a higher cap rate)
- brokerNOIStabilized (Year 4/stabilized NOI vs Year 1)
- Lease status (month-to-month vs fixed term) — affects rent uplift timeline
- Commercial unit details if mixed-use

Generate a JSON response with this exact structure:
{
  "questions": [
    {
      "id": "snake_case_slug",
      "category": "unit_mix|financials|valuation|lease|physical",
      "field": "the.json.field.path",
      "required": true|false,
      "question": "Direct question for the investor",
      "brokerQuestion": "Professional request for the broker",
      "context": "Why this field matters for analysis"
    }
  ],
  "missingRequired": ["fieldName1", "fieldName2"],
  "summary": "One paragraph describing what was extracted and what is missing."
}

Only generate questions for fields that are actually null/missing — do not ask for data that is already present.
Return ONLY valid JSON, no markdown, no prose outside the JSON.`

// callClaudeForOMValidation sends the validation request to Claude.
func (h *Handler) callClaudeForOMValidation(ctx context.Context, omDataJSON []byte) (*omValidationResult, error) {
	const anthropicURL = "https://api.anthropic.com/v1/messages"

	userMsg := fmt.Sprintf("Here is the extracted OM data. Analyze it and generate validation questions for any missing required or recommended fields:\n\n```json\n%s\n```", string(omDataJSON))

	reqBody := struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096,
		System:    omValidationSystemPrompt,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "user", Content: userMsg},
		},
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse envelope: %w", err)
	}

	var jsonText string
	for _, block := range envelope.Content {
		if block.Type == "text" {
			jsonText = strings.TrimSpace(block.Text)
			break
		}
	}
	if jsonText == "" {
		return nil, fmt.Errorf("empty response from Claude")
	}

	jsonText = strings.TrimPrefix(jsonText, "```json")
	jsonText = strings.TrimPrefix(jsonText, "```")
	jsonText = strings.TrimSuffix(jsonText, "```")
	jsonText = strings.TrimSpace(jsonText)

	var result omValidationResult
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, fmt.Errorf("parse validation JSON: %w", err)
	}

	return &result, nil
}

// pgtype helpers for nullable JSONB/text in sqlc params.
func jsonbOrNil(b json.RawMessage) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nullableText returns pgtype.Text from a nullable string pointer.
func nullableText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
