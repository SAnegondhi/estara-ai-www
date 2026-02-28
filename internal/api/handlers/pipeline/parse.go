// Package pipeline — broker OM document parse endpoint (ADR-102).
package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/pkg/httputil"
)

// ---------------------------------------------------------------------------
// ParseDocument — POST /api/pipeline/parse-document
// ---------------------------------------------------------------------------

// parseDocumentResponse is the response returned to the client after parsing.
type parseDocumentResponse struct {
	// Fields extracted from the document. Null values were not found.
	Address      *string  `json:"address"`
	City         *string  `json:"city"`
	State        *string  `json:"state"`
	Zip          *string  `json:"zip"`
	PropertyType *string  `json:"propertyType"` // sfh|multifamily|condo|townhouse|commercial|nnn|other
	Beds         *float64 `json:"beds"`
	Baths        *float64 `json:"baths"`
	Sqft         *int     `json:"sqft"`
	YearBuilt    *int     `json:"yearBuilt"`
	Units        *int     `json:"units"`
	AskingPrice       *float64 `json:"askingPrice"`
	BrokerRentCurrent  *float64 `json:"brokerRentCurrent"`  // Current in-place monthly rent (broker-stated)
	BrokerRentProForma *float64 `json:"brokerRentProForma"` // Pro forma monthly rent (broker-projected)
	BrokerRentMarket   *float64 `json:"brokerRentMarket"`   // Market monthly rent (broker-cited)
	BrokerCapRate      *float64 `json:"brokerCapRate"`      // As a decimal (e.g. 0.065 for 6.5%)
	VacancyRate  *float64 `json:"vacancyRate"`     // As a decimal
	// Financing
	DownPaymentPct *float64 `json:"downPaymentPct"` // As a decimal (e.g. 0.20)
	InterestRate   *float64 `json:"interestRate"`   // As a decimal (e.g. 0.0725)
	FinancingType  *string  `json:"financingType"`  // conventional|cash|other

	// Per-field confidence. Only keys with values other than "" are present.
	// Values: "high" | "medium" | "low"
	Confidence map[string]string `json:"confidence"`

	// True if parsing succeeded and at least one field was extracted.
	Extracted bool `json:"extracted"`
	// Human-readable note if extraction failed or was partial.
	Note string `json:"note,omitempty"`
}

const maxDocumentSize = 10 * 1024 * 1024 // 10 MB

