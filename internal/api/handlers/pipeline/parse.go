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
	"github.com/xuri/excelize/v2"
)

// ---------------------------------------------------------------------------
// ParseDocument — POST /api/pipeline/parse-document
// ---------------------------------------------------------------------------

// UnitMixRow captures a single unit-type row from a multifamily rent roll.
// For a 19-unit apartment with studios, 1bd, and 2bd units, there would be
// three rows — one per unit type — each with its own count, specs, and rents.
type UnitMixRow struct {
	Type          string   `json:"type"`           // studio|1bd|2bd|3bd|4bd+|penthouse|suite|floor|retail|commercial|other
	Count         int      `json:"count"`          // Number of units of this type
	Beds          *float64 `json:"beds"`           // Bedrooms per unit (null for studios/commercial/office)
	Baths         *float64 `json:"baths"`          // Bathrooms per unit (null for office)
	SqftPerUnit   *int     `json:"sqftPerUnit"`    // Sq ft per unit (not aggregate)
	RentCurrent   *float64 `json:"rentCurrent"`    // Current monthly rent per unit (broker-stated)
	RentProForma  *float64 `json:"rentProForma"`   // Pro forma monthly rent per unit (broker-projected)
	RentMarket    *float64 `json:"rentMarket"`     // Market monthly rent per unit (comparable)
	OccupancyPct  *float64 `json:"occupancyPct"`  // Current occupancy % for this unit type (0–100)
	PricePerUnit  *float64 `json:"pricePerUnit"`  // Asking/sale price per unit (for condo/portfolio sales)
	BuildingLabel *string  `json:"buildingLabel"` // Optional: building name/label for multi-building grouping
}