// ParseDocument parses a broker OM PDF and returns structured property data.
func (h *Handler) ParseDocument(w http.ResponseWriter, r *http.Request) {
	if middleware.GetUserFromContext(r.Context()) == nil {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Limit request body to avoid OOM on malicious uploads.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+1024)
	if err := r.ParseMultipartForm(maxDocumentSize); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.Error(w, http.StatusRequestEntityTooLarge,
				"This file exceeds the 10MB limit. Try a compressed version or enter the details manually.")
			return
		}
		httputil.Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	if header.Size > maxDocumentSize {
		httputil.Error(w, http.StatusRequestEntityTooLarge,
			"This file exceeds the 10MB limit. Try a compressed version or enter the details manually.")
		return
	}

	// Determine MIME type from content-type or filename extension.
	ct := header.Header.Get("Content-Type")
	filename := strings.ToLower(header.Filename)

	var mediaType string
	switch {
	case strings.Contains(ct, "pdf") || strings.HasSuffix(filename, ".pdf"):
		mediaType = "application/pdf"
	default:
		httputil.Error(w, http.StatusBadRequest,
			"Only PDF files are supported. Please upload a PDF version of the broker OM.")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		h.logger.Error("failed to read uploaded file", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	if h.cfg.AI.AnthropicAPIKey == "" {
		httputil.Error(w, http.StatusServiceUnavailable, "AI service not configured")
		return
	}

	result, err := h.extractDocumentFields(r.Context(), fileBytes, mediaType)
	if err != nil {
		h.logger.Warn("document extraction failed", "error", err)
		// Return a graceful empty response — don't block the user from manual entry.
		httputil.JSON(w, http.StatusOK, parseDocumentResponse{
			Extracted: false,
			Note:      "We couldn't extract data from this document. Please fill in the fields manually.",
		})
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// Claude extraction
// ---------------------------------------------------------------------------

const extractionSystemPrompt = `You are a real estate data extraction assistant. Your only job is to extract structured property data from broker offering memoranda (OMs), pitch decks, and property listings.

Rules:
- Extract ONLY what is explicitly stated in the document — never infer, calculate, estimate, or assume.
- Return null for any field not found. Do NOT guess or derive values from other fields.
- Do not calculate cap rate from rent and price — only extract if explicitly stated as a cap rate.
- For rent, extract the monthly figure. If stated as annual, divide by 12.
- Rent disambiguation: many OMs contain multiple rent figures (current in-place, pro forma projected, market comparable). You MUST return them in separate fields — never conflate or average them.
  - brokerRentCurrent: the actual current rent being collected today (in-place rents)
  - brokerRentProForma: the broker's projected future rent after renovations or lease-up
  - brokerRentMarket: market-rate comparable rents cited (not current, not projected — market context)
  - If the document has only one rent figure and does not distinguish type, put it in brokerRentCurrent and leave the others null.
- Prose-only documents: some OMs are written as narrative text with no financial tables (common for NNN commercial). For these, extract only what is explicitly stated in prose. Return null for any field not mentioned. Do NOT hallucinate expense breakdowns, unit mixes, or rent rolls that are not present.
- Confidence levels: "high" = exact explicit statement, "medium" = clearly implied from context, "low" = uncertain or ambiguous.
- Return ONLY valid JSON with no markdown, no explanation, no prose.`

const extractionUserPrompt = `Extract the following fields from this broker OM document. Return a single JSON object with exactly these keys. Use null for any field not found.

CRITICAL: Return null for any field not explicitly present in the document. Do not estimate, calculate, or infer missing values.

{
  "address": string | null,            // Street address only (no city/state)
  "city": string | null,
  "state": string | null,              // 2-letter abbreviation
  "zip": string | null,
  "propertyType": string | null,       // One of: sfh, multifamily, condo, townhouse, commercial, nnn, other
  "beds": number | null,
  "baths": number | null,
  "sqft": number | null,
  "yearBuilt": number | null,
  "units": number | null,              // Number of rental units (use 1 for SFH)
  "askingPrice": number | null,        // In USD, numeric only
  "brokerRentCurrent": number | null,  // Current in-place monthly rent, broker-stated (NOT pro forma)
  "brokerRentProForma": number | null, // Pro forma monthly rent after stabilization/renovation (null if not stated)
  "brokerRentMarket": number | null,   // Market-rate comparable monthly rent cited in OM (null if not stated)
  "brokerCapRate": number | null,      // As decimal (e.g., 0.065 for 6.5%)
  "vacancyRate": number | null,        // As decimal (e.g., 0.05 for 5%)
  "downPaymentPct": number | null,     // As decimal (e.g., 0.25 for 25%)
  "interestRate": number | null,       // As decimal (e.g., 0.0725 for 7.25%)
  "financingType": string | null,      // One of: conventional, cash, other
  "confidence": {                      // Confidence for each extracted (non-null) field only
    "<fieldName>": "high" | "medium" | "low"
  }
}

If the document is a prose/narrative OM without structured financial tables (e.g., a commercial NNN summary), extract only what is stated in the executive summary or narrative text. Return null for expense breakdowns, unit mixes, and rent rolls that are not present. The response must still be valid JSON.`

// anthropicDocumentRequest is the raw Anthropic API request for document analysis.
// We make a direct HTTP call here because the existing Client.Message struct uses
// string content — this endpoint needs the structured content array with document blocks.
type anthropicDocumentRequest struct {
	Model     string                  `json:"model"`
	MaxTokens int                     `json:"max_tokens"`
	System    string                  `json:"system"`
	Messages  []anthropicDocumentMsg  `json:"messages"`
}

type anthropicDocumentMsg struct {
	Role    string        `json:"role"`
	Content []interface{} `json:"content"`
}

type anthropicDocBlock struct {
	Type   string              `json:"type"`
	Source anthropicDocSource  `json:"source"`
}

type anthropicDocSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "application/pdf"
	Data      string `json:"data"`       // base64-encoded bytes
}

type anthropicTextBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

func (h *Handler) extractDocumentFields(ctx context.Context, fileBytes []byte, mediaType string) (*parseDocumentResponse, error) {
	encoded := base64.StdEncoding.EncodeToString(fileBytes)

	reqBody := anthropicDocumentRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 2048,
		System:    extractionSystemPrompt,
		Messages: []anthropicDocumentMsg{
			{
				Role: "user",
				Content: []interface{}{
					anthropicDocBlock{
						Type: "document",
						Source: anthropicDocSource{
							Type:      "base64",
							MediaType: mediaType,
							Data:      encoded,
						},
					},
					anthropicTextBlock{
						Type: "text",
						Text: extractionUserPrompt,
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "pdfs-2024-09-25")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, string(respBytes))
	}

	// Parse the Anthropic response envelope.
	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var rawText string
	for _, block := range envelope.Content {
		if block.Type == "text" {
			rawText = block.Text
			break
		}
	}
	if rawText == "" {
		return nil, fmt.Errorf("no text content in response")
	}

	// Strip markdown code fences if present.
	rawText = strings.TrimSpace(rawText)
	if strings.HasPrefix(rawText, "```") {
		lines := strings.Split(rawText, "\n")
		if len(lines) >= 3 {
			rawText = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	// Parse the extracted JSON.
	var raw struct {
		Address       *string            `json:"address"`
		City          *string            `json:"city"`
		State         *string            `json:"state"`
		Zip           *string            `json:"zip"`
		PropertyType  *string            `json:"propertyType"`
		Beds          *float64           `json:"beds"`
		Baths         *float64           `json:"baths"`
		Sqft          *int               `json:"sqft"`
		YearBuilt     *int               `json:"yearBuilt"`
		Units         *int               `json:"units"`
		AskingPrice        *float64           `json:"askingPrice"`
		BrokerRentCurrent  *float64           `json:"brokerRentCurrent"`
		BrokerRentProForma *float64           `json:"brokerRentProForma"`
		BrokerRentMarket   *float64           `json:"brokerRentMarket"`
		BrokerCapRate      *float64           `json:"brokerCapRate"`
		VacancyRate   *float64           `json:"vacancyRate"`
		DownPaymentPct *float64          `json:"downPaymentPct"`
		InterestRate  *float64           `json:"interestRate"`
		FinancingType *string            `json:"financingType"`
		Confidence    map[string]string  `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(rawText), &raw); err != nil {
		return nil, fmt.Errorf("parse extracted JSON: %w", err)
	}

	// Check if anything was extracted.
	extracted := raw.Address != nil || raw.AskingPrice != nil ||
		raw.BrokerRentCurrent != nil || raw.BrokerRentProForma != nil || raw.BrokerRentMarket != nil ||
		raw.City != nil || raw.Beds != nil || raw.Sqft != nil || raw.BrokerCapRate != nil

	result := &parseDocumentResponse{
		Address:            raw.Address,
		City:               raw.City,
		State:              raw.State,
		Zip:                raw.Zip,
		PropertyType:       raw.PropertyType,
		Beds:               raw.Beds,
		Baths:              raw.Baths,
		Sqft:               raw.Sqft,
		YearBuilt:          raw.YearBuilt,
		Units:              raw.Units,
		AskingPrice:        raw.AskingPrice,
		BrokerRentCurrent:  raw.BrokerRentCurrent,
		BrokerRentProForma: raw.BrokerRentProForma,
		BrokerRentMarket:   raw.BrokerRentMarket,
		BrokerCapRate:      raw.BrokerCapRate,
		VacancyRate:        raw.VacancyRate,
		DownPaymentPct:     raw.DownPaymentPct,
		InterestRate:       raw.InterestRate,
		FinancingType:      raw.FinancingType,
		Confidence:         raw.Confidence,
		Extracted:          extracted,
	}
	if !extracted {
		result.Note = "We couldn't extract data from this document. Please fill in the fields manually."
	}
	return result, nil
}