// parseDocumentResponse is the response returned to the client after parsing.
type parseDocumentResponse struct {
	// Fields extracted from the document. Null values were not found.
	Address      *string  `json:"address"`
	City         *string  `json:"city"`
	State        *string  `json:"state"`
	Zip          *string  `json:"zip"`
	PropertyType *string  `json:"propertyType"` // sfh|multifamily|condo|townhouse|commercial|nnn|retail|mixed_use|industrial|warehouse|self_storage|student_housing|senior_housing|other
	Beds         *float64 `json:"beds"`          // Null for MF when unitMix is populated
	Baths        *float64 `json:"baths"`         // Null for MF when unitMix is populated
	Sqft         *int     `json:"sqft"`          // Total building sq ft (all units combined)
	YearBuilt    *int     `json:"yearBuilt"`
	Units        *int     `json:"units"`
	AskingPrice        *float64     `json:"askingPrice"`
	BrokerRentCurrent  *float64     `json:"brokerRentCurrent"`  // Aggregate current in-place monthly rent
	BrokerRentProForma *float64     `json:"brokerRentProForma"` // Aggregate pro forma monthly rent
	BrokerRentMarket   *float64     `json:"brokerRentMarket"`   // Aggregate market monthly rent
	BrokerCapRate      *float64     `json:"brokerCapRate"`      // As a decimal (e.g. 0.065 for 6.5%)
	VacancyRate        *float64     `json:"vacancyRate"`        // As a decimal
	// Per-unit-type breakdown for MF/condo. Null for SFH and non-unit properties.
	UnitMix       []UnitMixRow `json:"unitMix"`
	LotSqft       *int         `json:"lotSqft"`       // Land parcel area in sq ft
	BuildingCount *int         `json:"buildingCount"` // Number of structures on the parcel
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
	case strings.Contains(ct, "spreadsheetml") || strings.Contains(ct, "ms-excel") ||
		strings.HasSuffix(filename, ".xlsx") || strings.HasSuffix(filename, ".xls"):
		mediaType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case strings.Contains(ct, "text/plain") || strings.Contains(ct, "text/csv") ||
		strings.HasSuffix(filename, ".txt") || strings.HasSuffix(filename, ".csv"):
		mediaType = "text/plain"
	default:
		httputil.Error(w, http.StatusBadRequest,
			"Unsupported file type. Please upload a PDF, Excel (.xlsx/.xls), or text (.txt/.csv) file.")
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
  - brokerRentCurrent: the actual current rent being collected today (aggregate across all units)
  - brokerRentProForma: the broker's projected future rent after renovations or lease-up (aggregate)
  - brokerRentMarket: market-rate comparable rents cited (not current, not projected — market context, aggregate)
  - If the document has only one rent figure and does not distinguish type, put it in brokerRentCurrent and leave the others null.
- Multifamily / condo / office unit mix: When the document contains a rent roll, unit-type table, or floor/suite schedule, extract the per-unit-type breakdown into the unitMix array. Each row represents one unit type or suite category.
  - unitMix[].type: studio|1bd|2bd|3bd|4bd+|penthouse|suite|floor|retail|commercial|other
  - unitMix[].rentCurrent/rentProForma/rentMarket: per-unit monthly rent (same disambiguation rules as top-level)
  - unitMix[].sqftPerUnit: square footage per unit (not the aggregate)
  - unitMix[].count: number of units of this type
  - unitMix[].occupancyPct: current occupancy percentage for this unit type (0–100); extract only if stated per unit type, not just overall property occupancy
  - unitMix[].pricePerUnit: asking or sale price per individual unit (extract for condo or portfolio unit sales; null for portfolio MF sales where one total price is given)
  - unitMix[].buildingLabel: if the OM describes multiple buildings (e.g. "Building A", "Building B", "Phase 1"), use the building name/label as the grouping identifier for each row; null for single-building properties
  - For MF with unitMix, leave top-level beds and baths null — they are meaningless scalars for a mixed unit building
  - Top-level sqft is the total building square footage (all units combined; sum of count × sqftPerUnit if not stated)
  - lotSqft is the land parcel area in sq ft — distinct from building sqft
  - buildingCount is the number of structures on the parcel (common in garden-style or campus MF)
  - Top-level brokerRentCurrent/ProForma/Market are the aggregate gross monthly income figures across all units
  - unitMix is null for SFH, NNN commercial, and any property where no unit-type breakdown is stated
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
  "propertyType": string | null,       // One of: sfh, multifamily, condo, townhouse, office, commercial, nnn, retail, mixed_use, industrial, warehouse, self_storage, student_housing, senior_housing, other
  "beds": number | null,               // Null for MF when unitMix is populated
  "baths": number | null,              // Null for MF when unitMix is populated
  "sqft": number | null,               // Total building sq ft (all units combined)
  "yearBuilt": number | null,
  "units": number | null,              // Total number of rental units
  "askingPrice": number | null,        // In USD, numeric only
  "brokerRentCurrent": number | null,  // Aggregate current in-place monthly rent (NOT pro forma)
  "brokerRentProForma": number | null, // Aggregate pro forma monthly rent after stabilization (null if not stated)
  "brokerRentMarket": number | null,   // Aggregate market-rate comparable monthly rent (null if not stated)
  "brokerCapRate": number | null,      // As decimal (e.g., 0.065 for 6.5%)
  "vacancyRate": number | null,        // As decimal (e.g., 0.05 for 5%)
  "unitMix": [                         // Per-unit-type breakdown for MF/condo/office; null for SFH/NNN
    {
      "type": string,                  // studio|1bd|2bd|3bd|4bd+|penthouse|suite|floor|retail|commercial|other
      "count": number,                 // Number of units of this type
      "beds": number | null,           // Bedrooms per unit (null for studio/office/commercial)
      "baths": number | null,          // Null for office/commercial
      "sqftPerUnit": number | null,    // Sq ft per unit (not total)
      "rentCurrent": number | null,    // Current monthly rent per unit
      "rentProForma": number | null,   // Pro forma monthly rent per unit
      "rentMarket": number | null,     // Market monthly rent per unit
      "occupancyPct": number | null,   // Occupancy % for this unit type (0-100); null if not stated per type
      "pricePerUnit": number | null,   // Sale/asking price per unit (condo/portfolio sales only)
      "buildingLabel": string | null   // Building name if multi-building (e.g. "Building A"); null otherwise
    }
  ] | null,
  "lotSqft": number | null,           // Land parcel area in sq ft (distinct from building sqft)
  "buildingCount": number | null,     // Number of structures on the parcel
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
	switch mediaType {
	case "application/pdf":
		return h.extractFromPDF(ctx, fileBytes)
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		text, err := excelToText(fileBytes)
		if err != nil {
			return nil, fmt.Errorf("read Excel file: %w", err)
		}
		return h.extractFromText(ctx, text)
	case "text/plain":
		return h.extractFromText(ctx, string(fileBytes))
	default:
		return nil, fmt.Errorf("unsupported media type: %s", mediaType)
	}
}

// excelToText converts an Excel workbook to a plain-text representation
// that Claude can parse. Each sheet is rendered as a tab-delimited table.
func excelToText(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		// Skip entirely empty sheets.
		hasContent := false
		for _, row := range rows {
			for _, cell := range row {
				if strings.TrimSpace(cell) != "" {
					hasContent = true
					break
				}
			}
			if hasContent {
				break
			}
		}
		if !hasContent {
			continue
		}

		fmt.Fprintf(&sb, "=== Sheet: %s ===\n", sheet)
		for _, row := range rows {
			sb.WriteString(strings.Join(row, "\t"))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
	}

	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "", fmt.Errorf("Excel file contains no readable content")
	}
	return result, nil
}

// extractFromPDF sends a base64-encoded PDF to Claude using the native PDF
// document block (requires anthropic-beta: pdfs-2024-09-25).
func (h *Handler) extractFromPDF(ctx context.Context, fileBytes []byte) (*parseDocumentResponse, error) {
	encoded := base64.StdEncoding.EncodeToString(fileBytes)

	reqBody := anthropicDocumentRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096, // unit mix tables can be verbose
		System:    extractionSystemPrompt,
		Messages: []anthropicDocumentMsg{
			{
				Role: "user",
				Content: []interface{}{
					anthropicDocBlock{
						Type: "document",
						Source: anthropicDocSource{
							Type:      "base64",
							MediaType: "application/pdf",
							Data:      encoded,
						},
					},
					anthropicTextBlock{Type: "text", Text: extractionUserPrompt},
				},
			},
		},
	}

	return h.callAnthropic(ctx, reqBody, true)
}

// extractFromText sends pre-extracted plain text (from .txt, .csv, or Excel)
// to Claude as a text-only message — no document block, no PDF beta header.
func (h *Handler) extractFromText(ctx context.Context, text string) (*parseDocumentResponse, error) {
	userMsg := "The following is the content of a broker OM document:\n\n---\n" + text + "\n---\n\n" + extractionUserPrompt

	reqBody := anthropicDocumentRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096, // unit mix tables can be verbose
		System:    extractionSystemPrompt,
		Messages: []anthropicDocumentMsg{
			{
				Role:    "user",
				Content: []interface{}{anthropicTextBlock{Type: "text", Text: userMsg}},
			},
		},
	}

	return h.callAnthropic(ctx, reqBody, false)
}

// callAnthropic sends the request to the Anthropic Messages API and parses the
// extraction JSON from the first text content block in the response.
// setPDFBeta adds the anthropic-beta: pdfs-2024-09-25 header (PDF path only).
func (h *Handler) callAnthropic(ctx context.Context, reqBody anthropicDocumentRequest, setPDFBeta bool) (*parseDocumentResponse, error) {
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
	if setPDFBeta {
		httpReq.Header.Set("anthropic-beta", "pdfs-2024-09-25")
	}

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

	return parseExtractionJSON(rawText)
}

// parseExtractionJSON maps a Claude JSON string to a parseDocumentResponse.
// Extracted as a separate function so it can be unit-tested without mocking HTTP.
func parseExtractionJSON(rawJSON string) (*parseDocumentResponse, error) {
	var raw struct {
		Address            *string           `json:"address"`
		City               *string           `json:"city"`
		State              *string           `json:"state"`
		Zip                *string           `json:"zip"`
		PropertyType       *string           `json:"propertyType"`
		Beds               *float64          `json:"beds"`
		Baths              *float64          `json:"baths"`
		Sqft               *int              `json:"sqft"`
		YearBuilt          *int              `json:"yearBuilt"`
		Units              *int              `json:"units"`
		AskingPrice        *float64          `json:"askingPrice"`
		BrokerRentCurrent  *float64          `json:"brokerRentCurrent"`
		BrokerRentProForma *float64          `json:"brokerRentProForma"`
		BrokerRentMarket   *float64          `json:"brokerRentMarket"`
		BrokerCapRate      *float64          `json:"brokerCapRate"`
		VacancyRate        *float64          `json:"vacancyRate"`
		UnitMix            []UnitMixRow      `json:"unitMix"`
		LotSqft            *int              `json:"lotSqft"`
		BuildingCount      *int              `json:"buildingCount"`
		DownPaymentPct     *float64          `json:"downPaymentPct"`
		InterestRate       *float64          `json:"interestRate"`
		FinancingType      *string           `json:"financingType"`
		Confidence         map[string]string `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return nil, fmt.Errorf("parse extracted JSON: %w", err)
	}

	// extracted = true when at least one substantive field was found.
	extracted := raw.Address != nil || raw.AskingPrice != nil ||
		raw.BrokerRentCurrent != nil || raw.BrokerRentProForma != nil || raw.BrokerRentMarket != nil ||
		raw.City != nil || raw.Beds != nil || raw.Sqft != nil || raw.BrokerCapRate != nil ||
		len(raw.UnitMix) > 0

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
		UnitMix:            raw.UnitMix,
		LotSqft:            raw.LotSqft,
		BuildingCount:      raw.BuildingCount,
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
