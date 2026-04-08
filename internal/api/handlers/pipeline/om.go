// Package pipeline — OM file upload/download and pipeline decision memo (ADR-105).
package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	anthropic "github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/market/unified"
	"github.com/estara-ai/www/pkg/httputil"
)

// omFilesBaseDir returns the directory to store OM files.
// Priority: RAILWAY_VOLUME_MOUNT_PATH (auto-injected by Railway) → OM_FILES_DIR (local dev).
// Falls back to "" if neither is set (BYTEA fallback mode).
func omFilesBaseDir() string {
	if d := os.Getenv("RAILWAY_VOLUME_MOUNT_PATH"); d != "" {
		return filepath.Join(d, "om-files")
	}
	if d := os.Getenv("OM_FILES_DIR"); d != "" {
		return d
	}
	return ""
}

// UploadOMFile handles POST /api/pipeline/deals/{dealId}/properties/{propId}/om-file
// Accepts a multipart upload (PDF, Excel, text). Parses the OM with Claude and stores:
//   - Parsed OMData in om_data JSONB
//   - broker_cap_rate typed column
//   - File bytes on Railway volume (om_file_path) or BYTEA fallback (om_file_data)
func (h *Handler) UploadOMFile(w http.ResponseWriter, r *http.Request) {
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

	// Verify ownership.
	prop, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property not found")
		return
	}

	// Parse multipart form — 10 MB limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+1024)
	if err := r.ParseMultipartForm(maxDocumentSize); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.Error(w, http.StatusRequestEntityTooLarge, "File exceeds the 10MB limit.")
			return
		}
		httputil.BadRequest(w, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer file.Close()

	if header.Size > maxDocumentSize {
		httputil.Error(w, http.StatusRequestEntityTooLarge, "File exceeds the 10MB limit.")
		return
	}

	// Detect MIME type.
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
		httputil.BadRequest(w, "Unsupported file type. Upload PDF, Excel (.xlsx/.xls), or text (.txt/.csv).")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		h.logger.Error("failed to read OM file", "error", err)
		httputil.InternalError(w, fmt.Errorf("read file: %w", err))
		return
	}

	// 1. Parse OM with Claude (extract basic fields + full OMData).
	var omDataJSON []byte
	var brokerCapRate *float64
	var parsedUploadOM omData

	if h.cfg.AI.AnthropicAPIKey != "" {
		basicResult, err := h.extractDocumentFields(r.Context(), fileBytes, mediaType)
		if err != nil {
			h.logger.Warn("basic OM extraction failed", "error", err)
		} else if basicResult != nil {
			brokerCapRate = basicResult.BrokerCapRate
		}

		// Full OM extraction — two-pass (ADR-108).
		omExtracted, err := h.extractFullOMData(r.Context(), fileBytes, mediaType)
		if err != nil {
			if _, ok := err.(*omOutOfScopeError); ok {
				// Out-of-scope for an existing property upload: store the file but skip extraction.
				h.logger.Warn("out-of-scope OM uploaded to existing property", "propId", propID, "error", err)
			} else {
				h.logger.Warn("full OM extraction failed", "error", err)
			}
		} else if omExtracted != nil {
			parsedUploadOM = *omExtracted
			if b, jerr := json.Marshal(omExtracted); jerr == nil {
				omDataJSON = b
			}
		}
	}

	// 2. Store file — volume preferred, BYTEA fallback.
	var omFilePath pgtype.Text
	var omFileData []byte
	baseDir := omFilesBaseDir()

	if baseDir != "" {
		// Ensure directory exists.
		dir := filepath.Join(baseDir, prop.PipelineDealID.String(), propID.String())
		if err := os.MkdirAll(dir, 0755); err != nil {
			h.logger.Error("failed to create OM file directory", "error", err, "dir", dir)
			// Fall back to BYTEA rather than failing the request.
			omFileData = fileBytes
		} else {
			// Safe filename: timestamp + sanitized original.
			safeFilename := fmt.Sprintf("%d_%s", time.Now().Unix(), sanitizeFilename(header.Filename))
			fullPath := filepath.Join(dir, safeFilename)
			if err := os.WriteFile(fullPath, fileBytes, 0644); err != nil {
				h.logger.Error("failed to write OM file to volume", "error", err, "path", fullPath)
				omFileData = fileBytes
			} else {
				// Store relative path (relative to baseDir).
				rel, _ := filepath.Rel(baseDir, fullPath)
				omFilePath = pgtype.Text{String: rel, Valid: true}
			}
		}
	} else {
		slog.Warn("OM_FILES_DIR and RAILWAY_VOLUME_MOUNT_PATH not set — using BYTEA fallback for OM storage")
		omFileData = fileBytes
	}

	// 3. Update property with OM data.
	updated, err := h.store.Q().UpdatePipelinePropertyOM(r.Context(), queries.UpdatePipelinePropertyOMParams{
		ID:            propID,
		OmData:        omDataJSON,
		BrokerCapRate: numericFromFloat(brokerCapRate),
		OmFilePath:    omFilePath,
		OmFileData:    omFileData,
		OmFileName:    pgtype.Text{String: header.Filename, Valid: true},
		OmFileType:    pgtype.Text{String: mediaType, Valid: true},
	})
	if err != nil {
		h.logger.Error("UpdatePipelinePropertyOM failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Populate null structured columns from OM extraction (non-destructive: only fills blanks).
	if len(omDataJSON) > 0 {
		inferredType := inferPropertyTypeFromOM(parsedUploadOM)
		propType := pgtype.Text{Valid: false}
		if !updated.PropertyType.Valid && inferredType != "" {
			propType = textVal(inferredType)
		}
		propNotes := pgtype.Text{Valid: false}
		if !updated.Notes.Valid && parsedUploadOM.PropertyDescription != "" {
			propNotes = textVal(parsedUploadOM.PropertyDescription)
		}
		if propType.Valid || propNotes.Valid {
			if enriched, err2 := h.store.Q().UpdatePipelineProperty(r.Context(), queries.UpdatePipelinePropertyParams{
				ID:           propID,
				PropertyType: propType,
				Notes:        propNotes,
			}); err2 == nil {
				updated = enriched
			}
		}
	}

	// ADR-106: Kick off OM validation in background (non-fatal).
	// Validation calls Claude to identify missing required fields and generates questions.
	if h.cfg.AI.AnthropicAPIKey != "" && len(omDataJSON) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if _, err := h.runOMValidation(ctx, propID, userID, omDataJSON); err != nil {
				h.logger.Warn("background OM validation failed", "propId", propID, "error", err)
			}
		}()
	}

	// Return the parsed OM data + basic file info (no BYTEA in response).
	httputil.Success(w, map[string]any{
		"property": mapPropertyToResponse(updated),
		"omData":   json.RawMessage(omDataJSON),
		"fileName": header.Filename,
		"fileType": mediaType,
	})
}

// GetOMFile handles GET /api/pipeline/deals/{dealId}/properties/{propId}/om-file
// Streams the OM file with correct Content-Type. Supports Range requests (for PDF iframe paging).
func (h *Handler) GetOMFile(w http.ResponseWriter, r *http.Request) {
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

	row, err := h.store.Q().GetPropertyOMFile(r.Context(), queries.GetPropertyOMFileParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property or OM file not found")
		return
	}

	// Determine content type.
	contentType := "application/octet-stream"
	if row.OmFileType.Valid && row.OmFileType.String != "" {
		contentType = row.OmFileType.String
	}

	filename := "om-file"
	if row.OmFileName.Valid && row.OmFileName.String != "" {
		filename = row.OmFileName.String
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	w.Header().Set("Content-Type", contentType)

	// Serve from volume path if available.
	if row.OmFilePath.Valid && row.OmFilePath.String != "" {
		baseDir := omFilesBaseDir()
		if baseDir != "" {
			fullPath := filepath.Join(baseDir, row.OmFilePath.String)
			f, err := os.Open(fullPath)
			if err != nil {
				h.logger.Error("failed to open OM file from volume", "error", err, "path", fullPath)
				httputil.InternalError(w, fmt.Errorf("open file: %w", err))
				return
			}
			defer f.Close()

			fi, err := f.Stat()
			if err != nil {
				http.ServeContent(w, r, filename, time.Time{}, f)
				return
			}
			http.ServeContent(w, r, filename, fi.ModTime(), f)
			return
		}
	}

	// BYTEA fallback.
	if len(row.OmFileData) > 0 {
		http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(row.OmFileData))
		return
	}

	httputil.NotFound(w, "OM file data not found")
}

// sanitizeFilename returns a safe filename for filesystem storage.
// inferPropertyTypeFromOM returns the most likely property type from extracted OM signals.
// Returns "" if the type cannot be reliably determined.
func inferPropertyTypeFromOM(om omData) string {
	// Count units from rent roll
	unitCount := 0
	for _, row := range om.RentByUnitType {
		if row.Count != nil {
			unitCount += *row.Count
		}
	}
	if unitCount > 1 {
		return "multifamily"
	}
	// Cap rate + NOI without a residential unit mix → commercial/office
	if (om.CapRate != nil || om.BrokerNOI != nil) && len(om.RentByUnitType) == 0 {
		return "commercial"
	}
	// Single residential unit with rent stated → SFH
	if unitCount == 1 && len(om.RentByUnitType) > 0 {
		return "sfh"
	}
	return ""
}

func sanitizeFilename(name string) string {
	// Keep only alphanumeric, dots, dashes, underscores.
	var sb strings.Builder
	for _, r := range filepath.Base(name) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if s == "" {
		return "om-file"
	}
	return s
}

// ---------------------------------------------------------------------------
// ADR-107: OM-first deal creation
// ---------------------------------------------------------------------------

// createDealFromOMResponse is returned by CreateDealFromOM.
type createDealFromOMResponse struct {
	DealID                   string `json:"dealId"`
	PropID                   string `json:"propId"`
	OMValidationStatus       string `json:"omValidationStatus"`
	DocumentClass            string `json:"documentClass,omitempty"`
	PropertyType             string `json:"propertyType,omitempty"`
	ClassificationConfidence string `json:"classificationConfidence,omitempty"`
	NeedsReview              bool   `json:"needsReview,omitempty"` // Pass 1 confidence = "low"
}

// ─── OM duplicate detection ──────────────────────────────────────────────────

// omDuplicateCheckRequest accepts key identifiers extracted from an OM.
// All fields are optional — empty string means "skip this criterion".
type omDuplicateCheckRequest struct {
	BrokerEmail         string `json:"brokerEmail"`
	OmDate              string `json:"omDate"`
	BrokerCompany       string `json:"brokerCompany"`
	PropertyDescription string `json:"propertyDescription"`
}

// omDuplicateProperty is one address within a matched deal.
type omDuplicateProperty struct {
	PropID  string `json:"propId"`
	Address string `json:"address"`
}

// omDuplicateDeal groups all matching properties under a single deal.
type omDuplicateDeal struct {
	DealID        string                `json:"dealId"`
	DealName      string                `json:"dealName"`
	Properties    []omDuplicateProperty `json:"properties"`
	MatchCriteria []string              `json:"matchCriteria"` // human-readable reasons
	BrokerName    string                `json:"brokerName,omitempty"`
	BrokerCompany string                `json:"brokerCompany,omitempty"`
	OmDate        string                `json:"omDate,omitempty"`
	CreatedAt     string                `json:"createdAt"`
}

// strIface safely converts an interface{} JSONB text result to string.
func strIface(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// CheckOMDuplicate handles POST /api/pipeline/check-duplicate-om
// Accepts extracted OM identifiers and returns matching active deals for the user.
// This is called client-side after extraction (before deal creation) to surface duplicates.
func (h *Handler) CheckOMDuplicate(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req omDuplicateCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// All criteria empty → nothing to check.
	if req.BrokerEmail == "" && req.OmDate == "" && req.BrokerCompany == "" && req.PropertyDescription == "" {
		httputil.Success(w, []omDuplicateDeal{})
		return
	}

	rows, err := h.store.Q().FindOMDuplicates(r.Context(), queries.FindOMDuplicatesParams{
		UserID:        userID,
		BrokerEmail:   req.BrokerEmail,
		OmDate:        req.OmDate,
		BrokerCompany: req.BrokerCompany,
		PropertyDesc:  req.PropertyDescription,
	})
	if err != nil {
		// Query failure is non-fatal — return empty list.
		h.logger.Warn("FindOMDuplicates query failed", "error", err)
		httputil.Success(w, []omDuplicateDeal{})
		return
	}

	// Group rows by deal, accumulate properties and match criteria.
	type dealKey = string // deal UUID string
	dealOrder := make([]dealKey, 0)
	dealMap := make(map[dealKey]*omDuplicateDeal)

	for _, row := range rows {
		key := row.DealID.String()
		if _, seen := dealMap[key]; !seen {
			dealOrder = append(dealOrder, key)

			brokerName    := strIface(row.BrokerName)
			brokerCompany := strIface(row.BrokerCompany)
			omDate        := strIface(row.OmDate)
			brokerEmail   := strIface(row.BrokerEmail)

			// Determine which criteria caused this match.
			var criteria []string
			if req.BrokerEmail != "" && brokerEmail == req.BrokerEmail {
				criteria = append(criteria, "same broker email")
			}
			if req.OmDate != "" && req.BrokerCompany != "" && omDate == req.OmDate && brokerCompany == req.BrokerCompany {
				criteria = append(criteria, "same OM date + broker firm")
			} else if req.OmDate != "" && omDate == req.OmDate {
				criteria = append(criteria, "same OM date")
			}
			if req.PropertyDescription != "" && (strings.EqualFold(row.Address, req.PropertyDescription) || strings.Contains(strings.ToLower(row.Address), strings.ToLower(req.PropertyDescription))) {
				criteria = append(criteria, "same property description")
			}
			if len(criteria) == 0 {
				criteria = append(criteria, "similar OM data")
			}

			dealMap[key] = &omDuplicateDeal{
				DealID:        key,
				DealName:      row.DealName,
				Properties:    []omDuplicateProperty{},
				MatchCriteria: criteria,
				BrokerName:    brokerName,
				BrokerCompany: brokerCompany,
				OmDate:        omDate,
				CreatedAt:     row.DealCreatedAt.UTC().Format(time.RFC3339),
			}
		}
		dealMap[key].Properties = append(dealMap[key].Properties, omDuplicateProperty{
			PropID:  row.PropID.String(),
			Address: row.Address,
		})
	}

	// Flatten into ordered slice.
	result := make([]omDuplicateDeal, 0, len(dealOrder))
	for _, key := range dealOrder {
		result = append(result, *dealMap[key])
	}

	httputil.Success(w, result)
}

// GetOMExtraction handles GET /api/pipeline/deals/{dealId}/om-extraction
// Returns the stored OM data + re-computed validation issues for resume mode.
func (h *Handler) GetOMExtraction(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	// Verify deal ownership.
	if _, err := h.store.Q().GetPipelineDeal(r.Context(), queries.GetPipelineDealParams{
		ID:     dealID,
		UserID: userID,
	}); err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	// Get properties and find the first one with stored OM data.
	props, err := h.store.Q().ListPipelineProperties(r.Context(), queries.ListPipelinePropertiesParams{
		PipelineDealID: dealID,
		UserID:         userID,
	})
	if err != nil {
		httputil.InternalError(w, err)
		return
	}

	for _, prop := range props {
		if len(prop.OmData) == 0 {
			continue
		}

		var d omData
		if jerr := json.Unmarshal(prop.OmData, &d); jerr != nil {
			continue
		}

		issues := computeOMValidationIssues(&d)

		fileName := ""
		if prop.OmFileName.Valid {
			fileName = prop.OmFileName.String
		}

		httputil.Success(w, map[string]any{
			"propId":           prop.ID,
			"fileName":         fileName,
			"extraction":       &d,
			"validationIssues": issues,
		})
		return
	}

	httputil.NotFound(w, "no OM data found for this deal")
}

// CreateDealFromOM handles POST /api/pipeline/deals/from-om
// Accepts a multipart OM file, creates deal + property atomically, runs OM validation.
func (h *Handler) CreateDealFromOM(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	// Parse multipart form — 10 MB limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+1024)
	if err := r.ParseMultipartForm(maxDocumentSize); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.Error(w, http.StatusRequestEntityTooLarge, "File exceeds the 10MB limit.")
			return
		}
		httputil.BadRequest(w, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer file.Close()

	if header.Size > maxDocumentSize {
		httputil.Error(w, http.StatusRequestEntityTooLarge, "File exceeds the 10MB limit.")
		return
	}

	// Detect MIME type.
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
		httputil.BadRequest(w, "Unsupported file type. Upload PDF, Excel (.xlsx/.xls), or text (.txt/.csv).")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		h.logger.Error("failed to read OM file", "error", err)
		httputil.InternalError(w, fmt.Errorf("read file: %w", err))
		return
	}

	// ADR-107 (revised): client may pass pre-extracted OM data (from ExtractOM step).
	// If provided, skip Claude re-extraction. Otherwise fall back to running extraction.
	inputComplete := r.FormValue("inputComplete") == "true"
	clientDealName := r.FormValue("dealName")
	clientExtractionJSON := r.FormValue("extraction")

	var omDataJSON []byte
	var brokerCapRate *float64
	var extractedAddress string
	var parsedOM omData // populated from whichever extraction path runs

	if clientExtractionJSON != "" {
		// Use client-provided extraction (user may have edited it).
		omDataJSON = []byte(clientExtractionJSON)
		if jerr := json.Unmarshal(omDataJSON, &parsedOM); jerr == nil {
			if parsedOM.CapRate != nil {
				brokerCapRate = parsedOM.CapRate
			}
			if parsedOM.PropertyAddress != "" {
				extractedAddress = parsedOM.PropertyAddress
			} else if parsedOM.PropertyDescription != "" {
				extractedAddress = parsedOM.PropertyDescription
			}
			if len(extractedAddress) > 80 {
				extractedAddress = extractedAddress[:80]
			}
		}
	} else if h.cfg.AI.AnthropicAPIKey != "" {
		basicResult, err := h.extractDocumentFields(r.Context(), fileBytes, mediaType)
		if err != nil {
			h.logger.Warn("basic OM extraction failed in CreateDealFromOM", "error", err)
		} else if basicResult != nil {
			brokerCapRate = basicResult.BrokerCapRate
		}

		omExtracted, err := h.extractFullOMData(r.Context(), fileBytes, mediaType)
		if err != nil {
			// ADR-108: out-of-scope documents are rejected before deal creation.
			if outOfScope, ok := err.(*omOutOfScopeError); ok {
				httputil.Error(w, http.StatusUnprocessableEntity, outOfScope.Error())
				return
			}
			h.logger.Warn("full OM extraction failed in CreateDealFromOM", "error", err)
		} else if omExtracted != nil {
			parsedOM = *omExtracted
			if b, jerr := json.Marshal(omExtracted); jerr == nil {
				omDataJSON = b
			}
			if omExtracted.CapRate != nil {
				brokerCapRate = omExtracted.CapRate
			}
			if omExtracted.PropertyAddress != "" {
				extractedAddress = omExtracted.PropertyAddress
			} else if omExtracted.PropertyDescription != "" {
				extractedAddress = omExtracted.PropertyDescription
			}
			if len(extractedAddress) > 80 {
				extractedAddress = extractedAddress[:80]
			}
		}
	}

	dealName := "New Deal"
	if clientDealName != "" {
		dealName = clientDealName
	} else if parsedOM.PropertyTitle != "" {
		dealName = parsedOM.PropertyTitle
	} else if extractedAddress != "" {
		dealName = extractedAddress
	}

	// Create the deal.
	deal, err := h.store.Q().CreatePipelineDeal(r.Context(), queries.CreatePipelineDealParams{
		UserID:            userID,
		Name:              dealName,
		Source:            "broker",
		Notes:             pgtype.Text{Valid: false},
		PortfolioExcluded: false,
	})
	if err != nil {
		h.logger.Error("CreatePipelineDeal failed in CreateDealFromOM", "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Create the property — populate ALL structured columns from OM extraction so the edit form
	// is fully pre-filled regardless of how the deal was created (OM upload == manual entry
	// in terms of data model; entry method differs, data does not).
	propAddress := "See OM"
	if extractedAddress != "" {
		propAddress = extractedAddress
	}
	// Parse street/city/state/zip from the extracted address string.
	// When successful, propAddress is narrowed to just the street portion so that
	// the address, city, state, and zip wizard fields don't duplicate the same data.
	propStreet, propCity, propState, propZip := parseUSAddressParts(propAddress)
	if propStreet != "" {
		propAddress = propStreet
	}

	// ADR-108: use extracted propertyType from Pass 1 if available, else fall back to heuristic.
	extractedPropType := ""
	if parsedOM.PropertyType != nil && *parsedOM.PropertyType != "" && *parsedOM.PropertyType != "other" {
		extractedPropType = *parsedOM.PropertyType
	} else {
		extractedPropType = inferPropertyTypeFromOM(parsedOM)
	}

	// Broker rent fallback chain: GrossPotentialRentCurrent → GrossPotentialRent.
	brokerRentOM := parsedOM.GrossPotentialRentCurrent
	if brokerRentOM == nil {
		brokerRentOM = parsedOM.GrossPotentialRent
	}

	// Income fallback chains — prefer full P&L fields (ADR-108) over legacy summary fields.
	omGPR        := omCoalesceFloat(parsedOM.GrossPotentialRentCurrent, parsedOM.GrossPotentialRent)
	omEGI        := omCoalesceFloat(parsedOM.TotalEffectiveGrossIncome, parsedOM.EffectiveGrossIncome)
	omBrokerNOI  := omCoalesceFloat(parsedOM.NOICurrent, parsedOM.BrokerNOI)
	omBrokerNOIStab := omCoalesceFloat(parsedOM.NOIProforma, parsedOM.BrokerNOIStabilized)
	omVacancyPct := omCoalesceFloat(parsedOM.VacancyPct, parsedOM.VacancyAssumption)
	omYr1CoC     := omCoalesceFloat(parsedOM.YearOneCashOnCash, parsedOM.CashOnCashCurrent)

	// Assumable debt: prefer structured detail, fall back to legacy flat amount.
	var omAssumableBalance, omAssumableRate *float64
	if parsedOM.AssumableDebtDetail != nil {
		omAssumableBalance = &parsedOM.AssumableDebtDetail.Balance
		omAssumableRate    = &parsedOM.AssumableDebtDetail.InterestRate
	} else {
		omAssumableBalance = parsedOM.AssumableDebt
	}

	// Unit count: prefer rent roll sum; fall back to totalUnits (directly extracted or
	// computed by Claude from "N x M-Unit Buildings" descriptions).
	omUnits := omComputeUnits(parsedOM.RentByUnitType)
	if omUnits == nil {
		omUnits = parsedOM.TotalUnits
	}

	// OmDate: dereference *string safely.
	omDateStr := strPtrVal(parsedOM.OmDate)

	prop, err := h.store.Q().CreatePipelineProperty(r.Context(), queries.CreatePipelinePropertyParams{
		PipelineDealID: deal.ID,
		Address:        propAddress,
		City:           textVal(propCity),
		State:          textVal(propState),
		Zip:            textVal(propZip),
		SourceType:     "document_upload",
		PropertyType:   textVal(extractedPropType),
		Notes:          textVal(parsedOM.PropertyDescription),
		Description:    textVal(parsedOM.PropertyDescription),
		Units:          int4FromGoIntPtr(omUnits),
		BuildingCount:  int4FromGoIntPtr(parsedOM.BuildingCount),
		AskingPrice:    numericFromFloatPtr(parsedOM.AskingPrice),
		BrokerRent:     numericFromFloatPtr(brokerRentOM),
		Sqft:           int4FromFloat64Ptr(omCoalesceFloat(parsedOM.RentableSF, parsedOM.BuildingSqft)),
		LotSqft:        int4FromFloat64Ptr(parsedOM.LotSqft),
		YearBuilt:      int4FromGoIntPtr(parsedOM.YearBuilt),
		YearRenovated:  int4FromGoIntPtr(parsedOM.YearRenovated),
		Stories:        int4FromGoIntPtr(parsedOM.Stories),
		Zoning:         textFromPtr(parsedOM.Zoning),
		Construction:   textFromPtr(parsedOM.Construction),
		ParkingSpaces:  int4FromGoIntPtr(parsedOM.ParkingSpaces),
		Parking:        textFromPtr(parsedOM.Parking),
		// Income statement
		CurrentOccupancy:     numericFromFloatPtr(parsedOM.CurrentOccupancy),
		GrossPotentialRent:   numericFromFloatPtr(omGPR),
		EffectiveGrossIncome: numericFromFloatPtr(omEGI),
		BrokerNoi:            numericFromFloatPtr(omBrokerNOI),
		BrokerNoiStabilized:  numericFromFloatPtr(omBrokerNOIStab),
		VacancyPct:           numericFromFloatPtr(omVacancyPct),
		VacancyLabel:         textFromPtr(parsedOM.VacancyLabel),
		OmDate:               textVal(omDateStr),
		// Return metrics
		CapRateProForma: numericFromFloatPtr(parsedOM.CapRateProForma),
		BrokerGrm:       numericFromFloatPtr(parsedOM.GRM),
		BrokerDscr:      numericFromFloatPtr(parsedOM.DSCR),
		Broker5yrIrr:    numericFromFloatPtr(parsedOM.FiveYearIRR),
		BrokerYr1Coc:    numericFromFloatPtr(omYr1CoC),
		// Assumable debt
		AssumableDebtBalance: numericFromFloatPtr(omAssumableBalance),
		AssumableDebtRate:    numericFromFloatPtr(omAssumableRate),
		// JSONB — rent roll, expenses, broker, highlights
		UnitMix:              omRentToUnitMixJSON(parsedOM.RentByUnitType),
		CommercialMix:        omTenantsJSON(parsedOM.TenantSchedule),
		ExpenseItems:         omExpenseItemsJSON(parsedOM.ExpenseItems),
		BrokerContact:        omBrokerContactJSON(parsedOM.BrokerContact),
		InvestmentHighlights: omHighlightsJSON(parsedOM.InvestmentHighlights),
		// ADR-113: progressive disclosure typed columns
		OtherIncomeItems:           omOtherIncomeJSON(parsedOM.OtherIncomeItems),
		RenovationCost:             numericFromFloatPtr(parsedOM.RenovationCost),
		ClaimedRenovationNoiUplift: numericFromFloatPtr(parsedOM.ClaimedRenovationNOIUplift),
		ValueAddData:               omValueAddJSON(parsedOM.ValueAdd),
		BuildingAmenities:          parsedOM.BuildingAmenities,
		MarketOverviewText:         textVal(parsedOM.MarketOverviewText),
	})
	if err != nil {
		h.logger.Error("CreatePipelineProperty failed in CreateDealFromOM", "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Store OM file.
	var omFilePath pgtype.Text
	var omFileData []byte
	baseDir := omFilesBaseDir()

	if baseDir != "" {
		dir := filepath.Join(baseDir, deal.ID.String(), prop.ID.String())
		if err := os.MkdirAll(dir, 0755); err != nil {
			h.logger.Error("failed to create OM file directory", "error", err, "dir", dir)
			omFileData = fileBytes
		} else {
			safeFilename := fmt.Sprintf("%d_%s", time.Now().Unix(), sanitizeFilename(header.Filename))
			fullPath := filepath.Join(dir, safeFilename)
			if err := os.WriteFile(fullPath, fileBytes, 0644); err != nil {
				h.logger.Error("failed to write OM file to volume", "error", err, "path", fullPath)
				omFileData = fileBytes
			} else {
				rel, _ := filepath.Rel(baseDir, fullPath)
				omFilePath = pgtype.Text{String: rel, Valid: true}
			}
		}
	} else {
		omFileData = fileBytes
	}

	// Update property with OM data + file.
	_, err = h.store.Q().UpdatePipelinePropertyOM(r.Context(), queries.UpdatePipelinePropertyOMParams{
		ID:            prop.ID,
		OmData:        omDataJSON,
		BrokerCapRate: numericFromFloat(brokerCapRate),
		OmFilePath:    omFilePath,
		OmFileData:    omFileData,
		OmFileName:    pgtype.Text{String: header.Filename, Valid: true},
		OmFileType:    pgtype.Text{String: mediaType, Valid: true},
	})
	if err != nil {
		h.logger.Warn("UpdatePipelinePropertyOM failed in CreateDealFromOM", "error", err)
	}

	// Compute and store property_completeness now that om_data is set.
	// prop was returned before om_data was written, so we attach it here for the calculation.
	propWithOM := prop
	propWithOM.OmData = omDataJSON
	if brokerCapRate != nil {
		propWithOM.BrokerCapRate = numericFromFloat(brokerCapRate)
	}
	completeness := computePropertyCompleteness(propWithOM)
	if cerr := h.store.Q().UpdatePropertyCompleteness(r.Context(), queries.UpdatePropertyCompletenessParams{
		ID:                   prop.ID,
		PropertyCompleteness: completeness,
	}); cerr != nil {
		h.logger.Warn("UpdatePropertyCompleteness failed in CreateDealFromOM", "error", cerr)
	}

	// Bump deal property count.
	_ = h.store.Q().BumpPipelineDealActivity(r.Context(), deal.ID)

	// Pass 3: validate extraction quality asynchronously.
	// Uses keyFacts captured during Pass 2 — no PDF re-read needed.
	// Result stored in extraction_issues; wizard surfaces warnings to user.
	if len(parsedOM.KeyFacts) > 0 && h.cfg.AI.AnthropicAPIKey != "" {
		propID := prop.ID
		keyFacts := parsedOM.KeyFacts
		extracted := parsedOM
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			issues := h.validateOMExtraction(ctx, keyFacts, &extracted)
			if len(issues) == 0 {
				return
			}
			issuesJSON, err := json.Marshal(issues)
			if err != nil {
				return
			}
			if err := h.store.Q().UpdatePropertyExtractionIssues(ctx, queries.UpdatePropertyExtractionIssuesParams{
				ID:               propID,
				ExtractionIssues: issuesJSON,
			}); err != nil {
				h.logger.Warn("UpdatePropertyExtractionIssues failed", "error", err)
			}
		}()
	}

	// ADR-107: if inputComplete, mark the deal now.
	if inputComplete {
		if _, err := h.store.Q().MarkDealInputComplete(r.Context(), queries.MarkDealInputCompleteParams{
			ID:     deal.ID,
			UserID: userID,
		}); err != nil {
			h.logger.Warn("MarkDealInputComplete failed", "error", err)
		}
	}

	// Build response with classification metadata.
	resp := createDealFromOMResponse{
		DealID:             deal.ID.String(),
		PropID:             prop.ID.String(),
		OMValidationStatus: "not_uploaded",
	}
	if parsedOM.DocumentClass != nil {
		resp.DocumentClass = *parsedOM.DocumentClass
	}
	if parsedOM.PropertyType != nil {
		resp.PropertyType = *parsedOM.PropertyType
	}
	if parsedOM.ClassificationConfidence != nil {
		resp.ClassificationConfidence = *parsedOM.ClassificationConfidence
		resp.NeedsReview = *parsedOM.ClassificationConfidence == "low"
	}
	httputil.Created(w, resp)
}

// ---------------------------------------------------------------------------
// Full OM data extraction (flexible-first, ADR-105)
// ---------------------------------------------------------------------------

// omData mirrors the OMData TypeScript interface on the client.
// All fields are nullable — Claude populates whatever the OM contains.
// ADR-108: backward compat — all existing fields retained; new fields added.
type omData struct {
	// Classification (Pass 1 results — ADR-108)
	DocumentClass             *string `json:"documentClass,omitempty"`
	PropertyType              *string `json:"propertyType,omitempty"` // extracted, not inferred
	ClassificationConfidence  *string `json:"classificationConfidence,omitempty"`

	// Must-haves
	BrokerContact *omBrokerContact `json:"brokerContact"`
	OmDate        *string          `json:"omDate"`
	AskingPrice   *float64         `json:"askingPrice"`
	CapRate       *float64         `json:"capRate"`
	RentalYield   *float64         `json:"rentalYield"`

	// Common but not universal
	PropertyTitle        string   `json:"propertyTitle,omitempty"`       // named title on OM cover (e.g. "Victorian Village Portfolio — 18 Units", "The Gulch on 12th")
	PropertyAddress      string   `json:"propertyAddress,omitempty"`     // street address (e.g. "123 Main St, Garden City, NY 11530")
	PropertyDescription  string   `json:"propertyDescription,omitempty"` // marketing tagline / summary
	InvestmentHighlights []string `json:"investmentHighlights,omitempty"`
	YearBuilt            *int     `json:"yearBuilt"`     // year originally constructed
	YearRenovated        *int     `json:"yearRenovated"` // year of most recent renovation
	Stories              *int     `json:"stories"`
	Zoning               *string  `json:"zoning"`
	Construction         *string  `json:"construction"`
	Parking              *string  `json:"parking"`
	BuildingAmenities    []string `json:"buildingAmenities,omitempty"`
	LotSqft              *float64 `json:"lotSqft"`      // lot area in sqft
	BuildingSqft         *float64 `json:"buildingSqft"` // gross building area in sqft

	// Physical detail — ADR-108 additions
	BuildingCount *int     `json:"buildingCount,omitempty"` // number of buildings in the portfolio
	TotalUnits    *int     `json:"totalUnits,omitempty"`    // total rental units (may be computed from N×M-unit buildings)

	// Pass 3 validation support: verbatim key facts captured during extraction.
	// Used by validateOMExtraction to check for obvious misses without re-reading the PDF.
	KeyFacts []string `json:"keyFacts,omitempty"`
	RentableSF   *float64 `json:"rentableSF,omitempty"`   // net rentable area
	AvgUnitSF    *float64 `json:"avgUnitSF,omitempty"`    // avg unit size
	ParkingSpaces *int    `json:"parkingSpaces,omitempty"` // total parking stalls
	WalkScore    *int     `json:"walkScore,omitempty"`

	// Rent by unit type — one row per unit type (Studio, 1bd, 2bd, …).
	RentByUnitType []omRentByUnitType `json:"rentByUnitType,omitempty"`

	// Financial — existing fields (backward compat)
	BrokerNOI            *float64    `json:"brokerNOI"`
	GrossPotentialRent   *float64    `json:"grossPotentialRent"`
	EffectiveGrossIncome *float64    `json:"effectiveGrossIncome"`
	TotalExpenses        *float64    `json:"totalExpenses"`
	ExpenseLineItems     [][2]string `json:"expenseLineItems,omitempty"` // legacy: [["label","value"],...]

	// Financial — extended (ADR-106)
	CapRateProForma         *float64 `json:"capRateProForma"`
	BrokerNOIStabilized     *float64 `json:"brokerNOIStabilized"`
	VacancyAssumption       *float64 `json:"vacancyAssumption"` // legacy; use VacancyPct for new records
	VacancyLabel            *string  `json:"vacancyLabel"`
	DSCR                    *float64 `json:"dscr"`
	StabilizedCashOnCash    *float64 `json:"stabilizedCashOnCash"` // legacy; use CashOnCashStabilized for new
	FinancingInterestAnnual *float64 `json:"financingInterestAnnual"`

	// Income statement — full P&L (ADR-108)
	CurrentOccupancy           *float64           `json:"currentOccupancy,omitempty"`    // decimal, e.g. 0.944 for 94.4%
	GrossPotentialRentCurrent  *float64           `json:"grossPotentialRentCurrent,omitempty"`
	GrossPotentialRentProforma *float64           `json:"grossPotentialRentProforma,omitempty"`
	VacancyPct                 *float64           `json:"vacancyPct,omitempty"`          // decimal, e.g. 0.05
	OtherIncomeItems           []omOtherIncomeItem `json:"otherIncomeItems,omitempty"`
	TotalEffectiveGrossIncome  *float64           `json:"totalEffectiveGrossIncome,omitempty"`
	ExpenseItems               []omExpenseItem    `json:"expenseItems,omitempty"` // structured expense rows
	ExpenseRatioPct            *float64           `json:"expenseRatioPct,omitempty"`
	NOICurrent                 *float64           `json:"noiCurrent,omitempty"`          // in-place
	NOIProforma                *float64           `json:"noiProforma,omitempty"`         // stabilized
	NOISummaryStated           *float64           `json:"noiSummaryStated,omitempty"`    // from cover/summary
	NOIComputedFromStatement   *float64           `json:"noiComputedFromStatement,omitempty"` // from P&L

	// Valuation — existing fields (backward compat)
	PricePerUnit      *float64 `json:"pricePerUnit"`
	PricePerSF        *float64 `json:"pricePerSF"`
	GRM               *float64 `json:"grm"`
	YearOneCashOnCash *float64 `json:"yearOneCashOnCash"` // legacy; use CashOnCashCurrent
	FiveYearIRR       *float64 `json:"fiveYearIRR"`
	AssumableDebt     *float64 `json:"assumableDebt"` // legacy flat amount; use AssumableDebtDetail for new

	// Return metrics — ADR-108 additions
	CashOnCashCurrent    *float64         `json:"cashOnCashCurrent,omitempty"`
	CashOnCashStabilized *float64         `json:"cashOnCashStabilized,omitempty"`
	AssumableDebtDetail  *omAssumableDebt `json:"assumableDebtDetail,omitempty"` // structured debt info
	ValueAdd             *omValueAdd      `json:"valueAdd,omitempty"`

	// NNN / commercial / office (ADR-108)
	TenantSchedule  []omTenant `json:"tenantSchedule,omitempty"`
	TrafficCountVPD *int       `json:"trafficCountVPD,omitempty"`
	WALR            *float64   `json:"walr,omitempty"` // weighted avg lease remaining (years)

	// Broker-stated market data (ADR-108)
	BrokerMarket *omBrokerMarket `json:"brokerMarket,omitempty"`

	// Type-specific extensibility (ADR-108)
	// self_storage: unit mix; industrial: clear height/dock doors; etc.
	TypeSpecificData map[string]interface{} `json:"typeSpecificData,omitempty"`

	// Market + freeform
	MarketOverviewText string       `json:"marketOverviewText,omitempty"`
	AdditionalMetrics  []omKeyValue `json:"additionalMetrics,omitempty"`
	AdditionalSections []omSection  `json:"additionalSections,omitempty"`

	// Renovation economics — ADR-110
	RenovationCost             *float64 `json:"renovationCost,omitempty"`             // total renovation budget in dollars
	ClaimedRenovationNOIUplift *float64 `json:"claimedRenovationNOIUplift,omitempty"` // incremental NOI increase from renovation (not absolute post-reno NOI)

	// File metadata
	OmFileSizeBytes *int64  `json:"omFileSizeBytes"`
	OmParsedAt      *string `json:"omParsedAt"`
}

// omRentByUnitType holds the three rent tiers for a single unit type.
// rentMarket is a string because OMs often state ranges (e.g. "$750-900").
type omRentByUnitType struct {
	UnitType     string   `json:"unitType"`    // "Studio", "1 Bed", "2 Bed", "3 Bed"
	Bedrooms     int      `json:"bedrooms"`    // 0 = studio, 1, 2, 3, …
	Count        *int     `json:"count"`       // unit count of this type
	SqftPerUnit  *float64 `json:"sqftPerUnit"` // rentable area per unit in sqft
	ParkingSlots *int     `json:"parkingSlots"`
	RentCurrent  *float64 `json:"rentCurrent"`  // broker-stated current rent
	RentProForma *float64 `json:"rentProForma"` // pro forma / projected rent
	RentMarket   *string  `json:"rentMarket"`   // market rent (may be a range string)
	Amenities    []string `json:"amenities,omitempty"`
	// ADR-108: explicit aliases (same data, clearer naming for extraction)
	RentCurrentAvg  *float64 `json:"rentCurrentAvg,omitempty"`
	RentProFormaAvg *float64 `json:"rentProFormaAvg,omitempty"`
	RentMarketAvg   *string  `json:"rentMarketAvg,omitempty"`
}

type omBrokerContact struct {
	Name          *string `json:"name"`
	Title         *string `json:"title"`
	Company       *string `json:"company"`
	Phone         *string `json:"phone"`
	Email         *string `json:"email"`
	LicenseNumber *string `json:"licenseNumber"`
}

// omExpenseItem is a structured expense row (ADR-108 addition; ExpenseLineItems kept for backward compat).
type omExpenseItem struct {
	Label    string   `json:"label"`
	Amount   float64  `json:"amount"`
	PctOfEGI *float64 `json:"pctOfEGI,omitempty"`
}

type omOtherIncomeItem struct {
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
}

// omAssumableDebt is structured debt info (ADR-108 addition; AssumableDebt float kept for backward compat).
type omAssumableDebt struct {
	Balance      float64 `json:"balance"`
	InterestRate float64 `json:"interestRate"`
	MaturityDate *string `json:"maturityDate,omitempty"`
	LenderName   *string `json:"lenderName,omitempty"`
	LoanType     *string `json:"loanType,omitempty"` // "bridge"|"fannie_mae"|"freddie"|"conventional"
}

type omValueAdd struct {
	LowEstimate      *float64 `json:"lowEstimate,omitempty"`
	HighEstimate     *float64 `json:"highEstimate,omitempty"`
	UnrenovatedUnits *int     `json:"unrenovatedUnits,omitempty"`
	CostPerUnit      *float64 `json:"costPerUnit,omitempty"`
}

type omBrokerMarket struct {
	VacancyRate     *float64 `json:"vacancyRate,omitempty"`
	RentGrowthYoY   *float64 `json:"rentGrowthYoY,omitempty"`
	JobGrowth       *int     `json:"jobGrowth,omitempty"`
	NewSupplyUnits  *int     `json:"newSupplyUnits,omitempty"`
	MedianHHI       *float64 `json:"medianHHI,omitempty"`
	WalkScore       *int     `json:"walkScore,omitempty"`
	CapRateComprBps *int     `json:"capRateComprBps,omitempty"`
	MarketNarrative *string  `json:"marketNarrative,omitempty"`
	DataSource      *string  `json:"dataSource,omitempty"` // "CoStar"|"CBRE"|"JLL"|etc.
	SubmarketName   *string  `json:"submarketName,omitempty"`
}

type omTenant struct {
	TenantName             string   `json:"tenantName"`
	SquareFeet             *float64 `json:"squareFeet,omitempty"`
	AnnualRent             *float64 `json:"annualRent,omitempty"`
	LeaseExpiry            *string  `json:"leaseExpiry,omitempty"`
	LeaseType              *string  `json:"leaseType,omitempty"` // "nnn"|"modified_gross"|"gross"
	AnnualRentBumpPct      *float64 `json:"annualRentBumpPct,omitempty"`
	LandlordResponsibility *string  `json:"landlordResponsibility,omitempty"`
	TenantCreditRating     *string  `json:"tenantCreditRating,omitempty"`
	GuarantorName          *string  `json:"guarantorName,omitempty"`
}

type omKeyValue struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type omSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

const omExtractionSystemPrompt = `You are a real estate OM (Offering Memorandum) data extraction assistant.

Your job: extract ALL information from this OM document into structured JSON.

Core rules:
- Extract ONLY what is explicitly stated — never infer, calculate, or assume.
- Return null for any field not present in the document.
- OMs vary enormously: a 2-page flyer may only have price and a photo; a 30-page institutional OM has extensive financials, market comps, and lease schedules. Capture whatever is there.
- For broker contact: extract name, title, company, phone, email, license number if present. A contact entry with no name AND no phone AND no email should be left null.
- yearBuilt = year the property was originally constructed (required; different from yearRenovated).
- yearRenovated = year of most recent major renovation (optional; null if not stated).

PROPERTY TITLE vs ADDRESS vs DESCRIPTION — THREE SEPARATE FIELDS:
- propertyTitle = the BRANDED NAME of the property — a proper noun that functions as the project's identity. Extract this whenever the OM presents a standalone name for the property, even if that name incorporates the street. Recognition rules:
  * ANY name ending in Apartments, Flats, Lofts, Place, Gardens, Plaza, Court, Commons, Portfolio, Residences, Manor, Village, House, Tower, Building, Center, Square → it IS a propertyTitle
  * Names styled as headlines on the cover page or executive summary header → propertyTitle
  * Examples: "Victorian Village Portfolio — 18 Units", "East 6th Street Flats", "The Gulch on 12th", "Parkview Apartments", "Riverside Lofts", "One University Place"
  * NOT a propertyTitle: a plain address like "3814 East 6th Street" with no branding suffix
  * If the OM cover shows BOTH a branded name AND an address, put the branded name in propertyTitle and the address in propertyAddress
  * Do NOT put propertyTitle content into propertyDescription
- propertyAddress = the full street address of the asset (e.g. "123 Main Street, Garden City, NY 11530"). Include street number, street name, city, state, and ZIP if present. This is a physical locator, not a description.
- propertyDescription = the marketing summary or tagline used to describe the asset (e.g. "12-unit value-add garden-style apartment community", "Trophy Class-A office tower with below-market rents", "Value-add multifamily opportunity"). This is typically a sentence describing the asset class or investment thesis — NOT the property's proper name and NOT its street address.
- If the OM only states an address with no separate marketing description, leave propertyDescription null.
- If the OM only states a description with no parseable street address, leave propertyAddress null.
- Never combine address and description into a single field.

LOT SIZE AND BUILDING SIZE — EXTRACT IN SQUARE FEET:
- lotSqft = total lot / land area in square feet. Look for labels like "Lot", "Land Area", "Site Area", "Lot Size", "Land", "Parcel".
  * If stated in acres, multiply by 43,560 (e.g. "- Lot: 1.05 acres" → 45,738; "0.25 acres" → 10,890).
  * If stated in sqft or SF, use as-is (e.g. "Lot: 8,200 SF" → 8200).
  * Result must be a number, not a string. Return null only if no lot/land area is mentioned anywhere.
- buildingSqft = gross building area or net rentable area in square feet. Look for "GBA", "NRA", "Building Size", "Rentable Area", "Gross SF".
  * Use GBA if both GBA and NRA are present.
  * Return null if not stated.

- For financials: extract the full income statement — GPR (current and proforma), vacancy, other income, EGI, all expense line items, NOI.
- For expense line items: use the structured expenseItems array — each item has "label" (string), "amount" (annual dollars, number), and "pctOfEGI" (decimal if stated, else null). Extract EVERY named expense line. Do NOT use expenseLineItems (legacy).
- For other income (laundry, parking, storage, late fees, etc.): use otherIncomeItems array with "label" and "amount".
- noiCurrent = in-place NOI at current rents. noiProforma = stabilized/proforma NOI. noiSummaryStated = NOI from cover summary box.

RENT BY UNIT TYPE — IMPORTANT:
- OMs often have a "Unit Mix" or "Rent Roll" table with one row per unit type.
- Extract ALL rows into rentByUnitType — do not merge rows or skip any.
- Column name mapping (OMs use varied labels — map them as follows):
  * "Current Avg Rent", "In-Place Rent", "Current Rent", "Avg Rent" → rentCurrent (number, monthly)
  * "Proforma Rent", "Pro Forma Rent", "Stabilized Rent", "Projected Rent" → rentProForma (number, monthly)
  * "Market Rent", "Market Comparable", "Market Rate" → rentMarket (string — may be a range)
  * "# Units", "Count", "Units" column → count (integer)
  * "Avg SF", "SF/Unit", "Sqft" column → sqftPerUnit (number)
- Standard bedrooms mapping: studio/efficiency → 0 bedrooms; 1bd/1br/1BR → 1; 2bd/2br/2BR → 2; 3bd/3br → 3.
- Use a clean label for unitType: "Studio", "1 Bed", "2 Bed", "3 Bed", etc.
- If a rent roll row has sub-types (e.g. "1BR Renovated" and "1BR Unrenovated"), keep them as separate rows with descriptive unitType labels.
- Leave any field null if not stated for a given type.
- Do NOT put rent roll data in expenseItems or additionalMetrics.

BUILDING AMENITIES:
- buildingAmenities = shared / common-area amenities that apply to the whole building as a string array.
- Examples: "elevator", "laundry room", "gym / fitness center", "rooftop deck", "pool", "doorman / concierge", "bike storage", "storage units", "package room", "parking garage", "EV charging".
- Normalize to clean labels. Include only what is explicitly stated in the OM.
- If amenities are listed but not distinguished as unit vs building, put them in buildingAmenities.

NOI AND CAP RATE — DISTINGUISH CURRENT vs PRO FORMA:
- capRate = current / in-place / Year-1 cap rate (decimal, e.g. 0.06 for 6%)
- capRateProForma = pro forma / stabilized / Year-N cap rate (decimal) — only if the OM states a second cap rate
- brokerNOI = Year-1 / current NOI (the number most OMs lead with)
- brokerNOIStabilized = stabilized / Year-N NOI (only if OM states a multi-year schedule)

VACANCY — LABEL PRECISELY:
- vacancyAssumption = decimal (e.g. 0.05 for 5%)
- vacancyLabel = the OM's own label: "bad debt", "physical vacancy", "economic vacancy", "credit loss", etc.

FINANCING METRICS — extract if present:
- dscr = debt service coverage ratio (e.g. 2.22)
- stabilizedCashOnCash = stabilized / Year-N cash-on-cash return (decimal)
- financingInterestAnnual = annual interest expense stated in the OM (dollar amount)

- For additional content not covered by named fields: use additionalSections for narrative prose sections (investment thesis, lease overview, value-add plan, etc.) and additionalMetrics for any tabular metrics (cap rate comps, market stats, demographics, etc.).
- Return ONLY valid JSON — no markdown, no explanation, no prose outside the JSON.`

const omExtractionUserPrompt = `Extract ALL information from this Offering Memorandum. Return ONLY this JSON structure (null for any field not present):

{
  "brokerContact": {
    "name": string | null,
    "title": string | null,
    "company": string | null,
    "phone": string | null,
    "email": string | null,
    "licenseNumber": string | null
  } | null,
  "omDate": string | null,
  "askingPrice": number | null,
  "capRate": number | null,
  "capRateProForma": number | null,
  "propertyTitle": string | null,
  "propertyAddress": string | null,
  "propertyDescription": string | null,
  "investmentHighlights": string[] | null,

  "yearBuilt": number | null,
  "yearRenovated": number | null,
  "stories": number | null,
  "zoning": string | null,
  "construction": string | null,
  "parking": string | null,
  "parkingSpaces": number | null,
  "buildingCount": number | null,
  "totalUnits": number | null,
  "buildingAmenities": string[] | null,
  "lotSqft": number | null,
  "buildingSqft": number | null,
  "rentableSF": number | null,
  "avgUnitSF": number | null,
  "walkScore": number | null,
  "currentOccupancy": number | null,

  "rentByUnitType": [
    {
      "unitType": string,
      "bedrooms": number,
      "count": number | null,
      "sqftPerUnit": number | null,
      "parkingSlots": number | null,
      "rentCurrent": number | null,
      "rentProForma": number | null,
      "rentMarket": string | null,
      "amenities": string[] | null
    }
  ] | null,

  "grossPotentialRentCurrent": number | null,
  "grossPotentialRentProforma": number | null,
  "grossPotentialRent": number | null,
  "vacancyPct": number | null,
  "vacancyLabel": string | null,
  "otherIncomeItems": [{"label": string, "amount": number}] | null,
  "effectiveGrossIncome": number | null,
  "totalEffectiveGrossIncome": number | null,

  "expenseItems": [
    {"label": string, "amount": number, "pctOfEGI": number | null}
  ] | null,
  "totalExpenses": number | null,
  "expenseRatioPct": number | null,
  "noiCurrent": number | null,
  "noiProforma": number | null,
  "noiSummaryStated": number | null,

  "brokerNOI": number | null,
  "brokerNOIStabilized": number | null,
  "pricePerUnit": number | null,
  "pricePerSF": number | null,
  "grm": number | null,
  "cashOnCashCurrent": number | null,
  "fiveYearIRR": number | null,
  "dscr": number | null,

  "valueAdd": {
    "lowEstimate": number | null,
    "highEstimate": number | null,
    "unrenovatedUnits": number | null,
    "costPerUnit": number | null
  } | null,

  "assumableDebtDetail": {
    "balance": number | null,
    "interestRate": number | null,
    "maturityDate": string | null,
    "loanType": string | null
  } | null,

  "renovationCost": number | null,
  "claimedRenovationNOIUplift": number | null,

  "marketOverviewText": string | null,
  "additionalMetrics": [{"label": string, "value": string}] | null,
  "additionalSections": [{"title": string, "content": string}] | null,

  "keyFacts": string[] | null
}

FIELD NOTES:
- keyFacts: capture 5–10 of the most important explicitly stated facts as verbatim short quotes from the OM — e.g. ["3 x 6-Unit Buildings", "Asking Price: $2,100,000", "Year Built: 1962", "Stories: 3 (per building)", "Cap Rate: 6.1%"]. These are used for quality validation after extraction. Prioritize physical attributes, price, and income figures.
- capRate: use decimal (0.061 for 6.1%). If OM labels it "Proforma Cap Rate" or "Stabilized Cap Rate", put in capRateProForma instead.
- currentOccupancy: decimal (0.94 for "17/18 units (94%)" or "94% occupied").
- buildingCount: number of structures on the parcel. Extract from phrases like "3 x 6-Unit Buildings", "two buildings", "portfolio of 4 buildings" → integer count. Do NOT confuse with unit count.
- totalUnits: total rental units in the property. When stated directly ("18 units", "24-unit building"), extract it. When described as "N x M-Unit Buildings" (e.g. "3 x 6-Unit Buildings"), compute N×M (this arithmetic derivation is permitted). Do NOT leave null when the count is trivially derivable.
- stories: extract from "3-story", "stories: 3", "3 floors". When qualified as "per building" (e.g. "Stories: 3 (per building)"), still extract 3 — this is the per-building story count.
- rentByUnitType: map OM rent roll columns as follows — "Current Avg Rent" or "In-Place Rent" → rentCurrent; "Proforma Rent" or "Stabilized Rent" → rentProForma; "Market Rent" or "Market Comparable" → rentMarket.
- grossPotentialRentCurrent: GPR at current/in-place rents. grossPotentialRentProforma: GPR at proforma/stabilized rents.
- noiCurrent: in-place NOI (current rents, current occupancy). noiProforma: stabilized/proforma NOI. noiSummaryStated: NOI from cover page summary box (may differ from P&L).
- vacancyPct: decimal (0.04 for 4%). vacancyLabel: exact label from OM ("Vacancy & Credit Loss", "Economic Vacancy", etc.).
- expenseItems: extract EVERY named expense line — do not skip any. amount is annual dollars.
- otherIncomeItems: ancillary income below GPR (laundry, parking, storage, late fees, etc.).
- valueAdd.lowEstimate/highEstimate: total value-add potential in dollars (e.g. $252,000–$294,000 → low=252000, high=294000).`

// ---------------------------------------------------------------------------
// ADR-108: Two-pass extraction — Pass 1 (classify) + Pass 2 (type-specific)
// ---------------------------------------------------------------------------

// omClassificationResult is the Pass 1 output from claude-haiku.
type omClassificationResult struct {
	DocumentClass string `json:"documentClass"` // "acquisition_om"|"development_om"|"fund_ppm"|"other"
	PropertyType  string `json:"propertyType"`  // "multifamily"|"nnn"|"retail"|...
	SubType       string `json:"subType"`       // e.g. "garden_style", "single_tenant_nnn"
	Confidence    string `json:"confidence"`    // "high"|"medium"|"low"
}

// omOutOfScopeError is returned when Pass 1 classifies the document as out-of-scope.
// CreateDealFromOM returns HTTP 422; UploadOMFile logs and continues with empty extraction.
type omOutOfScopeError struct {
	PropertyType string
}

func (e *omOutOfScopeError) Error() string {
	labels := map[string]string{
		"hotel":       "hotel investment",
		"development": "development equity raise",
		"fund":        "fund offering",
	}
	label, ok := labels[e.PropertyType]
	if !ok {
		label = e.PropertyType
	}
	return fmt.Sprintf("This document appears to be a %s. Estara Pipeline is designed for direct property acquisitions "+
		"(multifamily, NNN, retail, office, industrial, self-storage, student housing, senior housing, and portfolios "+
		"thereof). Please upload an acquisition OM for a specific property.", label)
}

const omClassifySystemPrompt = `Classify this real estate document. Return JSON only — no markdown, no prose.`

const omClassifyUserPrompt = `Classify this real estate document. Return ONLY this JSON:
{
  "documentClass": "acquisition_om" | "development_om" | "fund_ppm" | "other",
  "propertyType": "multifamily" | "nnn" | "retail" | "office" | "mixed_use" | "industrial" | "warehouse" | "self_storage" | "student_housing" | "senior_housing" | "hotel" | "portfolio" | "land" | "other",
  "subType": string | null,
  "confidence": "high" | "medium" | "low"
}
Rules:
- documentClass "acquisition_om": broker selling a specific property or portfolio
- documentClass "development_om": equity raise for a project not yet built or stabilised
- documentClass "fund_ppm": investment in a fund or pooled vehicle
- documentClass "other": cannot determine
- Classify from explicit document content only. Do not guess.`

// omBuildExtractionSystemPrompt returns a type-specific extraction system prompt.
// It appends type-specific instructions to the shared base prompt.
func omBuildExtractionSystemPrompt(propertyType string) string {
	var typeBlock string
	switch propertyType {
	case "multifamily", "student_housing":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — MULTIFAMILY / STUDENT HOUSING:
1. Rent roll: extract ALL three rent columns per unit type — rentCurrent, rentProForma, rentMarket, count, sqftPerUnit.
2. Occupancy: extract currentOccupancy as a decimal (e.g. 0.944 for "17 of 18 units occupied" or "94.4% occupied"). Leave null if not stated.
3. Full P&L: grossPotentialRentCurrent (and grossPotentialRentProforma if stated), vacancyPct, effectiveGrossIncome, all named expense rows in expenseItems (label, amount, pctOfEGI), total expenses, NOI.
4. NOI cross-check: extract noiSummaryStated (from cover/summary box) AND noiComputedFromStatement (from P&L) separately — even if they differ.
5. Return metrics: cashOnCashCurrent (Year 1 CoC), cashOnCashStabilized, fiveYearIRR, grm.
6. Assumable debt: populate assumableDebtDetail (balance, interestRate, maturityDate, loanType) if stated.
7. Value-add: populate valueAdd (lowEstimate, highEstimate, unrenovatedUnits, costPerUnit) if stated.
8. Broker market section: populate brokerMarket (vacancyRate, rentGrowthYoY, jobGrowth, medianHHI, walkScore, capRateComprBps, submarketName, dataSource).
9. Renovation economics: if the OM states a total renovation cost or per-unit budget, populate renovationCost (total dollars; multiply per-unit × unit count if stated per unit). If the OM states an expected NOI increase from renovation (e.g. "Year 2 NOI", "post-renovation NOI"), populate claimedRenovationNOIUplift as the INCREMENTAL NOI increase only — not the absolute stabilised NOI. If the OM only states a stabilised NOI without calling it a renovation uplift, leave claimedRenovationNOIUplift null.
10. Building count and unit count: these are CRITICAL physical attributes — do not skip them.
  - buildingCount: number of structures. Phrases like "3 x 6-Unit Buildings", "two buildings on parcel", "4-building campus" → extract the building count (3, 2, 4). This is NOT the unit count.
  - totalUnits: total rental units across all buildings. May be stated directly ("18 units", "24-unit property") or derivable as N×M from "N x M-Unit Buildings" (e.g. "3 x 6-Unit Buildings" → 18). Computing N×M is REQUIRED here — this is an explicitly stated arithmetic relationship, not an inference.
  - stories: extract from "3-story", "Stories: 3", "3 floors (per building)" → 3. The qualifier "per building" does not change the value — extract the number.`
	case "nnn", "retail", "office":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — NNN / RETAIL / OFFICE:
1. Tenant schedule: populate tenantSchedule array — one entry per tenant with: tenantName, squareFeet, annualRent, leaseExpiry, leaseType ("nnn"|"modified_gross"|"gross"), annualRentBumpPct, landlordResponsibility, tenantCreditRating, guarantorName.
2. Building metrics: buildingSqft (NRA), rentableSF, walr (weighted average lease remaining in years).
3. Traffic: trafficCountVPD for retail/NNN if stated.
4. NOI: brokerNOI (current in-place), noiProforma (stabilised), noiSummaryStated, noiComputedFromStatement.
5. Expenses: expenseItems array (label, amount, pctOfEGI).
6. Return metrics: cashOnCashCurrent, cashOnCashStabilized, capRate, capRateProForma.
7. Market: brokerMarket (vacancyRate, rentGrowthYoY, submarketName, dataSource).
Note: rentByUnitType is NOT applicable for pure commercial — leave it null.`
	case "mixed_use":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — MIXED-USE:
1. Residential units: rentByUnitType for all residential unit types (Studio, 1 Bed, 2 Bed, etc.).
2. Commercial tenants: tenantSchedule for all commercial tenants.
3. Combined P&L: grossPotentialRentCurrent, expenseItems, noiCurrent.
4. Physical: buildingSqft (total GBA), rentableSF (total NRA); put commercial sqft in typeSpecificData.commercialSqft.
5. Return metrics: cashOnCashCurrent, capRate, capRateProForma.`
	case "industrial", "warehouse":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — INDUSTRIAL / WAREHOUSE:
1. Physical spec: buildingSqft, rentableSF, stories, construction, zoning, parkingSpaces.
2. Type-specific in typeSpecificData: clearHeightFt, dockDoors, gradeDoors, columnSpacingFt, powerAmps (number), coldStorage (boolean), yardAcres.
3. Tenants: tenantSchedule (single or multi-tenant), walr, leaseType per tenant.
4. NOI: brokerNOI, noiProforma, expenseItems.
5. Return metrics: pricePerSF, capRate, capRateProForma.`
	case "self_storage":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — SELF-STORAGE:
1. Unit mix in typeSpecificData.unitMix: array of {unitType, count, sqft, monthlyRent, occupancyPct}.
2. Key metrics in typeSpecificData: totalUnits, climateControlledPct (decimal), managementType ("self_managed"|"third_party"), managerName.
3. Financials: grossPotentialRent, vacancyPct, expenseItems, noiCurrent.
4. Physical: buildingSqft, parkingSpaces (exterior drive-up), stories.`
	case "senior_housing":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — SENIOR HOUSING:
1. Care metrics in typeSpecificData: bedCount, unitCount, acuityLevel ("independent_living"|"assisted"|"memory_care"|"skilled_nursing"), payorMix ({medicare, medicaid, private} as decimals).
2. Occupancy: currentOccupancy, typeSpecificData.stabilizedOccupancy.
3. Financials: grossPotentialRent (per-bed or total), expenseItems, noiCurrent.
4. Licensing in typeSpecificData: licensedBeds, certifications (string array).
5. Market: brokerMarket (vacancyRate, submarketName if stated).`
	case "portfolio":
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — PORTFOLIO:
1. Portfolio totals: askingPrice (total), capRate (blended), units (total all properties).
2. Individual properties in typeSpecificData.properties: array of {address, propertyType, units, askingPrice, noiCurrent, capRate, yearBuilt}.
3. Combined financials: grossPotentialRent (total), noiCurrent (total), expenseItems (portfolio-level if stated).
4. Blended metrics: capRate (blended cap rate), capRateProForma (blended pro forma).`
	default:
		typeBlock = `
TYPE-SPECIFIC INSTRUCTIONS — GENERIC / BEST-EFFORT:
Extract whatever is explicitly present. Focus on: askingPrice, capRate, brokerNOI, grossPotentialRent, propertyAddress, yearBuilt, buildingSqft, brokerContact.
Use additionalMetrics and additionalSections for data not captured by named fields.`
	}
	return omExtractionSystemPrompt + strings.TrimRight(typeBlock, "\n")
}

// classifyOMDocument runs Pass 1: document classification using claude-haiku (fast, cheap).
func (h *Handler) classifyOMDocument(ctx context.Context, fileBytes []byte, mediaType string) (*omClassificationResult, error) {
	if h.cfg.AI.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("Anthropic API key not configured")
	}

	const anthropicURL = "https://api.anthropic.com/v1/messages"
	var reqBody anthropicDocumentRequest

	if mediaType == "application/pdf" {
		import64 := encodeBase64(fileBytes)
		reqBody = anthropicDocumentRequest{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 256,
			System:    omClassifySystemPrompt,
			Messages: []anthropicDocumentMsg{
				{
					Role: "user",
					Content: []interface{}{
						anthropicDocBlock{
							Type: "document",
							Source: anthropicDocSource{
								Type:      "base64",
								MediaType: "application/pdf",
								Data:      import64,
							},
						},
						anthropicTextBlock{Type: "text", Text: omClassifyUserPrompt},
					},
				},
			},
		}
	} else {
		// For text/Excel: send as plain text.
		var textContent string
		if mediaType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
			if t, err := excelToText(fileBytes); err == nil {
				textContent = t
			} else {
				textContent = string(fileBytes)
			}
		} else {
			textContent = string(fileBytes)
		}
		// Limit to first 8000 chars for classification — we only need enough to classify.
		if len(textContent) > 8000 {
			textContent = textContent[:8000]
		}
		combined := omClassifyUserPrompt + "\n\nDocument content:\n" + textContent
		reqBody = anthropicDocumentRequest{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 256,
			System:    omClassifySystemPrompt,
			Messages: []anthropicDocumentMsg{
				{
					Role:    "user",
					Content: []interface{}{anthropicTextBlock{Type: "text", Text: combined}},
				},
			},
		}
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal classify request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("create classify request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "pdfs-2024-09-25")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("classify API call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read classify response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("classify: %s", anthropicUserMessage(resp.StatusCode, body))
	}

	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse classify response: %w", err)
	}

	var jsonText string
	for _, block := range envelope.Content {
		if block.Type == "text" {
			jsonText = strings.TrimSpace(block.Text)
			break
		}
	}
	jsonText = strings.TrimPrefix(jsonText, "```json")
	jsonText = strings.TrimPrefix(jsonText, "```")
	jsonText = strings.TrimSuffix(jsonText, "```")
	jsonText = strings.TrimSpace(jsonText)

	var result omClassificationResult
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		// Non-fatal: fall back to generic extraction.
		return &omClassificationResult{
			DocumentClass: "other",
			PropertyType:  "other",
			Confidence:    "low",
		}, nil
	}
	return &result, nil
}

// extractFullOMData runs two-pass extraction: Pass 1 (classify, haiku) → Pass 2 (type-specific, sonnet).
// Returns *omOutOfScopeError if Pass 1 classifies the document as hotel/development/fund.
func (h *Handler) extractFullOMData(ctx context.Context, fileBytes []byte, mediaType string) (*omData, error) {
	// Pass 1 — classify (fast, cheap).
	classification, err := h.classifyOMDocument(ctx, fileBytes, mediaType)
	if err != nil {
		h.logger.Warn("Pass 1 classification failed — falling back to generic extraction", "error", err)
		classification = &omClassificationResult{
			DocumentClass: "other",
			PropertyType:  "other",
			Confidence:    "low",
		}
	}

	// Reject out-of-scope document classes.
	switch classification.PropertyType {
	case "hotel", "development", "fund":
		return nil, &omOutOfScopeError{PropertyType: classification.PropertyType}
	}
	if classification.DocumentClass == "development_om" || classification.DocumentClass == "fund_ppm" {
		dc := classification.DocumentClass
		pt := "development"
		if dc == "fund_ppm" {
			pt = "fund"
		}
		return nil, &omOutOfScopeError{PropertyType: pt}
	}

	// Pass 2 — full type-specific extraction (sonnet).
	systemPrompt := omBuildExtractionSystemPrompt(classification.PropertyType)
	result, err := h.callClaudeForOMData(ctx, mediaType, fileBytes, systemPrompt)
	if err != nil {
		return nil, err
	}

	// Stamp Pass 1 classification onto the result.
	result.DocumentClass = &classification.DocumentClass
	result.PropertyType = &classification.PropertyType
	result.ClassificationConfidence = &classification.Confidence

	// Annotate with file metadata.
	sz := int64(len(fileBytes))
	result.OmFileSizeBytes = &sz
	now := time.Now().UTC().Format(time.RFC3339)
	result.OmParsedAt = &now

	return result, nil
}

// callClaudeForOMData sends the Pass 2 extraction request and returns parsed OMData.
func (h *Handler) callClaudeForOMData(ctx context.Context, mediaType string, fileBytes []byte, systemPrompt string) (*omData, error) {
	if h.cfg.AI.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("Anthropic API key not configured")
	}

	const anthropicURL = "https://api.anthropic.com/v1/messages"

	var reqBody anthropicDocumentRequest

	if mediaType == "application/pdf" {
		// Use native PDF document block.
		import64 := encodeBase64(fileBytes)
		reqBody = anthropicDocumentRequest{
			Model:     "claude-sonnet-4-6",
			MaxTokens: 4096,
			System:    systemPrompt,
			Messages: []anthropicDocumentMsg{
				{
					Role: "user",
					Content: []interface{}{
						anthropicDocBlock{
							Type: "document",
							Source: anthropicDocSource{
								Type:      "base64",
								MediaType: "application/pdf",
								Data:      import64,
							},
						},
						anthropicTextBlock{
							Type: "text",
							Text: omExtractionUserPrompt,
						},
					},
				},
			},
		}
	} else {
		// Text/Excel content.
		var text string
		if mediaType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
			if t, xerr := excelToText(fileBytes); xerr == nil {
				text = t
			} else {
				return nil, fmt.Errorf("Excel to text: %w", xerr)
			}
		} else {
			text = string(fileBytes)
		}
		combined := omExtractionUserPrompt + "\n\nDocument content:\n" + text
		reqBody = anthropicDocumentRequest{
			Model:     "claude-sonnet-4-6",
			MaxTokens: 4096,
			System:    systemPrompt,
			Messages: []anthropicDocumentMsg{
				{
					Role: "user",
					Content: []interface{}{
						anthropicTextBlock{
							Type: "text",
							Text: combined,
						},
					},
				},
			},
		}
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "pdfs-2024-09-25")

	client := &http.Client{Timeout: 90 * time.Second}
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
		return nil, fmt.Errorf("%s", anthropicUserMessage(resp.StatusCode, body))
	}

	// Parse Anthropic response envelope.
	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
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

	// Strip markdown code fences if present.
	jsonText = strings.TrimPrefix(jsonText, "```json")
	jsonText = strings.TrimPrefix(jsonText, "```")
	jsonText = strings.TrimSuffix(jsonText, "```")
	jsonText = strings.TrimSpace(jsonText)

	var result omData
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, fmt.Errorf("parse OM JSON: %w", err)
	}

	return &result, nil
}

// anthropicUserMessage converts an Anthropic HTTP error into a user-friendly message.
// 529/503 = overloaded (retryable), 429 = rate limited (retryable), others = permanent.
func anthropicUserMessage(statusCode int, body []byte) string {
	bodyStr := strings.ToLower(string(body))
	switch statusCode {
	case 529, 503:
		return "AI service is temporarily overloaded — please wait a moment and try again."
	case 429:
		return "AI request limit reached — please wait a moment and try again."
	case 401, 403:
		return "AI service authentication error — please contact support."
	default:
		if strings.Contains(bodyStr, "overloaded") {
			return "AI service is temporarily overloaded — please wait a moment and try again."
		}
		return fmt.Sprintf("AI service returned an error (%d) — please try again.", statusCode)
	}
}

// isRetryableAnthropicError returns true when the error message indicates a transient condition.
func isRetryableAnthropicError(msg string) bool {
	return strings.Contains(msg, "overloaded") || strings.Contains(msg, "limit reached")
}

// encodeBase64 encodes bytes to base64 string.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// ---------------------------------------------------------------------------
// Pipeline Decision Memo — SSE streaming (ADR-105)
// ---------------------------------------------------------------------------

// GeneratePipelineMemo handles POST /api/pipeline/deals/{dealId}/decision-memo
// Generates a pipeline-specific decision memo via Claude with:
//   - Standard property analysis sections
//   - "Broker vs. System Underwriting" comparison
//   - "OM Critique" section (if OM data present)
//
// Streams the memo content as SSE.
func (h *Handler) GeneratePipelineMemo(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	// Verify deal ownership and fetch properties.
	deal, err := h.store.Q().GetPipelineDeal(r.Context(), queries.GetPipelineDealParams{
		ID:     dealID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	props, err := h.store.Q().ListPipelineProperties(r.Context(), queries.ListPipelinePropertiesParams{
		PipelineDealID: dealID,
		UserID:         userID,
	})
	if err != nil {
		h.logger.Error("ListPipelineProperties failed", "error", err)
		httputil.InternalError(w, err)
		return
	}

	if len(props) == 0 {
		httputil.BadRequest(w, "deal has no properties")
		return
	}

	if h.cfg.AI.AnthropicAPIKey == "" {
		httputil.Error(w, http.StatusServiceUnavailable, "AI service not configured")
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Disable write deadline so the long-running SSE stream is not cut off by
	// the server's write_timeout (typically 60s).
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	flusher, ok := w.(http.Flusher)
	if !ok {
		httputil.InternalError(w, fmt.Errorf("streaming not supported"))
		return
	}

	// Helper to send an SSE event.
	sendEvent := func(event, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	sendEvent("progress", `{"phase":"Building memo context...","pct":10}`)

	// Fetch market data for each unique city/state concurrently.
	// Non-fatal: a nil entry means no market data available for that location.
	marketData := fetchMarketDataForProps(r.Context(), h.marketService, props)

	// Build the memo prompt.
	prompt := buildPipelineMemoPrompt(deal.Name, props, marketData)

	sendEvent("progress", `{"phase":"Generating decision memo...","pct":30}`)

	// Use streaming Claude API so the SSE connection stays alive during generation.
	aiClient := anthropic.NewClient(anthropic.ClientConfig{
		APIKey:    h.cfg.AI.AnthropicAPIKey,
		Model:     "claude-sonnet-4-6",
		MaxTokens: 12288, // ADR-110: increased to accommodate deal analytics + broker claims + sensitivity sections
		Timeout:   10 * time.Minute, // streaming session; context cancel handles client disconnect
	})

	streamCh, err := aiClient.Stream(r.Context(), "", prompt)
	if err != nil {
		h.logger.Error("pipeline memo stream start failed", "error", err, "deal_id", dealID)
		errJSON, _ := json.Marshal(map[string]string{"error": err.Error()})
		sendEvent("error", string(errJSON))
		return
	}

	var memoBuf strings.Builder
	for evt := range streamCh {
		if evt.Error != nil {
			h.logger.Error("pipeline memo stream error", "error", evt.Error.Message, "deal_id", dealID)
			errJSON, _ := json.Marshal(map[string]string{"error": evt.Error.Message})
			sendEvent("error", string(errJSON))
			return
		}
		if evt.Type == "content_block_delta" && evt.Delta.Type == "text_delta" && evt.Delta.Text != "" {
			memoBuf.WriteString(evt.Delta.Text)
			tokenJSON, _ := json.Marshal(map[string]string{"text": evt.Delta.Text})
			sendEvent("token", string(tokenJSON))
		}
	}

	memo := memoBuf.String()

	// Bump memo count and persist memo text on the deal.
	_ = h.store.Q().BumpPipelineDealMemoCount(r.Context(), dealID)
	_ = h.store.Q().SavePipelineDealMemoText(r.Context(), queries.SavePipelineDealMemoTextParams{
		ID:       dealID,
		MemoText: pgtype.Text{String: memo, Valid: true},
	})

	// Send the complete event with the full memo.
	memoJSON, _ := json.Marshal(map[string]any{
		"memo":   memo,
		"dealId": dealID.String(),
	})
	sendEvent("complete", string(memoJSON))
}

// ---------------------------------------------------------------------------
// ADR-110: Deal Analytics — Go-side pre-computation injected into memo prompt.
// Arithmetic is done in Go (deterministic); Claude explains the implications.
// ---------------------------------------------------------------------------

// dealAnalytics holds pre-computed financial metrics for a single property.
// Nil pointer fields indicate insufficient data for that metric.
type dealAnalytics struct {
	AskingPrice float64

	// DSCR (30yr amortisation standard)
	DSCRAvailable    bool
	LoanAmount       float64
	LTV              float64 // e.g. 0.80
	Rate             float64 // annual decimal
	AnnualDebtService float64
	DSCR             float64
	DSCRBelow        bool    // < 1.20x — standard lender minimum
	DSCRThin         bool    // 1.20–1.30x — thin coverage
	IODebtService    float64 // interest-only annual service
	IODSCR           float64
	NOI              float64
	NOISource        string // "P&L"|"current"|"broker"|"estimated"

	// Scorecard
	PricePerUnit   *float64
	PricePerSF     *float64
	AnnualRentGap  *float64 // Σ(count × (market−current) × 12)
	ExpenseRatio   *float64 // TotalExpenses / EGI (decimal)
	ImpliedCapRate *float64 // NOI / askingPrice
	ExitValueEst   *float64 // NOI / (capRate + 0.005)

	// Vintage CapEx
	VintageCapEx  bool
	AgeYears      int
	Units         int32
	CapExLow      float64 // total ($5k/unit × units)
	CapExHigh     float64 // total ($12k/unit × units)
	CapExLowUnit  float64 // per unit
	CapExHighUnit float64 // per unit

	// Renovation reconciliation
	HasRecon          bool
	RentUpliftRevenue float64 // Σ(count × (market−current) × 12)
	RentUpliftNOI     float64 // × 0.65
	ClaimedNOIUplift  float64
	ReconGap          float64 // claimed − estimated
	ReconGapPct       float64 // gap / claimed × 100
	ReconGapHigh      bool    // |gap| > 15%
}

// computeDealAnalytics calculates all pre-computable financial metrics for one property.
func computeDealAnalytics(
	askingPrice, downPaymentPct, interestRate float64,
	hasDownPayment, hasInterestRate bool,
	noi float64, noiSource string,
	units int32, hasUnits bool,
	sqft int32, hasSqft bool,
	yearBuilt int32, hasYearBuilt bool,
	brokerCapRate float64, hasCapRate bool,
	rentGap float64,
	egi, totalExpenses float64, hasExpenseData bool,
	claimedNOIUplift float64, hasClaimedUplift bool,
) dealAnalytics {
	a := dealAnalytics{
		AskingPrice: askingPrice,
		NOI:         noi,
		NOISource:   noiSource,
	}
	if askingPrice <= 0 {
		return a
	}

	// DSCR — 30yr fixed amortisation
	if hasDownPayment && hasInterestRate && interestRate > 0 && noi > 0 {
		ltv := 1.0 - downPaymentPct
		loanAmt := askingPrice * ltv
		monthlyRate := interestRate / 12.0
		n360 := math.Pow(1+monthlyRate, 360)
		payment := loanAmt * (monthlyRate * n360) / (n360 - 1)
		annualDS := payment * 12
		a.DSCRAvailable = true
		a.LoanAmount = loanAmt
		a.LTV = ltv
		a.Rate = interestRate
		a.AnnualDebtService = annualDS
		if annualDS > 0 {
			a.DSCR = noi / annualDS
			a.DSCRBelow = a.DSCR < 1.20
			a.DSCRThin = a.DSCR >= 1.20 && a.DSCR < 1.30
		}
		// Interest-only scenario
		a.IODebtService = loanAmt * interestRate
		if a.IODebtService > 0 {
			a.IODSCR = noi / a.IODebtService
		}
	} else if hasDownPayment && noi > 0 {
		// Down payment known but no interest rate — IO only at assumed 7%
		ltv := 1.0 - downPaymentPct
		loanAmt := askingPrice * ltv
		assumedRate := 0.07
		a.DSCRAvailable = true
		a.LoanAmount = loanAmt
		a.LTV = ltv
		a.Rate = assumedRate
		a.IODebtService = loanAmt * assumedRate
		if a.IODebtService > 0 {
			a.IODSCR = noi / a.IODebtService
		}
	} else if hasInterestRate && !hasDownPayment && interestRate > 0 && noi > 0 {
		// Interest rate known but no down payment — bridge/IO loan scenario.
		// Assume 75% LTV (standard bridge). Flag as estimate.
		assumedLTV := 0.75
		loanAmt := askingPrice * assumedLTV
		a.DSCRAvailable = true
		a.LoanAmount = loanAmt
		a.LTV = assumedLTV
		a.Rate = interestRate
		// IO only — no amortisation payment; AnnualDebtService stays 0
		a.IODebtService = loanAmt * interestRate
		if a.IODebtService > 0 {
			a.IODSCR = noi / a.IODebtService
		}
	}

	// Scorecard: price/unit, price/SF
	if hasUnits && units > 0 {
		v := askingPrice / float64(units)
		a.PricePerUnit = &v
		a.Units = units
	}
	if hasSqft && sqft > 0 {
		v := askingPrice / float64(sqft)
		a.PricePerSF = &v
	}

	// Annual rent gap (MF value-add upside)
	if rentGap > 0 {
		a.AnnualRentGap = &rentGap
	}

	// Expense ratio
	if hasExpenseData && egi > 0 && totalExpenses > 0 {
		v := totalExpenses / egi
		a.ExpenseRatio = &v
	}

	// Implied cap rate + exit value
	if noi > 0 {
		v := noi / askingPrice
		a.ImpliedCapRate = &v
		exitCap := v + 0.005
		if hasCapRate && brokerCapRate > 0 {
			exitCap = brokerCapRate + 0.005
		}
		exitVal := noi / exitCap
		a.ExitValueEst = &exitVal
	}

	// Vintage CapEx
	if hasYearBuilt && yearBuilt > 0 {
		age := time.Now().Year() - int(yearBuilt)
		if age > 40 && hasUnits && units > 0 {
			a.VintageCapEx = true
			a.AgeYears = age
			a.Units = units
			a.CapExLowUnit = 5000
			a.CapExHighUnit = 12000
			a.CapExLow = float64(units) * 5000
			a.CapExHigh = float64(units) * 12000
		}
	}

	// Renovation reconciliation
	if rentGap > 0 && hasClaimedUplift && claimedNOIUplift > 0 {
		a.HasRecon = true
		a.RentUpliftRevenue = rentGap
		a.RentUpliftNOI = rentGap * 0.65
		a.ClaimedNOIUplift = claimedNOIUplift
		a.ReconGap = claimedNOIUplift - a.RentUpliftNOI
		if claimedNOIUplift != 0 {
			a.ReconGapPct = a.ReconGap / claimedNOIUplift * 100
		}
		a.ReconGapHigh = math.Abs(a.ReconGapPct) > 15
	}

	return a
}

// buildAnalyticsPromptBlock formats dealAnalytics as literal structured text for Claude.
// Claude is told to use these numbers verbatim and explain their investment implications.
func buildAnalyticsPromptBlock(a dealAnalytics) string {
	if a.AskingPrice <= 0 {
		return ""
	}
	// Require at least 2 computable scorecard rows to avoid a mostly-empty block.
	scorecardRows := 0
	if a.PricePerUnit != nil { scorecardRows++ }
	if a.PricePerSF != nil { scorecardRows++ }
	if a.AnnualRentGap != nil { scorecardRows++ }
	if a.ExpenseRatio != nil { scorecardRows++ }
	if a.ImpliedCapRate != nil { scorecardRows++ }
	if a.ExitValueEst != nil { scorecardRows++ }
	if a.DSCRAvailable { scorecardRows++ }
	if scorecardRows < 2 && !a.DSCRAvailable && !a.VintageCapEx && !a.HasRecon {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n**DEAL ANALYTICS** (pre-computed — use these numbers verbatim; do not alter them; explain their investment implications):\n")

	// Debt Service / DSCR
	if a.DSCRAvailable {
		sb.WriteString("\nDebt Service Analysis:\n")
		if a.AnnualDebtService > 0 {
			sb.WriteString(fmt.Sprintf("- Loan: $%.0f (%.0f%% LTV, %.2f%% rate, 30yr amortisation)\n",
				a.LoanAmount, a.LTV*100, a.Rate*100))
			sb.WriteString(fmt.Sprintf("- Annual debt service: $%.0f\n", a.AnnualDebtService))
			sb.WriteString(fmt.Sprintf("- Year-1 NOI (%s): $%.0f\n", a.NOISource, a.NOI))
			dscrLabel := "adequate"
			if a.DSCRBelow {
				dscrLabel = "⚠ BELOW 1.20x LENDER THRESHOLD"
			} else if a.DSCRThin {
				dscrLabel = "⚠ THIN (1.20–1.30x) — stress-test against rate increases"
			}
			sb.WriteString(fmt.Sprintf("- DSCR: %.2fx — %s\n", a.DSCR, dscrLabel))
		} else {
			// IO-only (no amortisation rate available)
			sb.WriteString(fmt.Sprintf("- Loan: $%.0f (%.0f%% LTV, rate not provided — using %.0f%% for IO estimate)\n",
				a.LoanAmount, a.LTV*100, a.Rate*100))
		}
		if a.IODebtService > 0 && a.IODSCR > 0 {
			sb.WriteString(fmt.Sprintf("- IO scenario: annual interest $%.0f → IO DSCR %.2fx\n", a.IODebtService, a.IODSCR))
		}
	}

	// Deal Scorecard table
	if scorecardRows >= 2 {
		sb.WriteString("\nDeal Scorecard:\n")
		sb.WriteString("| Metric | Value | Assessment | Note |\n")
		sb.WriteString("|--------|-------|------------|------|\n")
		if a.ImpliedCapRate != nil {
			capAssessment := "—" // market-dependent; flag only if very compressed
			if *a.ImpliedCapRate < 0.04 {
				capAssessment = "⚠"
			}
			sb.WriteString(fmt.Sprintf("| Going-in Cap Rate | %.2f%% | %s | Implied: NOI / asking price |\n", *a.ImpliedCapRate*100, capAssessment))
		}
		if a.PricePerUnit != nil {
			sb.WriteString(fmt.Sprintf("| Price / Unit | $%.0f | — | Benchmark varies by market |\n", *a.PricePerUnit))
		}
		// Show Price/SF only for commercial (no unit count). Price/unit is the primary MF metric.
		if a.PricePerSF != nil && a.PricePerUnit == nil {
			sb.WriteString(fmt.Sprintf("| Price / SF | $%.2f | — | — |\n", *a.PricePerSF))
		}
		if a.AnnualRentGap != nil {
			rentAssessment := "✓"
			if *a.AnnualRentGap == 0 {
				rentAssessment = "—"
			}
			sb.WriteString(fmt.Sprintf("| Annual Rent Gap | $%.0f/yr | %s | Rent-math: (market − current) × units × 12 |\n", *a.AnnualRentGap, rentAssessment))
		}
		if a.ExpenseRatio != nil {
			ratio := *a.ExpenseRatio * 100
			expAssessment := "✓"
			if ratio < 30 || ratio > 55 {
				expAssessment = "⚠"
			}
			sb.WriteString(fmt.Sprintf("| Expense Ratio | %.1f%% EGI | %s | Flag if <30%% (understated) or >55%% (high) |\n", ratio, expAssessment))
		}
		if a.DSCRAvailable && a.AnnualDebtService > 0 {
			dscrAssessment := "✓"
			if a.DSCRBelow {
				dscrAssessment = "❗"
			} else if a.DSCRThin {
				dscrAssessment = "⚠"
			}
			sb.WriteString(fmt.Sprintf("| DSCR | %.2fx | %s | Lender minimum: 1.20x |\n", a.DSCR, dscrAssessment))
		}
		if a.ExitValueEst != nil {
			exitRatio := *a.ExitValueEst / a.AskingPrice
			exitAssessment := "✓"
			if exitRatio < 0.90 {
				exitAssessment = "❗"
			} else if exitRatio < 1.00 {
				exitAssessment = "⚠"
			}
			sb.WriteString(fmt.Sprintf("| Exit Value Est. | $%.0f | %s | At going-in cap + 50bps; ask $%.0f |\n", *a.ExitValueEst, exitAssessment, a.AskingPrice))
		}
	}

	// Vintage CapEx
	if a.VintageCapEx {
		builtYear := time.Now().Year() - a.AgeYears
		sb.WriteString(fmt.Sprintf("\nVintage CapEx (built %d, age %d years, %d units):\n", builtYear, a.AgeYears, a.Units))
		sb.WriteString(fmt.Sprintf("- Industry estimate: $%.0f–$%.0f over 5 years ($%.0f–$%.0f/unit)\n",
			a.CapExLow, a.CapExHigh, a.CapExLowUnit, a.CapExHighUnit))
		sb.WriteString("- This capital is ADDITIONAL to purchase price — include in total capital required\n")
	}

	// Renovation reconciliation
	if a.HasRecon {
		sb.WriteString("\nRenovation Economics Check:\n")
		sb.WriteString(fmt.Sprintf("- Rent uplift revenue (from unit mix): $%.0f/yr\n", a.RentUpliftRevenue))
		sb.WriteString(fmt.Sprintf("- Estimated NOI uplift (×0.65 expense ratio): $%.0f/yr\n", a.RentUpliftNOI))
		sb.WriteString(fmt.Sprintf("- OM claimed NOI uplift: $%.0f/yr\n", a.ClaimedNOIUplift))
		if a.ReconGapHigh {
			sb.WriteString(fmt.Sprintf("- ⚠ RECONCILIATION GAP: $%.0f (%.1f%% of claimed) — investigate renovation scope and expense assumptions\n",
				a.ReconGap, a.ReconGapPct))
		} else {
			sb.WriteString(fmt.Sprintf("- Gap: $%.0f (%.1f%%) — within acceptable range\n", a.ReconGap, a.ReconGapPct))
		}
	}

	return sb.String()
}

// buildBrokerClaimsBlock produces a pre-populated broker claim verification table.
// Each row is Go-computed from structured OM data; Claude adds one explanation sentence per row.
// Gated on OM data — returns "" when called without reliable OM figures.
func buildBrokerClaimsBlock(a dealAnalytics, om *omData) string {
	type claimRow struct {
		Claim     string
		DataCheck string
		Rating    string // "✓" | "⚠" | "❗"
	}
	var rows []claimRow

	// 0. Financing feasibility (DSCR) — fires whenever financing data is present; not dependent on OM fields
	if a.DSCRAvailable && (a.AnnualDebtService > 0 || a.IODebtService > 0) {
		rating := "✓"
		var check string
		if a.AnnualDebtService > 0 {
			// Full amortising DSCR
			check = fmt.Sprintf("DSCR %.2fx (NOI $%.0f / debt service $%.0f at %.0f%% LTV, %.2f%% rate, 30yr am)",
				a.DSCR, a.NOI, a.AnnualDebtService, a.LTV*100, a.Rate*100)
			if a.DSCRBelow {
				check += " — below 1.20x lender minimum"
				rating = "❗"
			} else if a.DSCRThin {
				check += " — thin coverage (1.20–1.30x)"
				rating = "⚠"
			}
			rows = append(rows, claimRow{"Financing (DSCR)", check, rating})
		} else if a.IODebtService > 0 && a.IODSCR > 0 {
			// IO-only (bridge loan or rate-only entry)
			ltvLabel := fmt.Sprintf("%.0f%%", a.LTV*100)
			if a.LTV == 0.75 {
				ltvLabel = "75% (assumed — bridge)"
			}
			check = fmt.Sprintf("IO DSCR %.2fx (NOI $%.0f / IO interest $%.0f at %s LTV, %.2f%% rate)",
				a.IODSCR, a.NOI, a.IODebtService, ltvLabel, a.Rate*100)
			if a.IODSCR < 1.00 {
				check += " — IO cash flow negative; deal requires rent growth or rent-up to service debt"
				rating = "❗"
			} else if a.IODSCR < 1.20 {
				check += " — thin IO coverage; amortising refi will be worse"
				rating = "⚠"
			}
			rows = append(rows, claimRow{"Financing (IO DSCR — bridge)", check, rating})
		}
	}

	// 1. Cap rate integrity
	if a.ImpliedCapRate != nil && om.CapRate != nil && *om.CapRate > 0 {
		statedPct := *om.CapRate * 100
		impliedPct := *a.ImpliedCapRate * 100
		deltaBps := (impliedPct - statedPct) * 100
		check := fmt.Sprintf("Stated: %.2f%%; implied (NOI/price): %.2f%%; delta: %.0f bps", statedPct, impliedPct, deltaBps)
		rating := "✓"
		if math.Abs(deltaBps) > 75 {
			rating = "❗"
		} else if math.Abs(deltaBps) > 25 {
			rating = "⚠"
		}
		// Flag "pro forma" label applied to in-place NOI
		if om.CapRateProForma != nil && math.Abs(*om.CapRateProForma-*om.CapRate) < 0.0005 {
			check += " (pro forma label may apply to in-place NOI — verify)"
			if rating == "✓" {
				rating = "⚠"
			}
		}
		rows = append(rows, claimRow{"Cap rate integrity", check, rating})
	}

	// 2. NOI integrity (summary vs P&L)
	if om.NOISummaryStated != nil && om.NOIComputedFromStatement != nil {
		delta := *om.NOISummaryStated - *om.NOIComputedFromStatement
		pct := 0.0
		if *om.NOIComputedFromStatement != 0 {
			pct = delta / *om.NOIComputedFromStatement * 100
		}
		check := fmt.Sprintf("Summary: $%.0f; P&L computed: $%.0f; delta: $%.0f (%.1f%%)",
			*om.NOISummaryStated, *om.NOIComputedFromStatement, delta, pct)
		rating := "✓"
		if math.Abs(pct) > 15 {
			rating = "❗"
		} else if math.Abs(pct) > 5 {
			rating = "⚠"
		}
		rows = append(rows, claimRow{"NOI (summary vs P&L)", check, rating})
	}

	// 3. Expense ratio benchmark
	if a.ExpenseRatio != nil {
		ratio := *a.ExpenseRatio * 100
		check := fmt.Sprintf("%.1f%% of EGI", ratio)
		rating := "✓"
		if ratio < 30 {
			check += " — below 30%; expenses may be understated"
			rating = "⚠"
		} else if ratio > 55 {
			check += " — above 55%; verify expense categories"
			rating = "⚠"
		}
		rows = append(rows, claimRow{"Expense ratio", check, rating})
	}

	// 4. Renovation NOI uplift reconciliation
	if a.HasRecon {
		check := fmt.Sprintf("Rent-math NOI estimate: $%.0f (rent gap $%.0f × 0.65); OM implies $%.0f uplift; gap: $%.0f (%.1f%%)",
			a.RentUpliftNOI, a.RentUpliftRevenue, a.ClaimedNOIUplift, math.Abs(a.ReconGap), math.Abs(a.ReconGapPct))
		rating := "✓"
		if math.Abs(a.ReconGapPct) > 15 {
			rating = "❗"
		} else if math.Abs(a.ReconGapPct) > 8 {
			rating = "⚠"
		}
		rows = append(rows, claimRow{"Renovation math (uplift reconciliation)", check, rating})
	} else if a.AnnualRentGap != nil && *a.AnnualRentGap > 0 && a.NOI > 0 {
		// Fallback: fires when rent gap exists but claimedNOIUplift wasn't extracted.
		// Compare rent-math NOI uplift against any proforma NOI the OM states.
		var proformaNOI float64
		if om.BrokerNOIStabilized != nil && *om.BrokerNOIStabilized > a.NOI {
			proformaNOI = *om.BrokerNOIStabilized
		} else if om.NOIProforma != nil && *om.NOIProforma > a.NOI {
			proformaNOI = *om.NOIProforma
		}
		if proformaNOI > 0 {
			rentMathUplift := *a.AnnualRentGap * 0.65
			impliedUplift := proformaNOI - a.NOI
			gap := impliedUplift - rentMathUplift
			gapPct := 0.0
			if impliedUplift != 0 {
				gapPct = gap / impliedUplift * 100
			}
			check := fmt.Sprintf("Rent-math NOI uplift: $%.0f (rent gap $%.0f × 0.65); stabilized NOI implies $%.0f uplift; gap: $%.0f (%.1f%%)",
				rentMathUplift, *a.AnnualRentGap, impliedUplift, math.Abs(gap), math.Abs(gapPct))
			rating := "✓"
			if math.Abs(gapPct) > 15 {
				rating = "❗"
			} else if math.Abs(gapPct) > 8 {
				rating = "⚠"
			}
			rows = append(rows, claimRow{"Renovation math (rent-math vs stabilized NOI)", check, rating})
		}
	}

	// 5. Vacancy — combine stated assumption with implied vacancy from income statement
	{
		var impliedVacancyPct float64
		hasImplied := false
		// Compute implied vacancy from GPR vs EGI (always reliable when both present)
		gpr := 0.0
		if om.GrossPotentialRentCurrent != nil {
			gpr = *om.GrossPotentialRentCurrent
		} else if om.GrossPotentialRent != nil {
			gpr = *om.GrossPotentialRent
		}
		egi := 0.0
		if om.TotalEffectiveGrossIncome != nil {
			egi = *om.TotalEffectiveGrossIncome
		}
		if gpr > 0 && egi > 0 && gpr > egi {
			impliedVacancyPct = (1.0 - egi/gpr) * 100
			hasImplied = true
		}

		if om.VacancyPct != nil || hasImplied {
			rating := "✓"
			var check string
			statedVP := 0.0
			if om.VacancyPct != nil {
				statedVP = *om.VacancyPct * 100
			}
			if hasImplied && om.VacancyPct != nil {
				delta := math.Abs(impliedVacancyPct - statedVP)
				check = fmt.Sprintf("Stated: %.1f%%; implied from GPR/EGI: %.1f%%; discrepancy: %.1fpp",
					statedVP, impliedVacancyPct, delta)
				if delta > 8 {
					check += " — income statement implies materially higher vacancy than stated"
					rating = "❗"
				} else if delta > 3 || statedVP < 3 {
					rating = "⚠"
				}
			} else if hasImplied {
				check = fmt.Sprintf("Implied from GPR $%.0f / EGI $%.0f: %.1f%% vacancy",
					gpr, egi, impliedVacancyPct)
				if impliedVacancyPct > 12 {
					check += " — elevated vacancy; verify lease-up timeline"
					rating = "⚠"
				} else if impliedVacancyPct < 2 {
					check += " — very low; verify in-place occupancy"
					rating = "⚠"
				}
			} else {
				check = fmt.Sprintf("Stated: %.1f%%", statedVP)
				if statedVP < 3 {
					check += " — very low; verify against submarket occupancy"
					rating = "⚠"
				} else if statedVP < 5 {
					check += " — below 5%; optimistic assumption"
					rating = "⚠"
				}
			}
			rows = append(rows, claimRow{"Vacancy / occupancy", check, rating})
		}
	}

	// 6. Unverifiable third-party market claims (CoStar, JLL, Yardi, etc.)
	{
		citationKeywords := []string{"CoStar", "Yardi", "JLL", "CBRE", "Marcus & Millichap",
			"top 3", "top 5", "#1", "No. 1", "No.1", "ranked first", "fastest-growing", "best-performing"}
		for _, h := range om.InvestmentHighlights {
			for _, kw := range citationKeywords {
				if strings.Contains(h, kw) {
					check := fmt.Sprintf("Highlight cites '%s': \"%s\" — cannot be verified without current subscription data", kw, h)
					if len(check) > 160 {
						check = check[:157] + "…"
					}
					rows = append(rows, claimRow{"Unverifiable market claim", check, "⚠"})
					break // one row per highlight max
				}
			}
		}
	}

	if len(rows) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n**BROKER CLAIM VERIFICATION** (pre-computed — include this table verbatim in the Broker Claim Verification section; add one explanation sentence per row):\n")
	sb.WriteString("| Claim | Data Check | Rating |\n")
	sb.WriteString("|-------|-----------|--------|\n")
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", r.Claim, r.DataCheck, r.Rating))
	}

	// Overall credibility
	redCount, yellowCount := 0, 0
	for _, r := range rows {
		switch r.Rating {
		case "❗":
			redCount++
		case "⚠":
			yellowCount++
		}
	}
	overall := "High"
	if redCount >= 2 {
		overall = "Low"
	} else if redCount == 1 || yellowCount >= 2 {
		overall = "Medium"
	} else if yellowCount >= 1 {
		overall = "Medium"
	}
	sb.WriteString(fmt.Sprintf("\n**Overall Broker Credibility**: %s\n", overall))
	return sb.String()
}

// buildSensitivityBlock produces the Estara conservative sensitivity scenario as literal text.
// Claude is told to include it verbatim and write a conclusion sentence.
func buildSensitivityBlock(a dealAnalytics, om *omData, brokerCapRate float64) string {
	if a.AskingPrice <= 0 || a.NOI <= 0 {
		return ""
	}

	// Get EGI and total expenses from OM
	egi := 0.0
	totalExpenses := 0.0
	if om.TotalEffectiveGrossIncome != nil {
		egi = *om.TotalEffectiveGrossIncome
	}
	if om.TotalExpenses != nil {
		totalExpenses = *om.TotalExpenses
	} else if len(om.ExpenseItems) > 0 {
		for _, e := range om.ExpenseItems {
			totalExpenses += e.Amount
		}
	}

	brokerNOI := a.NOI
	conservNOI := brokerNOI

	if egi > 0 {
		conservEGI := egi * 0.98 // −2pp vacancy
		conservExp := totalExpenses * 1.10
		if totalExpenses > 0 {
			conservNOI = conservEGI - conservExp
		} else {
			// Estimate expenses at 40% of EGI if not available
			conservNOI = conservEGI - (egi*0.40)*1.10
		}
	} else {
		// No EGI — reduce NOI by 12% as blended conservative assumption
		conservNOI = brokerNOI * 0.88
	}

	goingInCap := brokerCapRate
	if goingInCap <= 0 && a.ImpliedCapRate != nil {
		goingInCap = *a.ImpliedCapRate
	}
	conservGoingInCap := 0.0
	if conservNOI > 0 && a.AskingPrice > 0 {
		conservGoingInCap = conservNOI / a.AskingPrice
	}
	exitCap := goingInCap + 0.005
	brokerExitValue := 0.0
	if exitCap > 0 && brokerNOI > 0 {
		brokerExitValue = brokerNOI / exitCap
	}
	conservExitValue := 0.0
	if exitCap > 0 && conservNOI > 0 {
		conservExitValue = conservNOI / exitCap
	}
	conservDSCR := 0.0
	if a.DSCRAvailable && a.AnnualDebtService > 0 {
		conservDSCR = conservNOI / a.AnnualDebtService
	}

	var sb strings.Builder
	sb.WriteString("\n**ESTARA SENSITIVITY SCENARIO** (include this table verbatim in the Estara Sensitivity Scenario section; write one conclusion sentence after):\n")
	sb.WriteString("Conservative assumptions: vacancy +2pp, operating expenses +10%, exit cap +50bps\n")
	sb.WriteString("| Metric | Broker | Estara Conservative | Assumption |\n")
	sb.WriteString("|--------|--------|---------------------|------------|\n")
	// Renovation cost: broker stated vs. Estara conservative (+20% contingency)
	if om.RenovationCost != nil && *om.RenovationCost > 0 {
		sb.WriteString(fmt.Sprintf("| Renovation Cost | $%.0f | $%.0f | +20%% contingency on broker budget |\n",
			*om.RenovationCost, *om.RenovationCost*1.20))
	}
	if egi > 0 {
		sb.WriteString(fmt.Sprintf("| EGI | $%.0f | $%.0f | After +2pp vacancy |\n", egi, egi*0.98))
	}
	if totalExpenses > 0 {
		sb.WriteString(fmt.Sprintf("| Operating Expenses | $%.0f | $%.0f | +10%% on broker figure |\n", totalExpenses, totalExpenses*1.10))
	}
	sb.WriteString(fmt.Sprintf("| NOI | $%.0f | $%.0f | Conservative EGI − expenses |\n", brokerNOI, conservNOI))
	if goingInCap > 0 {
		sb.WriteString(fmt.Sprintf("| Going-in Cap | %.2f%% | %.2f%% | At conservative NOI / price |\n", goingInCap*100, conservGoingInCap*100))
	}
	if exitCap > 0 {
		sb.WriteString(fmt.Sprintf("| Exit Cap | %.2f%% | %.2f%% | Broker +50bps expansion |\n", exitCap*100-0.5, exitCap*100))
	}
	if brokerExitValue > 0 {
		sb.WriteString(fmt.Sprintf("| Exit Value | $%.0f | $%.0f | NOI / exit cap |\n", brokerExitValue, conservExitValue))
	}
	if a.DSCRAvailable && a.AnnualDebtService > 0 {
		sb.WriteString(fmt.Sprintf("| DSCR | %.2fx | %.2fx | At conservative NOI |\n", a.DSCR, conservDSCR))
	}

	// Conclusion hint for Claude
	conclusion := "the deal works on conservative assumptions"
	if conservDSCR > 0 && conservDSCR < 1.05 {
		conclusion = "DSCR falls to %.2fx on conservative assumptions — lender concessions or additional equity likely required"
		conclusion = fmt.Sprintf(conclusion, conservDSCR)
	} else if conservNOI <= 0 {
		conclusion = "conservative NOI turns negative — the deal is highly sensitive to broker's expense and vacancy assumptions"
	} else if conservGoingInCap > 0 && goingInCap > 0 && conservGoingInCap < goingInCap*0.85 {
		conclusion = "conservative cap rate is materially lower — value creation is sensitive to accurate expense underwriting"
	}
	sb.WriteString(fmt.Sprintf("\n**Conservative conclusion hint**: %s. Write one sentence expanding on this.\n", conclusion))

	return sb.String()
}

// buildPipelineMemoPrompt constructs the Claude prompt for a pipeline decision memo.
// The memo is an investor decision document — it helps the investor decide whether to
// proceed with a deal. It is NOT a critique of any offering memorandum. OM data (when
// present) is one input among many; manual-entry deals are equally valid.
//
// ADR-109: preamble protocol, type-aware analysis blocks, fallback chains.
// ADR-110: Go-side pre-computed analytics, broker claim verification, sensitivity scenario.
// fetchMarketDataForProps fetches unified market data for all unique city/state pairs
// across the given properties. Results are keyed by "city|state" (lower-case). Non-fatal:
// missing data for a location is represented by a nil map value.
func fetchMarketDataForProps(ctx context.Context, ms *unified.Service, props []queries.PipelineProperty) map[string]*unified.LocationMarketData {
	if ms == nil || !ms.IsConfigured() {
		return nil
	}

	type locationKey struct{ city, state string }

	// Collect unique locations.
	seen := make(map[locationKey]bool)
	var locs []locationKey
	for _, p := range props {
		if p.City.Valid && p.State.Valid && p.City.String != "" && p.State.String != "" {
			k := locationKey{
				city:  strings.ToLower(strings.TrimSpace(p.City.String)),
				state: strings.ToLower(strings.TrimSpace(p.State.String)),
			}
			if !seen[k] {
				seen[k] = true
				locs = append(locs, k)
			}
		}
	}
	if len(locs) == 0 {
		return nil
	}

	type result struct {
		key  string
		data *unified.LocationMarketData
	}
	ch := make(chan result, len(locs))

	for _, loc := range locs {
		loc := loc // capture
		go func() {
			data := ms.Get(ctx, loc.city, loc.state)
			ch <- result{key: loc.city + "|" + loc.state, data: data}
		}()
	}

	out := make(map[string]*unified.LocationMarketData, len(locs))
	for range locs {
		r := <-ch
		out[r.key] = r.data // nil is valid (no data available)
	}
	return out
}

// marketKey returns the map key used in fetchMarketDataForProps.
func marketKey(city, state string) string {
	return strings.ToLower(strings.TrimSpace(city)) + "|" + strings.ToLower(strings.TrimSpace(state))
}

func buildPipelineMemoPrompt(dealName string, props []queries.PipelineProperty, marketData map[string]*unified.LocationMarketData) string {
	var sb strings.Builder

	// Preamble protocol: instruct Claude to output structured metadata before narrative.
	sb.WriteString("You are Estara's Investment Analyst. Generate a professional Investment Decision Memo ")
	sb.WriteString("to help an investor decide whether to proceed with a pipeline deal.\n\n")
	sb.WriteString("ROLE: Decision support. Frame everything as factors for the investor to weigh — ")
	sb.WriteString("not as advice. Use probabilistic language: \"may\", \"could\", \"data indicates\".\n\n")
	sb.WriteString("PREAMBLE PROTOCOL: Before writing any section headings, output EXACTLY these 3 lines:\n")
	sb.WriteString("VERDICT: Proceed|Negotiate|Pass\n")
	sb.WriteString("RISK: Low|Medium|High\n")
	sb.WriteString("KEY_UNCERTAINTY: [one sentence describing the single most important unknown]\n\n")
	sb.WriteString("After those 3 lines, write the memo sections starting with ### Executive Summary.\n\n")
	sb.WriteString("IMPORTANT: This memo is about the INVESTMENT OPPORTUNITY, not about any document. ")
	sb.WriteString("If OM data is available it is one data source; if not, work with what you have.\n\n")
	sb.WriteString("P&L INSTRUCTION: When 'Operating expenses (from P&L)' are provided, you MUST use them in the ")
	sb.WriteString("Financial Snapshot section. List each line item, compute the total, and compare to the stated NOI. ")
	sb.WriteString("If NOI (computed from P&L) and NOI (stated on cover) differ, call out the delta and assess what it means. ")
	sb.WriteString("Do NOT write 'no P&L available' or 'limited financial detail' when expense items are present.\n\n")
	sb.WriteString("CITATIONS INSTRUCTION: Do NOT cite specific named reports, rankings, indices, or publications ")
	sb.WriteString("(e.g. 'CBRE H1 2025 Columbus Office Report', 'CoStar Q3 2024 Retail Rankings', 'JLL Industrial Index'). ")
	sb.WriteString("You have no access to live market data and any specific citation you generate may be fabricated. ")
	sb.WriteString("Do NOT use these evasion phrases — they all signal the same generic padding:\n")
	sb.WriteString("  ❌ 'markets of this type'\n")
	sb.WriteString("  ❌ 'infill neighborhoods of this type'\n")
	sb.WriteString("  ❌ 'markets with these characteristics'\n")
	sb.WriteString("  ❌ 'comparable markets'\n")
	sb.WriteString("  ❌ 'similar submarkets'\n")
	sb.WriteString("  ❌ 'neighborhoods like this'\n")
	sb.WriteString("Replace with the actual city/submarket name and a specific conditional claim, or delete the sentence entirely. ")
	sb.WriteString("If you cannot name the specific submarket and make a specific claim, omit. Do not pad with generic asset-class language.\n\n")
	sb.WriteString("MARKET CONTEXT INSTRUCTION: Each property below may include an **Independent Market Context** block ")
	sb.WriteString("(sourced from Zillow/FRED database or AI-estimated). This is third-party data — not the broker's figures. ")
	sb.WriteString("You MUST use it in three specific ways:\n")
	sb.WriteString("  1. RENT SANITY CHECK: Compare broker-stated rent against market median rent. State the delta in dollars and percent. ")
	sb.WriteString("If broker rent > market median by more than 10%%, flag it as ⚠ in the Broker Claim Verification table with: ")
	sb.WriteString("'Broker states $X/mo; market median is $Y/mo (+Z%% premium — verify achievability).' ")
	sb.WriteString("If broker rent is below market, note upside potential.\n")
	sb.WriteString("  2. CAP RATE SANITY CHECK: Compare implied cap rate (NOI / asking price) against market cap rate. ")
	sb.WriteString("State the spread in basis points. A negative spread (deal priced below market cap) means the investor is paying a premium — flag it.\n")
	sb.WriteString("  3. MARKET CONTEXT section: Use the independent data to anchor the Market & Location section with specific numbers: ")
	sb.WriteString("vacancy rate, YoY price appreciation, unemployment, rent growth. Cite the source as 'per independent market data'. ")
	sb.WriteString("Do not use this data to contradict clearly stated OM figures — use it to calibrate confidence.\n")
	sb.WriteString("If no **Independent Market Context** block is present for a property, omit these checks for that property.\n\n")
	sb.WriteString("DSCR INSTRUCTION: A FINANCING FEASIBILITY line above the unit mix provides the pre-computed DSCR ratio. ")
	sb.WriteString("This ratio MUST appear explicitly in the Financial Snapshot — as a number (e.g. '1.11x'), not paraphrased as 'thin cash flow', 'constrained debt coverage', or any other description that omits the ratio. ")
	sb.WriteString("After stating the ratio, include EXACTLY this sentence (fill in the actual computed value): ")
	sb.WriteString("'This implies a DSCR of X.XXx, which is [above / below] the standard 1.20x lender minimum.' ")
	sb.WriteString("If you review your Financial Snapshot and cannot find a DSCR ratio expressed as a number followed by 'x', rewrite that paragraph — it is incomplete.\n")
	sb.WriteString("If DSCR < 1.20x, the Financial Snapshot MUST open with this exact bold warning (fill in actual numbers): ")
	sb.WriteString("'⚠ Financing Risk: DSCR of X.XXx is below the standard 1.20x lender minimum. This deal likely requires interest-only terms, ")
	sb.WriteString("a renovation escrow, or a price reduction to qualify for conventional financing.' ")
	sb.WriteString("If DSCR is 1.20–1.30x, add: 'Thin coverage at X.XXx — stress-test against rates 50–100bps higher.'\n\n")
	sb.WriteString("RENOVATION MATH INSTRUCTION: When unit mix data is present (current rent and market rent per unit type), ")
	sb.WriteString("you MUST compute and show this calculation in the Financial Snapshot:\n")
	sb.WriteString("  'Rent uplift: Σ(units × monthly rent increase) × 12 = $X annual revenue uplift → NOI uplift = $X × 0.65 = $Y'\n")
	sb.WriteString("If the OM or property data states a stabilized NOI higher than in-place NOI, compare:\n")
	sb.WriteString("  'Rent-math supports $Y NOI uplift; OM implies $Z uplift — gap of $W (P%%).'\n")
	sb.WriteString("Flag gaps > 15%% as ⚠ in the Broker Claim Verification table renovation row.\n")
	sb.WriteString("If the Deal Analytics shows a reconciliation gap flagged with ⚠ RECONCILIATION GAP, ")
	sb.WriteString("include it as a ❗-rated Risk Factor: state the gap amount and percentage, and frame it as ")
	sb.WriteString("'This is a common location for broker optimism — verify renovation scope, unit count, and achievable post-renovation rents independently.'\n\n")
	sb.WriteString("CAP RATE LABEL: If the Broker Claim Verification table flags a cap rate label issue (pro forma label on in-place NOI), ")
	sb.WriteString("call it out explicitly in the Financial Snapshot: ")
	sb.WriteString("'The cap rate labeled [pro forma/stabilised] in the OM matches the in-place NOI math. Either the label is incorrect or the NOI figure is mixed — clarify with the seller.'\n\n")
	sb.WriteString("TAX CLAIM: If any property tax advantage is mentioned in the OM or investor notes (CAUV, agricultural exemption, ")
	sb.WriteString("homestead exemption, green certification credit, historic tax credit, etc.), assess whether it plausibly applies to ")
	sb.WriteString("this property type and state. Be specific: CAUV applies to Ohio agricultural land, not urban multifamily or commercial. ")
	sb.WriteString("If inapplicable, flag it as marketing language and explain what does apply.\n\n")
	sb.WriteString("CAPEX QUANTIFICATION: If Vintage CapEx data is present in DEAL ANALYTICS, include it in Risk Factors: ")
	sb.WriteString("'Vintage CapEx (built XXXX, age XX years): Industry benchmark is $5,000–$12,000/unit over 5 years ")
	sb.WriteString("($X–$X total for this property). This capital is not shown in the OM's investment summary and must be ")
	sb.WriteString("factored into total capital required.' Do not qualify this as speculation — it is an industry benchmark range.\n\n")
	sb.WriteString("SCORECARD INSTRUCTION: The Deal Analytics block contains a pre-computed Deal Scorecard table. ")
	sb.WriteString("Reproduce it as the FIRST element of the Financial Snapshot section, before any narrative. ")
	sb.WriteString("Do not alter any values. After the table, explain each metric briefly in prose.\n\n")
	sb.WriteString("DEAL QUALITY SCORE INSTRUCTION: Immediately after the Deal Scorecard prose explanation, output a **Deal Quality Score** table:\n")
	sb.WriteString("| Dimension | Score (1–10) | Rationale |\n")
	sb.WriteString("|-----------|--------------|----------|\n")
	sb.WriteString("Five dimensions (one row each): Market Quality, Asset Quality, Value-Add Thesis, Financial Structure, Risk-Adjusted Entry.\n")
	sb.WriteString("Score each 1–10 (1=poor, 10=exceptional). Keep Rationale to one short phrase (≤12 words).\n")
	sb.WriteString("End the table with a final row: **Overall** | **X.X / 10** | [one sentence verdict].\n")
	sb.WriteString("Score honestly — a mediocre deal should score 4–5. Do not inflate scores.\n\n")
	sb.WriteString("CLAIMS TABLE INSTRUCTION: The Broker Claim Verification section MUST contain a single markdown table with 4–6 rows. ")
	sb.WriteString("Build it as follows:\n")
	sb.WriteString("  STEP 1 — Copy any pre-computed rows from the BROKER CLAIM VERIFICATION block above verbatim (do not alter ratings or numbers).\n")
	sb.WriteString("  STEP 2 — Add one row for each of the following checks, using data from your own analysis of this memo:\n")
	sb.WriteString("    Row: 'Vacancy / occupancy' — compare implied vacancy (from GPR vs EGI) against stated assumption or submarket norm; rate ✓/⚠/❗\n")
	sb.WriteString("    Row: 'Renovation math' — rent-math NOI uplift estimate vs OM's stabilized NOI claim; state both numbers and the gap; rate ✓/⚠/❗\n")
	sb.WriteString("    Row: 'Expense ratio' — stated expenses as % of EGI vs benchmark (35–50%% MF; 25–40%% NNN); flag if outside range; rate ✓/⚠/❗\n")
	sb.WriteString("    Row: 'Tax / regulatory claim' — if any tax exemption or credit was cited in the OM or notes, state whether it applies to this property type and state; if not applicable, rate ❗\n")
	sb.WriteString("    Row: 'Market claim' — if any CoStar / JLL / Yardi ranking, superlative, or market forecast was cited, state that it is unverifiable without a current data subscription; rate ⚠\n")
	sb.WriteString("  Skip a row only if there is literally no data to populate it (e.g. no renovation claim, no market ranking cited).\n")
	sb.WriteString("  After the table, add exactly ONE sentence per row (no more). Each sentence restates the key number and its implication. ")
	sb.WriteString("No paragraphs. No elaboration. No hedge stacking. The table row does the analytical work — the sentence is a signpost only.\n")
	sb.WriteString("  A table with one row means you have not completed this section — that is not acceptable.\n\n")
	sb.WriteString("SENSITIVITY INSTRUCTION: If an ESTARA SENSITIVITY SCENARIO block is provided, include that table verbatim ")
	sb.WriteString("in the Estara Sensitivity Scenario section. After the table, write exactly ONE sentence. ")
	sb.WriteString("Format: lead with the specific dollar figure from the conservative scenario, then state the action. ")
	sb.WriteString("Example: 'At $2,950,000 ask, the conservative scenario implies a $550,000 mark-to-market loss — negotiate entry to $2,650,000.' ")
	sb.WriteString("Do NOT lead with 'while', 'although', 'however', or any subordinate clause. Number first, action last. 20 words maximum.\n\n")
	sb.WriteString("INVESTMENT SUMMARY INSTRUCTION: Before the Executive Summary, output a structured **Investment Summary** block in exactly this format (fill from available data; use '—' if unknown):\n")
	sb.WriteString("  - **Deal**: [property type] | [city, state] | [asking price]\n")
	sb.WriteString("  - **Execution**: [primary value-creation mechanism — e.g., 'value-add via rent roll renovation', 'NNN cash flow', 'vacant-to-leased turnover']\n")
	sb.WriteString("  - **Capital**: [acquisition price] + [renovation cost if stated, else '—'] = [total capital required]\n")
	sb.WriteString("  - **NOI upside**: [in-place NOI] → [stabilized NOI or rent-math estimate] (delta ≈ [amount])\n")
	sb.WriteString("  - **Risk-adjusted view**: [one phrase — e.g., 'Proceed if price reduced to $X', 'Pass at ask price', 'Negotiate renovation escrow']\n")
	sb.WriteString("Keep each line to one line; no prose. This block must appear BEFORE the Executive Summary heading.\n\n")
	sb.WriteString("SUBMARKET NUANCE: Do not default to generic university-proximity framing for multifamily properties. ")
	sb.WriteString("Assess the actual likely tenant profile based on property type, unit mix, and rent levels. ")
	sb.WriteString("A property with 2BR+ units at $900–$1,200/mo near a medical center or mid-size city downtown suggests working professionals, not students. ")
	sb.WriteString("Identify the most plausible demand driver (medical workers, young professionals, workforce housing, student, etc.) and use it throughout. ")
	sb.WriteString("If unable to determine the profile from available data, name that as a due diligence question, not an assumption.\n\n")
	sb.WriteString("VOICE INSTRUCTION: This memo is institutional-grade decision support. Write like a senior acquisitions professional, not a disclosure document.\n")
	sb.WriteString("  RULE 1 — Declarative endings: Sentences end with conclusions, not caveats. ")
	sb.WriteString("'At $2,950,000 margin of safety is thin. Negotiate to $2,700,000.' NOT ")
	sb.WriteString("'...while the margin of safety is thin, a negotiated entry price could preserve adequate downside protection.'\n")
	sb.WriteString("  RULE 2 — No hedge-stacking: Do not chain qualifiers: 'while X, Y could Z, which may potentially W.' ")
	sb.WriteString("Pick the most likely scenario and state it. Flag uncertainty once, then move on.\n")
	sb.WriteString("  RULE 3 — Numbers anchor claims: When making a market or risk statement, lead with the number. ")
	sb.WriteString("'5.5%–6.5% cap range for this submarket' not 'cap rates in this market tend to be in a typical range.'\n")
	sb.WriteString("  RULE 4 — Verdict and Sensitivity sections are the sharpest: These sections must read like the last line of a partner recommendation — one sentence, one number, one directive. No throat-clearing.\n")
	sb.WriteString("  RULE 5 — If you would not say it at a partner meeting, cut it.\n\n")
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("# Deal: %s\n\n", dealName))
	sb.WriteString(fmt.Sprintf("**Properties in deal**: %d\n\n", len(props)))

	hasOMData := false

	for i, p := range props {
		sb.WriteString(fmt.Sprintf("## Property %d\n\n", i+1))

		// Core identification
		sb.WriteString(fmt.Sprintf("**Address**: %s\n", p.Address))
		if p.City.Valid && p.State.Valid {
			sb.WriteString(fmt.Sprintf("**Location**: %s, %s\n", p.City.String, p.State.String))
		}

		propType := ""
		if p.PropertyType.Valid {
			propType = p.PropertyType.String
			sb.WriteString(fmt.Sprintf("**Type**: %s\n", propType))
		}
		if p.Units.Valid {
			sb.WriteString(fmt.Sprintf("**Units**: %d\n", p.Units.Int32))
		}
		if p.YearBuilt.Valid {
			sb.WriteString(fmt.Sprintf("**Year Built**: %d\n", p.YearBuilt.Int32))
		}
		if p.Sqft.Valid {
			sb.WriteString(fmt.Sprintf("**Size**: %d sqft\n", p.Sqft.Int32))
		}
		if p.CurrentOccupancy.Valid {
			f, _ := p.CurrentOccupancy.Float64Value()
			sb.WriteString(fmt.Sprintf("**Occupancy**: %.0f%%\n", f.Float64*100))
		}

		// Independent market context — from Zillow/FRED DB or Haiku estimation fallback.
		// Injected per-property so Claude can cross-reference broker claims against
		// independent data for this specific city/state.
		if p.City.Valid && p.State.Valid && marketData != nil {
			if md := marketData[marketKey(p.City.String, p.State.String)]; md != nil {
				sb.WriteString(md.ForPrompt())
			} else {
				sb.WriteString(fmt.Sprintf("\n⚠ **No independent market data available for %s, %s** — neither the Zillow/FRED database nor the AI estimation fallback returned figures for this location. ",
					p.City.String, p.State.String))
				sb.WriteString("The rent sanity check, cap rate sanity check, and market context anchoring required by the MARKET CONTEXT INSTRUCTION cannot be performed. ")
				sb.WriteString("Note this gap explicitly in the Market & Location section and flag it as a due diligence item: the investor should independently source comparable rent and cap rate data for this market before proceeding.\n")
			}
		} else if (!p.City.Valid || !p.State.Valid) && marketData != nil {
			sb.WriteString("\n⚠ **No city/state on record for this property — independent market data lookup skipped.**\n")
		}

		// Pricing
		sb.WriteString("\n**Pricing**\n")
		askingPrice := 0.0
		if p.AskingPrice.Valid {
			f, _ := p.AskingPrice.Float64Value()
			askingPrice = f.Float64
			sb.WriteString(fmt.Sprintf("- Asking Price: $%.0f\n", askingPrice))
		}
		if p.TargetPrice.Valid {
			f, _ := p.TargetPrice.Float64Value()
			sb.WriteString(fmt.Sprintf("- Investor Target Price: $%.0f\n", f.Float64))
		}

		// Income / returns
		sb.WriteString("\n**Income & Returns**\n")
		brokerRentMonthly := 0.0
		if p.BrokerRent.Valid {
			f, _ := p.BrokerRent.Float64Value()
			brokerRentMonthly = f.Float64
			sb.WriteString(fmt.Sprintf("- Stated Monthly Rent: $%.0f\n", brokerRentMonthly))
		}
		if p.SystemRent.Valid {
			f, _ := p.SystemRent.Float64Value()
			sb.WriteString(fmt.Sprintf("- System Rent Estimate: $%.0f/mo\n", f.Float64))
		}
		brokerCapRate := 0.0
		if p.BrokerCapRate.Valid {
			f, _ := p.BrokerCapRate.Float64Value()
			brokerCapRate = f.Float64
			sb.WriteString(fmt.Sprintf("- Stated Cap Rate: %.2f%%\n", brokerCapRate*100))
		}

		// Financing assumptions — extract to variables for analytics (ADR-110)
		var downPaymentPct, interestRate float64
		var hasDownPayment, hasInterestRate bool
		if p.DownPaymentPct.Valid {
			f, _ := p.DownPaymentPct.Float64Value()
			downPaymentPct = f.Float64
			hasDownPayment = true
		}
		if p.InterestRate.Valid {
			f, _ := p.InterestRate.Float64Value()
			interestRate = f.Float64
			hasInterestRate = true
		}
		if hasDownPayment || hasInterestRate {
			sb.WriteString("\n**Financing Assumptions**\n")
			if hasDownPayment {
				sb.WriteString(fmt.Sprintf("- Down Payment: %.0f%%\n", downPaymentPct*100))
			}
			if hasInterestRate {
				sb.WriteString(fmt.Sprintf("- Interest Rate: %.2f%%\n", interestRate*100))
			}
		}

		// Notes
		if p.Notes.Valid && p.Notes.String != "" {
			sb.WriteString(fmt.Sprintf("\n**Investor Notes**: %s\n", p.Notes.String))
		}

		// ADR-110: resolved NOI for analytics (filled inside OM block or estimated below)
		var resolvedNOI float64
		var resolvedNOISource string

		// ---------- OM data extraction (fallback chain) ----------
		omForMemo := p.OmData
		if len(p.OmValidatedData) > 0 && string(p.OmValidatedData) != "null" {
			omForMemo = p.OmValidatedData
		}

		var om omData
		hasOMForProp := false
		if len(omForMemo) > 2 && string(omForMemo) != "null" {
			if jerr := json.Unmarshal(omForMemo, &om); jerr == nil {
				hasOMForProp = true
				hasOMData = true
			}
		}

		// ADR-113 Phase 7: typed column → om_data fallback for progressive fields.
		// This ensures manual-entry deals produce equally rich memos as OM-uploaded deals.
		if len(p.OtherIncomeItems) > 2 && string(p.OtherIncomeItems) != "null" {
			var typedOtherIncome []omOtherIncomeItem
			if json.Unmarshal(p.OtherIncomeItems, &typedOtherIncome) == nil && len(typedOtherIncome) > 0 {
				om.OtherIncomeItems = typedOtherIncome
				hasOMForProp = true
			}
		}
		if v := numericToFloat(p.RenovationCost); v != nil {
			om.RenovationCost = v
		}
		if v := numericToFloat(p.ClaimedRenovationNoiUplift); v != nil {
			om.ClaimedRenovationNOIUplift = v
		}
		if len(p.ValueAddData) > 2 && string(p.ValueAddData) != "null" {
			var typedValueAdd omValueAdd
			if json.Unmarshal(p.ValueAddData, &typedValueAdd) == nil {
				om.ValueAdd = &typedValueAdd
			}
		}
		if len(p.BuildingAmenities) > 0 {
			om.BuildingAmenities = p.BuildingAmenities
			hasOMForProp = true
		}
		if p.MarketOverviewText.Valid && p.MarketOverviewText.String != "" {
			om.MarketOverviewText = p.MarketOverviewText.String
			hasOMForProp = true
		}

		if hasOMForProp {
			// Update propType from OM if not in structured column.
			if propType == "" && om.PropertyType != nil {
				propType = *om.PropertyType
			}

			sb.WriteString("\n**Data from Offering Document**\n")
			if om.OmDate != nil && *om.OmDate != "" {
				sb.WriteString(fmt.Sprintf("- Document date: %s\n", *om.OmDate))
			}

			// ---------- Full P&L (ADR-108 structured fields) ----------
			// Income side
			if om.GrossPotentialRentCurrent != nil {
				sb.WriteString(fmt.Sprintf("- Gross Potential Rent (current): $%.0f/yr\n", *om.GrossPotentialRentCurrent))
			}
			if om.GrossPotentialRentProforma != nil {
				sb.WriteString(fmt.Sprintf("- Gross Potential Rent (pro forma): $%.0f/yr\n", *om.GrossPotentialRentProforma))
			}
			// Legacy GPR fallback
			if om.GrossPotentialRentCurrent == nil && om.GrossPotentialRentProforma == nil && om.GrossPotentialRent != nil {
				sb.WriteString(fmt.Sprintf("- Gross Potential Rent: $%.0f/yr\n", *om.GrossPotentialRent))
			}
			if om.VacancyPct != nil {
				sb.WriteString(fmt.Sprintf("- Vacancy assumption: %.1f%%\n", *om.VacancyPct*100))
			}
			if len(om.OtherIncomeItems) > 0 {
				sb.WriteString("- Other income:\n")
				for _, oi := range om.OtherIncomeItems {
					sb.WriteString(fmt.Sprintf("  - %s: $%.0f/yr\n", oi.Label, oi.Amount))
				}
			}
			if om.TotalEffectiveGrossIncome != nil {
				sb.WriteString(fmt.Sprintf("- Effective Gross Income (EGI): $%.0f/yr\n", *om.TotalEffectiveGrossIncome))
			}

			// Expense side — prefer structured ExpenseItems over legacy TotalExpenses
			if len(om.ExpenseItems) > 0 {
				sb.WriteString("- Operating expenses (from P&L):\n")
				for _, ei := range om.ExpenseItems {
					if ei.PctOfEGI != nil {
						sb.WriteString(fmt.Sprintf("  - %s: $%.0f/yr (%.1f%% of EGI)\n", ei.Label, ei.Amount, *ei.PctOfEGI*100))
					} else {
						sb.WriteString(fmt.Sprintf("  - %s: $%.0f/yr\n", ei.Label, ei.Amount))
					}
				}
				if om.ExpenseRatioPct != nil {
					sb.WriteString(fmt.Sprintf("- Total expense ratio: %.1f%% of EGI\n", *om.ExpenseRatioPct*100))
				}
			} else if om.TotalExpenses != nil {
				sb.WriteString(fmt.Sprintf("- Total Expenses: $%.0f/yr\n", *om.TotalExpenses))
			}

			// NOI — prefer computed-from-statement for accuracy; capture to resolvedNOI for analytics
			switch {
			case om.NOIComputedFromStatement != nil:
				resolvedNOI = *om.NOIComputedFromStatement
				resolvedNOISource = "P&L"
				sb.WriteString(fmt.Sprintf("- NOI (computed from P&L): $%.0f/yr\n", *om.NOIComputedFromStatement))
				if om.NOISummaryStated != nil && *om.NOISummaryStated != *om.NOIComputedFromStatement {
					sb.WriteString(fmt.Sprintf("- NOI (stated on cover): $%.0f/yr  [delta vs computed: $%.0f]\n",
						*om.NOISummaryStated, *om.NOISummaryStated-*om.NOIComputedFromStatement))
				}
			case om.NOICurrent != nil:
				resolvedNOI = *om.NOICurrent
				resolvedNOISource = "current"
				sb.WriteString(fmt.Sprintf("- Year-1 NOI: $%.0f/yr\n", *om.NOICurrent))
			case om.BrokerNOI != nil:
				resolvedNOI = *om.BrokerNOI
				resolvedNOISource = "broker"
				sb.WriteString(fmt.Sprintf("- Broker NOI: $%.0f/yr\n", *om.BrokerNOI))
			}
			if om.BrokerNOIStabilized != nil {
				sb.WriteString(fmt.Sprintf("- Stabilized NOI: $%.0f/yr\n", *om.BrokerNOIStabilized))
			}

			if om.CapRateProForma != nil {
				sb.WriteString(fmt.Sprintf("- Pro Forma Cap Rate: %.2f%%\n", *om.CapRateProForma*100))
			}
			if len(om.InvestmentHighlights) > 0 {
				sb.WriteString("- Investment highlights:\n")
				for _, h := range om.InvestmentHighlights {
					sb.WriteString(fmt.Sprintf("  - %s\n", h))
				}
			}
			if om.PropertyDescription != "" {
				desc := om.PropertyDescription
				if len(desc) > 300 {
					desc = desc[:300] + "…"
				}
				sb.WriteString(fmt.Sprintf("- Property description: %s\n", desc))
			}
			if len(om.BuildingAmenities) > 0 {
				sb.WriteString(fmt.Sprintf("- Building amenities: %s\n", strings.Join(om.BuildingAmenities, ", ")))
			}
			if om.MarketOverviewText != "" {
				text := om.MarketOverviewText
				if len(text) > 400 {
					text = text[:400] + "…"
				}
				sb.WriteString(fmt.Sprintf("- Broker market context: %s\n", text))
			}
		}

		// ADR-110: if no OM NOI, fall back to rent estimate
		if resolvedNOI == 0 && brokerRentMonthly > 0 {
			resolvedNOI = brokerRentMonthly * 12 * 0.65
			resolvedNOISource = "estimated"
		}

		// ADR-110 fix: inject DSCR as primary inline data point so Claude treats it as
		// a first-class fact (not buried analytics commentary).
		if interestRate > 0 && resolvedNOI > 0 && askingPrice > 0 {
			ltv := 0.0
			ltvLabel := ""
			if hasDownPayment {
				ltv = 1.0 - downPaymentPct
				ltvLabel = fmt.Sprintf("%.0f%%", ltv*100)
			} else {
				ltv = 0.75 // assumed bridge
				ltvLabel = "75% (assumed)"
			}
			loanAmt := askingPrice * ltv

			if hasDownPayment && hasInterestRate {
				// Full amortising DSCR
				monthlyRate := interestRate / 12.0
				n360 := math.Pow(1+monthlyRate, 360)
				payment := loanAmt * (monthlyRate * n360) / (n360 - 1)
				annualDS := payment * 12
				if annualDS > 0 {
					dscr := resolvedNOI / annualDS
					dscrAlert := ""
					if dscr < 1.20 {
						dscrAlert = " — ⚠ BELOW STANDARD 1.20x LENDER MINIMUM: flag as Financing Risk"
					} else if dscr < 1.30 {
						dscrAlert = " — ⚠ THIN COVERAGE (1.20–1.30x): stress-test against rate increases"
					}
					sb.WriteString(fmt.Sprintf("\n**FINANCING FEASIBILITY**: DSCR **%.2fx** (NOI $%.0f / debt service $%.0f at %s LTV, %.2f%% rate, 30yr am)%s\n",
						dscr, resolvedNOI, annualDS, ltvLabel, interestRate*100, dscrAlert))
				}
			} else {
				// IO-only (bridge loan or rate-only entry)
				ioDS := loanAmt * interestRate
				if ioDS > 0 {
					ioDSCR := resolvedNOI / ioDS
					dscrAlert := ""
					if ioDSCR < 1.00 {
						dscrAlert = " — ⚠ IO CASH FLOW NEGATIVE: deal requires rent-up before break-even"
					} else if ioDSCR < 1.20 {
						dscrAlert = " — ⚠ THIN IO COVERAGE: amortising refi will further compress DSCR"
					}
					sb.WriteString(fmt.Sprintf("\n**FINANCING FEASIBILITY**: IO DSCR **%.2fx** (NOI $%.0f / interest $%.0f at %s LTV, %.2f%% IO rate — bridge structure)%s\n",
						ioDSCR, resolvedNOI, ioDS, ltvLabel, interestRate*100, dscrAlert))
				}
			}
		}

		// ---------- Manual unit mix (ADR-109 fallback chain) ----------
		// Use prop.UnitMix first; fall back to om.RentByUnitType.
		type unitRow struct {
			UnitType    string   `json:"type"`
			Count       int      `json:"count"`
			RentCurrent *float64 `json:"rentCurrent"`
			RentProForma *float64 `json:"rentProForma"`
			RentMarket  *float64 `json:"rentMarket"`
		}
		var unitMixRows []unitRow
		if len(p.UnitMix) > 2 && string(p.UnitMix) != "null" {
			_ = json.Unmarshal(p.UnitMix, &unitMixRows)
		}

		hasUnitMix := len(unitMixRows) > 0
		if !hasUnitMix && hasOMForProp && len(om.RentByUnitType) > 0 {
			for _, u := range om.RentByUnitType {
				cnt := 0
				if u.Count != nil { cnt = *u.Count }
				// rentMarket is a string in omData — parse to float if possible.
				var mktRent *float64
				if u.RentMarket != nil {
					var v float64
					if _, err := fmt.Sscanf(*u.RentMarket, "%f", &v); err == nil {
						mktRent = &v
					}
				}
				unitMixRows = append(unitMixRows, unitRow{
					UnitType:    u.UnitType,
					Count:       cnt,
					RentCurrent: coalesceFloat(u.RentCurrent, u.RentCurrentAvg),
					RentProForma: coalesceFloat(u.RentProForma, u.RentProFormaAvg),
					RentMarket:  mktRent,
				})
			}
			hasUnitMix = len(unitMixRows) > 0
		}

		// ADR-110: compute annual rent gap from unit mix rows
		var annualRentGap float64
		for _, u := range unitMixRows {
			if u.RentCurrent != nil && u.RentMarket != nil && u.Count > 0 {
				if *u.RentMarket > *u.RentCurrent {
					annualRentGap += (*u.RentMarket - *u.RentCurrent) * float64(u.Count) * 12
				}
			}
		}

		// ---------- Manual commercial mix (ADR-109 fallback chain) ----------
		type cmTenantRow struct {
			TenantName       string   `json:"tenantName"`
			Sqft             *float64 `json:"sqft"`
			AnnualRent       *float64 `json:"annualRent"`
			LeaseType        *string  `json:"leaseType"`
			LeaseExpiry      *string  `json:"leaseExpiry"`
			AnnualRentBumpPct *float64 `json:"annualRentBumpPct"`
		}
		var commercialMixRows []cmTenantRow
		if len(p.CommercialMix) > 2 && string(p.CommercialMix) != "null" {
			_ = json.Unmarshal(p.CommercialMix, &commercialMixRows)
		}

		hasCommercialMix := len(commercialMixRows) > 0
		if !hasCommercialMix && hasOMForProp && len(om.TenantSchedule) > 0 {
			for _, t := range om.TenantSchedule {
				var expiry *string
				if t.LeaseExpiry != nil { expiry = t.LeaseExpiry }
				var lt *string
				if t.LeaseType != nil { lt = t.LeaseType }
				var bump *float64
				if t.AnnualRentBumpPct != nil { bump = t.AnnualRentBumpPct }
				row := cmTenantRow{
					TenantName:       t.TenantName,
					Sqft:             t.SquareFeet,
					AnnualRent:       t.AnnualRent,
					LeaseType:        lt,
					LeaseExpiry:      expiry,
					AnnualRentBumpPct: bump,
				}
				commercialMixRows = append(commercialMixRows, row)
			}
			hasCommercialMix = len(commercialMixRows) > 0
		}

		// ---------- Type-specific analysis blocks ----------
		typeBlock := buildTypeSpecificMemoBlock(propType, askingPrice, brokerCapRate, brokerRentMonthly,
			unitMixRows, hasUnitMix, commercialMixRows, hasCommercialMix, &om, hasOMForProp)
		if typeBlock != "" {
			sb.WriteString(typeBlock)
		}

		// ---------- ADR-110: Deal Analytics (Go-computed, injected as literal text) ----------
		var omEGI, omTotalExpenses float64
		var hasExpenseData bool
		var claimedNOIUplift float64
		var hasClaimedUplift bool
		if hasOMForProp {
			if om.TotalEffectiveGrossIncome != nil {
				omEGI = *om.TotalEffectiveGrossIncome
			}
			if om.TotalExpenses != nil {
				omTotalExpenses = *om.TotalExpenses
				if omEGI > 0 {
					hasExpenseData = true
				}
			} else if len(om.ExpenseItems) > 0 {
				for _, e := range om.ExpenseItems {
					omTotalExpenses += e.Amount
				}
				if omEGI > 0 {
					hasExpenseData = true
				}
			}
			if om.ClaimedRenovationNOIUplift != nil {
				claimedNOIUplift = *om.ClaimedRenovationNOIUplift
				hasClaimedUplift = true
			} else if resolvedNOI > 0 {
				// ADR-110 fix: derive claimed NOI uplift from proforma NOI when field not extracted
				// (deals extracted before ADR-110 won't have ClaimedRenovationNOIUplift populated)
				var proformaNOI float64
				if om.NOIProforma != nil && *om.NOIProforma > resolvedNOI {
					proformaNOI = *om.NOIProforma
				} else if om.BrokerNOIStabilized != nil && *om.BrokerNOIStabilized > resolvedNOI {
					proformaNOI = *om.BrokerNOIStabilized
				}
				if proformaNOI > 0 {
					claimedNOIUplift = proformaNOI - resolvedNOI
					hasClaimedUplift = true
				}
			}
		}

		var unitsVal, sqftVal, yearBuiltVal int32
		hasUnitsForAnalytics := p.Units.Valid
		if p.Units.Valid {
			unitsVal = p.Units.Int32
		} else if hasOMForProp && len(om.RentByUnitType) > 0 {
			// ADR-110 fix: derive unit count from OM rent roll for Price/Unit scorecard
			var omUnitCount int32
			for _, u := range om.RentByUnitType {
				if u.Count != nil {
					omUnitCount += int32(*u.Count)
				}
			}
			if omUnitCount > 0 {
				unitsVal = omUnitCount
				hasUnitsForAnalytics = true
			}
		}
		if p.Sqft.Valid {
			sqftVal = p.Sqft.Int32
		}
		var hasYearBuilt bool
		if p.YearBuilt.Valid {
			yearBuiltVal = p.YearBuilt.Int32
			hasYearBuilt = true
		} else if hasOMForProp && om.YearBuilt != nil && *om.YearBuilt > 0 {
			// ADR-110 fix: fall back to OM-extracted year built when property column is empty
			yearBuiltVal = int32(*om.YearBuilt)
			hasYearBuilt = true
		}

		analytics := computeDealAnalytics(
			askingPrice,
			downPaymentPct, interestRate,
			hasDownPayment, hasInterestRate,
			resolvedNOI, resolvedNOISource,
			unitsVal, hasUnitsForAnalytics,
			sqftVal, p.Sqft.Valid,
			yearBuiltVal, hasYearBuilt,
			brokerCapRate, p.BrokerCapRate.Valid,
			annualRentGap,
			omEGI, omTotalExpenses, hasExpenseData,
			claimedNOIUplift, hasClaimedUplift,
		)
		if block := buildAnalyticsPromptBlock(analytics); block != "" {
			sb.WriteString(block)
		}
		if hasOMForProp {
			if claimsBlock := buildBrokerClaimsBlock(analytics, &om); claimsBlock != "" {
				sb.WriteString(claimsBlock)
			}
			if sensBlock := buildSensitivityBlock(analytics, &om, brokerCapRate); sensBlock != "" {
				sb.WriteString(sensBlock)
			}
		}

		// OM reference instruction for Executive Summary (only if OM uploaded).
		if hasOMForProp && om.OmDate != nil && *om.OmDate != "" {
			sb.WriteString(fmt.Sprintf("\n[OM reference: When writing the Executive Summary, open with: "+
				"\"This memo is prepared in connection with the offering memorandum for %s dated %s.\"]\n",
				p.Address, *om.OmDate))
		}

		sb.WriteString("\n")
	}

	// ---------- Memo structure instructions ----------
	sb.WriteString("---\n\n")
	sb.WriteString("## Required Memo Sections\n\n")
	sb.WriteString("Write the Investment Decision Memo with these sections:\n\n")
	sb.WriteString("### IC Brief\n")
	sb.WriteString("30-second read. Exactly four bullet lines. Fragments and hard numbers only.\n")
	sb.WriteString("- **Deal**: [property type] | [city, state] | [asking price]\n")
	sb.WriteString("- **Thesis**: 2 fragments maximum. Numbers must appear. No full sentences. ")
	sb.WriteString("❌ WRONG: 'This deal works if the 12 unrenovated units can be leased at post-renovation rents consistent with comparable Victorian Village product, and if total capital can be deployed at a blended cost that supports a positive spread to the going-in cap rate.' ")
	sb.WriteString("✅ RIGHT: '12 unrenovated units → $1,670/mo post-reno; NOI gap $18K/yr. Works if basis clears 6.1% going-in cap.'\n")
	sb.WriteString("- **Top 3 Risks**: (1) [risk, rated H/M/L] (2) [risk, rated H/M/L] (3) [risk, rated H/M/L]\n")
	sb.WriteString("- **Recommendation**: [Proceed / Negotiate / Pass] — [price target or single condition, ≤10 words]\n")
	sb.WriteString("If the Thesis bullet contains the word 'if' more than once, rewrite it as two shorter fragments. ")
	sb.WriteString("This section stands alone as the summary. The full memo follows immediately after.\n\n")
	sb.WriteString("### Executive Summary\n")
	sb.WriteString("2-3 sentences. What is this deal and what is the core investment question the investor needs to answer. ")
	sb.WriteString("If an OM reference instruction is provided above, open with that sentence.\n\n")
	sb.WriteString("### Investment Thesis\n")
	sb.WriteString("What would need to be true for this to be a compelling investment. The value drivers and upside case.\n\n")
	sb.WriteString("### Financial Snapshot\n")
	sb.WriteString("Key numbers: pricing, income, cap rate, estimated cash flow. ")
	sb.WriteString("If both stated rent and system rent estimate are available, note the variance and what it implies for underwriting. ")
	sb.WriteString("If financing assumptions are present, sketch the debt service and cash-on-cash range. ")
	sb.WriteString("Incorporate the type-specific analysis blocks provided above — do not repeat data already stated, synthesise it.\n\n")
	sb.WriteString("### Market & Location\n")
	sb.WriteString("What the location tells us about demand, rent growth potential, and exit optionality. Be specific about the city/market if stated.\n\n")
	sb.WriteString("### Risk Factors\n")
	sb.WriteString("3-5 specific risks. Rate each Low / Medium / High. Include market risk, property risk, financing risk, and execution risk as applicable. ")
	sb.WriteString("Incorporate the type-specific risk signals provided above into your analysis.\n\n")
	sb.WriteString("### Decision Criteria\n")
	sb.WriteString("The 2-3 things the investor needs to verify or negotiate before proceeding. Frame as questions, not directives.\n\n")
	sb.WriteString("### Verdict\n")
	sb.WriteString("One of: **Proceed** / **Negotiate** / **Pass** — followed by 2–3 declarative sentences max. ")
	sb.WriteString("Lead with price or condition: 'The asking price of $X cannot be justified at this stage because [1–2 specific numbers].' ")
	sb.WriteString("State the condition required to proceed. No hedge-stacking. No throat-clearing. ")
	sb.WriteString("This must match the VERDICT line in your preamble.\n\n")

	if hasOMData {
		sb.WriteString("### Appendix: Offering Document Notes\n")
		sb.WriteString("Brief observations on data quality and any figures that warrant independent verification. ")
		sb.WriteString("Keep this section short — it is an appendix, not the focus of the memo.\n\n")

		sb.WriteString("### Broker Claim Verification\n")
		sb.WriteString("Follow the CLAIMS TABLE INSTRUCTION exactly. The output must be a markdown table with 4–6 rows covering: ")
		sb.WriteString("financing (DSCR), cap rate integrity, vacancy/occupancy, renovation math, expense ratio, tax/regulatory claims, and any unverifiable market claims. ")
		sb.WriteString("Start with any pre-computed rows from the BROKER CLAIM VERIFICATION block (verbatim). ")
		sb.WriteString("Add the remaining rows from your own analysis. Add one explanation sentence per row. ")
		sb.WriteString("End with **Overall Broker Credibility: High / Medium / Low** and one sentence of context. ")
		sb.WriteString("Do not write this as prose paragraphs — it must be a table.\n\n")

		sb.WriteString("### Estara Sensitivity Scenario\n")
		sb.WriteString("Include the ESTARA SENSITIVITY SCENARIO table verbatim. ")
		sb.WriteString("Then write exactly one sentence: lead with the specific dollar figure from the conservative scenario, end with the action required. ")
		sb.WriteString("Format: '$[amount] mark-to-market loss in the conservative scenario — [action].' ")
		sb.WriteString("Do NOT open with 'while', 'although', 'however', or any subordinate clause. Number first, action last.\n\n")
	}

	sb.WriteString("---\n")
	sb.WriteString("Write like a senior acquisitions professional presenting to a partner: numbers-first, declarative, no hedge-stacking. ")
	sb.WriteString("Every sentence ends with a conclusion. Specific submarket names, not 'markets of this type'. ")
	sb.WriteString("The Verdict and IC Brief are the sharpest sections — write those last and cut anything that dilutes the signal.")

	return sb.String()
}

// coalesceFloat returns the first non-nil float64 pointer.
func coalesceFloat(a, b *float64) *float64 {
	if a != nil { return a }
	return b
}

// ---------------------------------------------------------------------------
// Pass 3 — Extraction validation
// ---------------------------------------------------------------------------

// omExtractionIssue is a single field-level discrepancy found during validation.
// Stored in pipeline_properties.extraction_issues and surfaced to the user in the wizard.
type omExtractionIssue struct {
	Field    string `json:"field"`    // e.g. "buildingCount"
	Tab      string `json:"tab"`      // "property" | "pricing" | "income" | "expenses" | "rentroll"
	Severity string `json:"severity"` // "high" | "medium"
	OmSays   string `json:"omSays"`   // verbatim quote or close paraphrase from OM
	Message  string `json:"message"`  // user-facing: "OM states '3 x 6-Unit Buildings' — please verify Number of Buildings"
}

const omExtractionCheckSystemPrompt = `You are auditing a real estate data extraction. You will be given:
1. Key facts quoted verbatim from the offering memorandum
2. The extracted data JSON

Your job: identify fields that were clearly missed or extracted incorrectly.

Rules:
- Only flag EXPLICIT contradictions — the OM fact plainly states a value but extraction returned null or a wrong value
- Do NOT flag inferences or calculations (e.g. do not say cap rate could be derived)
- Maximum 5 issues — only the most impactful ones for a buyer's decision
- Prioritize: buildingCount, totalUnits, stories, units, askingPrice, yearBuilt, brokerNOI, vacancyPct
- message must be plain English written for a busy real estate professional (not a developer)
- Return valid JSON only — no markdown, no prose`

const omExtractionCheckUserPrompt = `Key facts from the offering memorandum (verbatim quotes):
%s

Extracted data (key fields only):
%s

Identify any obvious misses. Return a JSON array, or [] if none found:
[{
  "field": string,      // exact field name that was missed or wrong
  "tab": "property" | "pricing" | "income" | "expenses" | "rentroll",
  "severity": "high" | "medium",
  "omSays": string,     // the relevant key fact (short, verbatim)
  "message": string     // e.g. "OM states '3 x 6-Unit Buildings' — please verify Number of Buildings"
}]`

// validateOMExtraction runs a lightweight text-only validation pass using keyFacts
// captured during Pass 2. No PDF re-read — uses Haiku for speed.
func (h *Handler) validateOMExtraction(ctx context.Context, keyFacts []string, extracted *omData) []omExtractionIssue {
	if len(keyFacts) == 0 {
		return nil
	}

	// Build a slim extracted-data summary — only checkable high-value fields.
	type extractedSummary struct {
		BuildingCount  *int     `json:"buildingCount"`
		TotalUnits     *int     `json:"totalUnits"`
		Units          int      `json:"rentRollUnitCount"` // sum from rentByUnitType
		Stories        *int     `json:"stories"`
		YearBuilt      *int     `json:"yearBuilt"`
		BuildingSqft   *float64 `json:"buildingSqft"`
		AskingPrice    *float64 `json:"askingPrice"`
		CapRate        *float64 `json:"capRate"`
		BrokerNOI      *float64 `json:"brokerNOI"`
		GPRCurrent     *float64 `json:"grossPotentialRentCurrent"`
		VacancyPct     *float64 `json:"vacancyPct"`
		ExpenseCount   int      `json:"expenseItemCount"`
		RentRollRows   int      `json:"rentRollRows"`
	}
	unitCount := 0
	for _, r := range extracted.RentByUnitType {
		if r.Count != nil {
			unitCount += *r.Count
		}
	}
	summary := extractedSummary{
		BuildingCount: extracted.BuildingCount,
		TotalUnits:    extracted.TotalUnits,
		Units:         unitCount,
		Stories:       extracted.Stories,
		YearBuilt:     extracted.YearBuilt,
		BuildingSqft:  extracted.BuildingSqft,
		AskingPrice:   extracted.AskingPrice,
		CapRate:       extracted.CapRate,
		BrokerNOI:     extracted.BrokerNOI,
		GPRCurrent:    extracted.GrossPotentialRentCurrent,
		VacancyPct:    extracted.VacancyPct,
		ExpenseCount:  len(extracted.ExpenseItems),
		RentRollRows:  len(extracted.RentByUnitType),
	}
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")

	// Format key facts as numbered list.
	var factsStr strings.Builder
	for i, f := range keyFacts {
		fmt.Fprintf(&factsStr, "%d. %s\n", i+1, f)
	}

	userMsg := fmt.Sprintf(omExtractionCheckUserPrompt, factsStr.String(), string(summaryJSON))

	reqBody := anthropicDocumentRequest{
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 1024,
		System:    omExtractionCheckSystemPrompt,
		Messages: []anthropicDocumentMsg{
			{Role: "user", Content: []interface{}{anthropicTextBlock{Type: "text", Text: userMsg}}},
		},
	}

	bodyBytes, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil
	}
	defer httpResp.Body.Close()
	respBytes, _ := io.ReadAll(httpResp.Body)

	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(respBytes, &envelope) != nil || len(envelope.Content) == 0 {
		return nil
	}
	rawText := strings.TrimSpace(envelope.Content[0].Text)
	// Strip markdown fences if present.
	if strings.HasPrefix(rawText, "```") {
		lines := strings.Split(rawText, "\n")
		if len(lines) >= 3 {
			rawText = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	var issues []omExtractionIssue
	if json.Unmarshal([]byte(rawText), &issues) != nil {
		return nil
	}
	return issues
}

// parseUSAddressParts extracts street, city, state, and ZIP from a standard US address string.
// Handles "Street, City, ST NNNNN" and "Street, City, ST NNNNN-NNNN" formats.
// Returns empty strings if pattern does not match.
// street is the portion before the city — useful for storing only the street in the address column.
var usAddrRe = regexp.MustCompile(`,\s*([A-Za-z]{2})\s+(\d{5}(?:-\d{4})?)$`)

func parseUSAddressParts(addr string) (street, city, state, zip string) {
	addr = strings.TrimSpace(addr)
	loc := usAddrRe.FindStringIndex(addr)
	if loc == nil {
		return
	}
	m := usAddrRe.FindStringSubmatch(addr)
	state = strings.ToUpper(m[1])
	zip = m[2]
	rest := strings.TrimSpace(addr[:loc[0]])
	parts := strings.Split(rest, ",")
	city = strings.TrimSpace(parts[len(parts)-1])
	if len(parts) > 1 {
		street = strings.TrimSpace(strings.Join(parts[:len(parts)-1], ","))
	} else {
		street = rest // no city separator found; keep rest as street
	}
	return
}

// omComputeUnits sums unit counts from OM rent roll rows.
func omComputeUnits(rows []omRentByUnitType) *int {
	if len(rows) == 0 {
		return nil
	}
	total := 0
	for _, r := range rows {
		if r.Count != nil {
			total += *r.Count
		}
	}
	if total == 0 {
		return nil
	}
	return &total
}

// omParseRentString tries to parse a dollar/numeric string like "$1,495" or "1495" to float64.
func omParseRentString(s string) *float64 {
	clean := strings.NewReplacer("$", "", ",", "").Replace(strings.TrimSpace(s))
	// For ranges like "750-900", take the average.
	if idx := strings.Index(clean, "-"); idx > 0 {
		lo, err1 := strconv.ParseFloat(clean[:idx], 64)
		hi, err2 := strconv.ParseFloat(clean[idx+1:], 64)
		if err1 == nil && err2 == nil {
			avg := (lo + hi) / 2
			return &avg
		}
	}
	if v, err := strconv.ParseFloat(clean, 64); err == nil {
		return &v
	}
	return nil
}

// omRentToUnitMixJSON converts OM rent-by-unit-type rows to the UnitMixRow JSONB format
// expected by the wizard and stored in the unit_mix column.
func omRentToUnitMixJSON(rows []omRentByUnitType) []byte {
	if len(rows) == 0 {
		return nil
	}
	type unitMixRow struct {
		Type         string   `json:"type"`
		Count        int      `json:"count"`
		Beds         *int     `json:"beds"`
		Baths        *float64 `json:"baths"`
		SqftPerUnit  *float64 `json:"sqftPerUnit"`
		RentCurrent  *float64 `json:"rentCurrent"`
		RentProForma *float64 `json:"rentProForma"`
		RentMarket   *float64 `json:"rentMarket"`
		OccupancyPct *float64 `json:"occupancyPct"`
		PricePerUnit *float64 `json:"pricePerUnit"`
		BuildingLabel *string `json:"buildingLabel"`
	}
	out := make([]unitMixRow, 0, len(rows))
	for _, r := range rows {
		beds := r.Bedrooms
		cnt := 0
		if r.Count != nil {
			cnt = *r.Count
		}
		// Resolve market rent: prefer numeric avg, fall back to parsing string.
		var mktRent *float64
		if r.RentMarket != nil {
			mktRent = omParseRentString(*r.RentMarket)
		}
		out = append(out, unitMixRow{
			Type:         r.UnitType,
			Count:        cnt,
			Beds:         &beds,
			SqftPerUnit:  r.SqftPerUnit,
			RentCurrent:  r.RentCurrent,
			RentProForma: r.RentProForma,
			RentMarket:   mktRent,
		})
	}
	b, _ := json.Marshal(out)
	return b
}

// omExpenseItemsJSON converts OM expense items to the ExpenseItem JSONB format.
func omExpenseItemsJSON(items []omExpenseItem) []byte {
	if len(items) == 0 {
		return nil
	}
	type expenseItem struct {
		Label  string   `json:"label"`
		Amount float64  `json:"amount"`
		Pct    *float64 `json:"pct,omitempty"`
	}
	out := make([]expenseItem, 0, len(items))
	for _, e := range items {
		out = append(out, expenseItem{Label: e.Label, Amount: e.Amount, Pct: e.PctOfEGI})
	}
	b, _ := json.Marshal(out)
	return b
}

// omBrokerContactJSON converts an OM broker contact to the BrokerContact JSONB format.
func omBrokerContactJSON(bc *omBrokerContact) []byte {
	if bc == nil {
		return nil
	}
	type brokerContact struct {
		Name    *string `json:"name"`
		Title   *string `json:"title"`
		Company *string `json:"company"`
		Phone   *string `json:"phone"`
		Email   *string `json:"email"`
		License *string `json:"license"`
	}
	b, _ := json.Marshal(brokerContact{
		Name:    bc.Name,
		Title:   bc.Title,
		Company: bc.Company,
		Phone:   bc.Phone,
		Email:   bc.Email,
		License: bc.LicenseNumber,
	})
	return b
}

// omTenantsJSON converts OM tenant schedule rows to the CommercialTenant JSONB format.
func omTenantsJSON(tenants []omTenant) []byte {
	if len(tenants) == 0 {
		return nil
	}
	type commercialTenant struct {
		TenantName       *string  `json:"tenantName"`
		Sqft             *float64 `json:"sqft"`
		MonthlyRent      *float64 `json:"monthlyRent"`
		AnnualRent       *float64 `json:"annualRent"`
		LeaseType        *string  `json:"leaseType"`
		LeaseExpiry      *string  `json:"leaseExpiry"`
		AnnualRentBumpPct *float64 `json:"annualRentBumpPct"`
	}
	out := make([]commercialTenant, 0, len(tenants))
	for _, t := range tenants {
		name := t.TenantName
		var monthlyRent *float64
		if t.AnnualRent != nil {
			v := *t.AnnualRent / 12
			monthlyRent = &v
		}
		out = append(out, commercialTenant{
			TenantName:       &name,
			Sqft:             t.SquareFeet,
			MonthlyRent:      monthlyRent,
			AnnualRent:       t.AnnualRent,
			LeaseType:        t.LeaseType,
			LeaseExpiry:      t.LeaseExpiry,
			AnnualRentBumpPct: t.AnnualRentBumpPct,
		})
	}
	b, _ := json.Marshal(out)
	return b
}

// omOtherIncomeJSON converts OM other income items to the OtherIncomeItem JSONB format.
func omOtherIncomeJSON(items []omOtherIncomeItem) []byte {
	if len(items) == 0 {
		return nil
	}
	type otherIncomeItem struct {
		Label  string  `json:"label"`
		Amount float64 `json:"amount"`
	}
	out := make([]otherIncomeItem, 0, len(items))
	for _, item := range items {
		out = append(out, otherIncomeItem{Label: item.Label, Amount: item.Amount})
	}
	b, _ := json.Marshal(out)
	return b
}

// omValueAddJSON converts an OM value-add block to JSONB bytes.
func omValueAddJSON(va *omValueAdd) []byte {
	if va == nil {
		return nil
	}
	b, _ := json.Marshal(va)
	return b
}

// omHighlightsJSON marshals a string slice to JSONB bytes, returning nil for empty/nil input.
func omHighlightsJSON(highlights []string) []byte {
	if len(highlights) == 0 {
		return nil
	}
	b, _ := json.Marshal(highlights)
	return b
}

// strPtrVal dereferences a *string safely, returning "" for nil.
func strPtrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// buildTypeSpecificMemoBlock returns type-dispatched analysis instructions for Claude.
func buildTypeSpecificMemoBlock(
	propType string,
	askingPrice, brokerCapRate, brokerRentMonthly float64,
	unitMixRows interface{ /* []unitRow - use any */ },
	hasUnitMix bool,
	commercialMixRows interface{},
	hasCommercialMix bool,
	om *omData,
	hasOMData bool,
) string {
	// Re-assert concrete types via closures since Go generics not used here.
	type unitRow struct {
		UnitType    string   `json:"type"`
		Count       int      `json:"count"`
		RentCurrent *float64 `json:"rentCurrent"`
		RentProForma *float64 `json:"rentProForma"`
		RentMarket  *float64 `json:"rentMarket"`
	}
	type cmTenantRow struct {
		TenantName       string   `json:"tenantName"`
		Sqft             *float64 `json:"sqft"`
		AnnualRent       *float64 `json:"annualRent"`
		LeaseType        *string  `json:"leaseType"`
		LeaseExpiry      *string  `json:"leaseExpiry"`
		AnnualRentBumpPct *float64 `json:"annualRentBumpPct"`
	}
	// Marshal + unmarshal to convert interface{} to concrete slices.
	var umRows []unitRow
	if b, err := json.Marshal(unitMixRows); err == nil {
		_ = json.Unmarshal(b, &umRows)
	}
	var cmRows []cmTenantRow
	if b, err := json.Marshal(commercialMixRows); err == nil {
		_ = json.Unmarshal(b, &cmRows)
	}

	var sb strings.Builder

	switch propType {
	case "multifamily", "student_housing":
		if !hasUnitMix { return "" }
		sb.WriteString("\n**MULTIFAMILY ANALYSIS CONTEXT** (incorporate into Financial Snapshot and Risk Factors):\n")
		// Compute value-add upside.
		totalUpside := 0.0
		for _, u := range umRows {
			if u.RentCurrent != nil && u.RentMarket != nil && u.Count > 0 {
				upliftPerUnit := *u.RentMarket - *u.RentCurrent
				if upliftPerUnit > 0 {
					totalUpside += upliftPerUnit * float64(u.Count) * 12
				}
			}
		}
		if totalUpside > 0 {
			sb.WriteString(fmt.Sprintf("- Rent roll upside: $%.0f/yr annualised value-add potential across all unit types\n", totalUpside))
		}
		// Per-unit-type breakdown.
		for _, u := range umRows {
			line := fmt.Sprintf("- %s (%d units)", u.UnitType, u.Count)
			if u.RentCurrent != nil { line += fmt.Sprintf(": current $%.0f/mo", *u.RentCurrent) }
			if u.RentMarket != nil  { line += fmt.Sprintf(", market $%.0f/mo", *u.RentMarket) }
			if u.RentCurrent == nil { line += " — market rent absent, flag for due diligence" }
			sb.WriteString(line + "\n")
		}
		// Vacancy flags.
		if hasOMData && om.VacancyPct != nil {
			vp := *om.VacancyPct * 100
			if vp < 5 {
				sb.WriteString(fmt.Sprintf("- Vacancy assumption of %.1f%% is optimistic — verify against submarket\n", vp))
			} else if vp > 15 {
				sb.WriteString(fmt.Sprintf("- Vacancy assumption of %.1f%% signals distressed occupancy — investigate cause\n", vp))
			}
		}
		// Management fee flag.
		if hasOMData && om.EffectiveGrossIncome != nil && om.EffectiveGrossIncome != nil {
			egi := *om.EffectiveGrossIncome
			// Check expense items for management fee.
			for _, exp := range om.ExpenseItems {
				if strings.Contains(strings.ToLower(exp.Label), "manag") && egi > 0 {
					mgmtPct := exp.Amount / egi * 100
					if mgmtPct < 8 {
						sb.WriteString(fmt.Sprintf(
							"- Management fee appears understated at %.1f%% of EGI — restate at 9%% ($%.0f/yr impact)\n",
							mgmtPct, egi*0.01))
					}
				}
			}
		}
		// NOI integrity cross-check.
		if hasOMData && om.NOISummaryStated != nil && om.NOIComputedFromStatement != nil {
			delta := *om.NOISummaryStated - *om.NOIComputedFromStatement
			if delta != 0 {
				sb.WriteString(fmt.Sprintf(
					"- NOI integrity: summary states $%.0f vs P&L computes $%.0f (delta $%.0f) — investigate\n",
					*om.NOISummaryStated, *om.NOIComputedFromStatement, delta))
			}
		}

	case "nnn", "retail", "office", "commercial":
		if !hasCommercialMix { return "" }
		sb.WriteString("\n**COMMERCIAL / NNN ANALYSIS CONTEXT** (incorporate into Financial Snapshot and Risk Factors):\n")
		// WALR computation.
		now := time.Now()
		var weightedSum, rentSum float64
		for _, t := range cmRows {
			if t.AnnualRent == nil || *t.AnnualRent <= 0 || t.LeaseExpiry == nil { continue }
			expiry, err := parseLeaseExpiry(*t.LeaseExpiry)
			if err != nil { continue }
			yrs := expiry.Sub(now).Hours() / (365.25 * 24)
			if yrs < 0 { yrs = 0 }
			weightedSum += *t.AnnualRent * yrs
			rentSum += *t.AnnualRent
		}
		walr := 0.0
		if rentSum > 0 { walr = weightedSum / rentSum }
		if walr > 0 {
			riskLabel := "acceptable"
			if walr < 3 {
				riskLabel = "HIGH — near-term rollover risk"
			} else if walr < 5 {
				riskLabel = "MEDIUM"
			}
			sb.WriteString(fmt.Sprintf("- WALR: %.1f years — re-leasing risk: %s\n", walr, riskLabel))
		}
		// Per-tenant analysis.
		var totalAnnualRent, totalSF float64
		for _, t := range cmRows {
			line := fmt.Sprintf("- %s", t.TenantName)
			if t.Sqft != nil { line += fmt.Sprintf(": %.0f SF", *t.Sqft); totalSF += *t.Sqft }
			if t.AnnualRent != nil { line += fmt.Sprintf(", $%.0f/yr", *t.AnnualRent); totalAnnualRent += *t.AnnualRent }
			if t.LeaseType != nil  { line += fmt.Sprintf(", %s", *t.LeaseType) }
			if t.LeaseExpiry != nil {
				expiry, err := parseLeaseExpiry(*t.LeaseExpiry)
				if err == nil {
					yrs := expiry.Sub(now).Hours() / (365.25 * 24)
					line += fmt.Sprintf(", expires %s", *t.LeaseExpiry)
					if yrs < 3 {
						line += " ⚠ expires within 36 months"
					}
				}
			}
			if t.AnnualRentBumpPct != nil {
				line += fmt.Sprintf(", %.1f%%/yr bump", *t.AnnualRentBumpPct*100)
			}
			sb.WriteString(line + "\n")
		}
		// Cap rate integrity.
		if brokerCapRate > 0 && askingPrice > 0 && totalAnnualRent > 0 {
			// Estimate NOI as totalAnnualRent × 0.70 (30% expense ratio) if no direct NOI.
			estimatedNOI := totalAnnualRent * 0.70
			impliedCapRate := estimatedNOI / askingPrice
			statedCapRate := brokerCapRate
			deltaBps := (impliedCapRate - statedCapRate) * 10000
			if deltaBps != 0 {
				sb.WriteString(fmt.Sprintf(
					"- Cap rate: stated %.2f%% vs implied %.2f%% from tenant schedule (delta %.0f bps) — verify expense assumptions\n",
					statedCapRate*100, impliedCapRate*100, deltaBps))
			}
		}
		if totalSF > 0 {
			sb.WriteString(fmt.Sprintf("- Total leased area: %.0f SF\n", totalSF))
		}

	case "industrial", "warehouse":
		sb.WriteString("\n**INDUSTRIAL / WAREHOUSE ANALYSIS CONTEXT** (incorporate into Financial Snapshot and Risk Factors):\n")
		if hasCommercialMix {
			sb.WriteString("- Tenant information available — assess lease term and rollover risk\n")
			for _, t := range cmRows {
				line := fmt.Sprintf("- Tenant: %s", t.TenantName)
				if t.AnnualRent != nil { line += fmt.Sprintf(", $%.0f/yr", *t.AnnualRent) }
				if t.LeaseExpiry != nil { line += fmt.Sprintf(", expires %s", *t.LeaseExpiry) }
				sb.WriteString(line + "\n")
			}
		} else {
			sb.WriteString("- No tenant data — assess as vacant industrial: underwrite time-to-lease risk\n")
		}
		sb.WriteString("- Assess replacement cost vs asking price/SF to identify margin of safety\n")
		sb.WriteString("- Note lot SF vs building SF for expansion optionality or excess land value\n")

	case "mixed_use":
		hasMFBlock := hasUnitMix
		hasCMBlock := hasCommercialMix
		if !hasMFBlock && !hasCMBlock { return "" }
		sb.WriteString("\n**MIXED-USE ANALYSIS CONTEXT** (apply both lenses below, then synthesise):\n")
		if hasMFBlock {
			sb.WriteString("Residential Component:\n")
			for _, u := range umRows {
				line := fmt.Sprintf("- %s (%d units)", u.UnitType, u.Count)
				if u.RentCurrent != nil { line += fmt.Sprintf(": $%.0f/mo", *u.RentCurrent) }
				if u.RentMarket != nil  { line += fmt.Sprintf(", market $%.0f/mo", *u.RentMarket) }
				sb.WriteString(line + "\n")
			}
		}
		if hasCMBlock {
			sb.WriteString("Commercial Component:\n")
			for _, t := range cmRows {
				line := fmt.Sprintf("- %s", t.TenantName)
				if t.AnnualRent != nil { line += fmt.Sprintf(": $%.0f/yr", *t.AnnualRent) }
				if t.LeaseExpiry != nil { line += fmt.Sprintf(", expires %s", *t.LeaseExpiry) }
				sb.WriteString(line + "\n")
			}
		}
		sb.WriteString("- Synthesise: assess income diversification benefit vs complexity premium\n")

	case "sfh", "condo", "townhouse":
		sb.WriteString("\n**RESIDENTIAL INVESTMENT CONTEXT** (incorporate into Financial Snapshot):\n")
		if brokerRentMonthly > 0 {
			sb.WriteString(fmt.Sprintf("- Stated rent: $%.0f/mo\n", brokerRentMonthly))
		}
		if askingPrice > 0 && brokerRentMonthly > 0 {
			grm := askingPrice / (brokerRentMonthly * 12)
			sb.WriteString(fmt.Sprintf("- Gross rent multiplier: %.1f× — assess against local residential market\n", grm))
		}
		sb.WriteString("- Assess long-term appreciation drivers and rental demand depth for this market\n")
	}

	return sb.String()
}

// callClaudeForMemo calls Claude and returns the memo text.
func (h *Handler) callClaudeForMemo(ctx context.Context, prompt string) (string, error) {
	const anthropicURL = "https://api.anthropic.com/v1/messages"

	reqBody := struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 8192,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "user", Content: prompt},
		},
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(reqJSON))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 240 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("API call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s", anthropicUserMessage(resp.StatusCode, body))
	}

	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	for _, block := range envelope.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("no text content in response")
}

// ---------------------------------------------------------------------------
// ADR-107 (revised): New-OM wizard endpoints
// ---------------------------------------------------------------------------

// omCheckResult is returned by CheckOM.
type omCheckResult struct {
	IsOM       bool   `json:"isOM"`
	Confidence string `json:"confidence"` // "high" | "medium" | "low"
	Reason     string `json:"reason"`
}

// omValidationIssue represents a data quality issue found in an extracted OM.
type omValidationIssue struct {
	Field    string `json:"field"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "required" | "warning"
}

// omExtractionResult is returned by ExtractOM.
type omExtractionResult struct {
	Extraction       *omData             `json:"extraction"`
	ValidationIssues []omValidationIssue `json:"validationIssues"`
	FileName         string              `json:"fileName"`
	FileType         string              `json:"fileType"`
}

const checkOMSystemPrompt = `You are analyzing a document to determine if it is a commercial real estate Offering Memorandum (OM), Investment Memorandum, or Investment Summary.

An OM is a sales document used to market investment properties to buyers. It typically contains:
- Property address and physical description
- Asking price or guidance price
- Financial information (cap rate, NOI, rental income, rent roll)
- Investment highlights
- Market overview

Examine the provided document and respond with JSON only, no other text:
{"isOM":true,"confidence":"high","reason":"one-sentence explanation"}`

// CheckOM handles POST /api/pipeline/check-om
// Lightweight step 1 of the new-OM wizard: determines if the uploaded file is an OM.
func (h *Handler) CheckOM(w http.ResponseWriter, r *http.Request) {
	_, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+1024)
	if err := r.ParseMultipartForm(maxDocumentSize); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.Error(w, http.StatusRequestEntityTooLarge, "File exceeds the 10MB limit.")
			return
		}
		httputil.BadRequest(w, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer file.Close()

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
		httputil.BadRequest(w, "Unsupported file type. Upload PDF, Excel (.xlsx/.xls), or text (.txt/.csv).")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("read file: %w", err))
		return
	}

	result := omCheckResult{IsOM: true, Confidence: "high", Reason: "Document accepted for review."}

	if h.cfg.AI.AnthropicAPIKey != "" {
		rawText, err := h.callClaudeRaw(r.Context(), checkOMSystemPrompt, "Analyze this document.", fileBytes, mediaType)
		if err != nil {
			h.logger.Warn("CheckOM Claude call failed", "error", err)
			// Fail open: allow the wizard to proceed if Claude is unavailable.
		} else {
			clean := strings.TrimSpace(rawText)
			clean = strings.TrimPrefix(clean, "```json")
			clean = strings.TrimPrefix(clean, "```")
			clean = strings.TrimSuffix(clean, "```")
			clean = strings.TrimSpace(clean)
			var parsed omCheckResult
			if jerr := json.Unmarshal([]byte(clean), &parsed); jerr == nil {
				result = parsed
			}
		}
	}

	httputil.Success(w, result)
}

// callClaudeRaw calls Claude with a custom system prompt and returns the raw text response.
// Used for short structured tasks (e.g. CheckOM) where we don't need omData parsing.
func (h *Handler) callClaudeRaw(ctx context.Context, systemPrompt, userPrompt string, fileBytes []byte, mediaType string) (string, error) {
	const anthropicURL = "https://api.anthropic.com/v1/messages"

	var reqBody anthropicDocumentRequest

	if mediaType == "application/pdf" {
		reqBody = anthropicDocumentRequest{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 512,
			System:    systemPrompt,
			Messages: []anthropicDocumentMsg{
				{
					Role: "user",
					Content: []interface{}{
						anthropicDocBlock{
							Type: "document",
							Source: anthropicDocSource{
								Type:      "base64",
								MediaType: "application/pdf",
								Data:      encodeBase64(fileBytes),
							},
						},
						anthropicTextBlock{Type: "text", Text: userPrompt},
					},
				},
			},
		}
	} else {
		reqBody = anthropicDocumentRequest{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 512,
			System:    systemPrompt,
			Messages: []anthropicDocumentMsg{
				{
					Role: "user",
					Content: []interface{}{
						anthropicTextBlock{Type: "text", Text: userPrompt + "\n\n" + string(fileBytes[:min(len(fileBytes), 8000)])},
					},
				},
			},
		}
	}

	reqJSON, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(reqJSON))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", h.cfg.AI.AnthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "pdfs-2024-09-25")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("API call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	for _, block := range envelope.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("no text content in response")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ExtractOM handles POST /api/pipeline/extract-om
// Step 2 of the new-OM wizard: full extraction + validation issues. No DB writes.
func (h *Handler) ExtractOM(w http.ResponseWriter, r *http.Request) {
	_, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+1024)
	if err := r.ParseMultipartForm(maxDocumentSize); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.Error(w, http.StatusRequestEntityTooLarge, "File exceeds the 10MB limit.")
			return
		}
		httputil.BadRequest(w, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer file.Close()

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
		httputil.BadRequest(w, "Unsupported file type. Upload PDF, Excel (.xlsx/.xls), or text (.txt/.csv).")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("read file: %w", err))
		return
	}

	extracted, err := h.extractFullOMData(r.Context(), fileBytes, mediaType)
	if err != nil {
		msg := err.Error()
		if isRetryableAnthropicError(msg) {
			h.logger.Warn("ExtractOM: retryable AI error", "error", msg)
			httputil.Error(w, http.StatusServiceUnavailable, msg)
		} else {
			h.logger.Error("ExtractOM: extraction failed", "error", msg)
			httputil.Error(w, http.StatusInternalServerError, "Data extraction failed — please try again.")
		}
		return
	}

	issues := computeOMValidationIssues(extracted)

	httputil.Success(w, omExtractionResult{
		Extraction:       extracted,
		ValidationIssues: issues,
		FileName:         header.Filename,
		FileType:         mediaType,
	})
}

// computeOMValidationIssues returns data-quality issues for a freshly extracted OM.
// Checks presence of required fields (6 checks) and data quality / internal consistency (7 depth checks).
func computeOMValidationIssues(d *omData) []omValidationIssue {
	if d == nil {
		return []omValidationIssue{{Field: "document", Message: "No data could be extracted from this document.", Severity: "required"}}
	}
	var issues []omValidationIssue

	// --- Presence checks ---
	if d.AskingPrice == nil {
		issues = append(issues, omValidationIssue{Field: "askingPrice", Message: "Asking price not found — required for analysis.", Severity: "required"})
	}
	if d.BrokerNOI == nil && d.CapRate == nil {
		issues = append(issues, omValidationIssue{Field: "brokerNOI", Message: "Neither NOI nor cap rate found — at least one is needed for income analysis.", Severity: "required"})
	}
	if len(d.RentByUnitType) == 0 && d.BrokerNOI == nil {
		issues = append(issues, omValidationIssue{Field: "rentByUnitType", Message: "No rent roll or unit-type breakdown found.", Severity: "warning"})
	}
	if d.PropertyAddress == "" {
		issues = append(issues, omValidationIssue{Field: "propertyAddress", Message: "Property street address not extracted — required to identify the asset.", Severity: "required"})
	}
	if d.PropertyDescription == "" {
		issues = append(issues, omValidationIssue{Field: "propertyDescription", Message: "Property marketing description not extracted.", Severity: "warning"})
	}
	if d.CapRate != nil && (*d.CapRate > 0.40 || *d.CapRate < 0) {
		issues = append(issues, omValidationIssue{Field: "capRate", Message: fmt.Sprintf("Cap rate %.1f%% appears unusual — please verify.", *d.CapRate*100), Severity: "warning"})
	}
	if d.BrokerContact == nil {
		issues = append(issues, omValidationIssue{Field: "brokerContact", Message: "No broker contact information found.", Severity: "warning"})
	}

	// OM Date required — establishes timing of projections.
	// A quarter-only value (e.g. "Q4 2025") is not a real date and must be flagged.
	if d.OmDate == nil || *d.OmDate == "" {
		issues = append(issues, omValidationIssue{Field: "omDate", Message: "OM date not stated — required to establish timing of broker projections.", Severity: "required"})
	} else if !omDateIsUsable(*d.OmDate) {
		issues = append(issues, omValidationIssue{
			Field:    "omDate",
			Message:  fmt.Sprintf("OM date %q is not a specific date — quarter or year-only values are too imprecise; a month and year (e.g. \"December 2024\") is required.", *d.OmDate),
			Severity: "required",
		})
	}

	// Year Built required — distinct from Year Renovated.
	if d.YearBuilt == nil {
		issues = append(issues, omValidationIssue{Field: "yearBuilt", Message: "Year Built not extracted — required for physical assessment and depreciation analysis.", Severity: "required"})
	}

	// Parking required for non-SFR (multiple unit types → multifamily/commercial).
	if d.Parking == nil && len(d.RentByUnitType) > 1 {
		issues = append(issues, omValidationIssue{Field: "parking", Message: "Parking not stated — required for multifamily and commercial properties.", Severity: "required"})
	}

	// Broker contact incomplete — name missing or no way to contact.
	if d.BrokerContact != nil {
		if d.BrokerContact.Name == nil || *d.BrokerContact.Name == "" {
			issues = append(issues, omValidationIssue{Field: "brokerContact.name", Message: "Broker name not stated — contact information incomplete.", Severity: "required"})
		}
		phoneEmpty := d.BrokerContact.Phone == nil || *d.BrokerContact.Phone == ""
		emailEmpty := d.BrokerContact.Email == nil || *d.BrokerContact.Email == ""
		if phoneEmpty && emailEmpty {
			issues = append(issues, omValidationIssue{Field: "brokerContact.contact", Message: "Broker has no phone or email — cannot follow up on this property.", Severity: "required"})
		}
	}

	// Commercial space in rent roll with no details — invalid row.
	for _, row := range d.RentByUnitType {
		lower := strings.ToLower(row.UnitType)
		isCommercial := strings.Contains(lower, "commercial") || strings.Contains(lower, "retail") || strings.Contains(lower, "office")
		if isCommercial && row.Count == nil && row.RentCurrent == nil {
			issues = append(issues, omValidationIssue{
				Field:    "rentByUnitType.commercial",
				Message:  fmt.Sprintf("Commercial space (%q) listed in rent roll but no unit count or rent data — invalid row.", row.UnitType),
				Severity: "required",
			})
			break // flag once even if multiple commercial rows with missing data
		}
	}

	// --- Depth checks ---

	// 1. Rent roll unit counts — if all rows have nil Count, GPR/NOI/IRR cannot be computed.
	// Required (not just a warning) because analysis is impossible without unit counts on a multi-unit property.
	if len(d.RentByUnitType) > 0 {
		allCountNil := true
		for _, row := range d.RentByUnitType {
			if row.Count != nil {
				allCountNil = false
				break
			}
		}
		if allCountNil {
			issues = append(issues, omValidationIssue{
				Field:    "rentByUnitType.count",
				Message:  "Unit counts missing from all rent roll rows — GPR, NOI and cap rate cannot be computed without them.",
				Severity: "required",
			})
		} else {
			// 2. Partial market rents — some but not all rows have RentMarket set.
			var missingMarketRent []string
			for _, row := range d.RentByUnitType {
				if row.RentMarket == nil {
					missingMarketRent = append(missingMarketRent, row.UnitType)
				}
			}
			if len(missingMarketRent) > 0 && len(missingMarketRent) < len(d.RentByUnitType) {
				issues = append(issues, omValidationIssue{
					Field:    "rentByUnitType.rentMarket",
					Message:  fmt.Sprintf("Market rents not stated for: %s — pro forma assumptions are unanchored.", strings.Join(missingMarketRent, ", ")),
					Severity: "warning",
				})
			}
		}
	}

	// 3. Implied vs stated cap rate — flag if discrepancy exceeds 150bps.
	if d.AskingPrice != nil && *d.AskingPrice > 0 && d.BrokerNOI != nil && d.CapRate != nil {
		impliedCapRate := *d.BrokerNOI / *d.AskingPrice
		delta := impliedCapRate - *d.CapRate
		if delta < 0 {
			delta = -delta
		}
		if delta > 0.015 {
			issues = append(issues, omValidationIssue{
				Field: "capRate",
				Message: fmt.Sprintf(
					"Implied cap rate %.2f%% differs from stated %.2f%% by %dbps — verify NOI and price.",
					impliedCapRate*100, *d.CapRate*100, int(delta*10000+0.5),
				),
				Severity: "warning",
			})
		}
	}

	// 4. Aggressive NOI growth — stabilized NOI > 20% above Year-1.
	if d.BrokerNOI != nil && *d.BrokerNOI > 0 && d.BrokerNOIStabilized != nil {
		growth := *d.BrokerNOIStabilized / *d.BrokerNOI
		if growth > 1.20 {
			issues = append(issues, omValidationIssue{
				Field:    "brokerNOIStabilized",
				Message:  fmt.Sprintf("Stabilized NOI is %.0f%% above Year-1 — aggressive value-add assumption.", (growth-1)*100),
				Severity: "warning",
			})
		}
	}

	// 5. Pro forma vs current cap rate gap — flag if spread exceeds 100bps.
	if d.CapRate != nil && d.CapRateProForma != nil {
		gap := *d.CapRateProForma - *d.CapRate
		if gap < 0 {
			gap = -gap
		}
		if gap > 0.01 {
			issues = append(issues, omValidationIssue{
				Field:    "capRateProForma",
				Message:  fmt.Sprintf("Pro forma cap rate %.2f%% is %.0fbps from current %.2f%% — value-add execution risk.", *d.CapRateProForma*100, gap*10000, *d.CapRate*100),
				Severity: "warning",
			})
		}
	}

	// 6. "Bad debt" vacancy label — credit loss ≠ physical vacancy; understates economic vacancy.
	if d.VacancyLabel != nil && strings.Contains(strings.ToLower(*d.VacancyLabel), "bad debt") {
		issues = append(issues, omValidationIssue{
			Field:    "vacancyLabel",
			Message:  `Vacancy stated as "bad debt" (credit loss), not physical vacancy — economic vacancy may be understated.`,
			Severity: "warning",
		})
	}

	// 7. No expense schedule — NOI cannot be verified from components.
	if d.TotalExpenses == nil && len(d.ExpenseLineItems) < 2 {
		issues = append(issues, omValidationIssue{
			Field:    "totalExpenses",
			Message:  "No expense schedule found — NOI cannot be verified from its components.",
			Severity: "warning",
		})
	}

	return issues
}

// omDateIsUsable returns true if s is a specific enough date to be useful (month+year minimum).
// Quarter-only ("Q4 2025") and year-only ("2025") values return false.
func omDateIsUsable(s string) bool {
	upper := strings.ToUpper(strings.TrimSpace(s))
	// Reject quarter patterns: Q1–Q4 followed by a year.
	if len(upper) >= 2 && upper[0] == 'Q' && upper[1] >= '1' && upper[1] <= '4' {
		return false
	}
	// Reject bare 4-digit year.
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 4 {
		allDigits := true
		for _, c := range trimmed {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return false
		}
	}
	// Try standard date formats (month+year minimum).
	for _, f := range []string{
		"January 2, 2006", "Jan 2, 2006",
		"01/02/2006", "1/2/2006",
		"2006-01-02",
		"January 2006", "Jan 2006",
		"2006-01",
		"January, 2006",
	} {
		if _, err := time.Parse(f, trimmed); err == nil {
			return true
		}
	}
	// Fallback: accept if string contains a recognisable month name AND a 4-digit year.
	lower := strings.ToLower(trimmed)
	hasMonth := false
	for _, m := range []string{
		"january", "february", "march", "april", "may", "june",
		"july", "august", "september", "october", "november", "december",
		"jan", "feb", "mar", "apr", "jun", "jul", "aug", "sep", "oct", "nov", "dec",
	} {
		if strings.Contains(lower, m) {
			hasMonth = true
			break
		}
	}
	hasYear := false
	for i := 0; i+4 <= len(trimmed); i++ {
		allD := true
		for _, c := range trimmed[i : i+4] {
			if c < '0' || c > '9' {
				allD = false
				break
			}
		}
		if allD {
			hasYear = true
			break
		}
	}
	return hasMonth && hasYear
}

// ReextractOM handles POST /api/pipeline/deals/{dealId}/properties/{propId}/om-reextract
// ADR-108: re-runs Pass 2 with a user-corrected property type, updates om_data.
// Pass 1 classification is preserved (not re-run). Used from the OM review sheet when
// the user changes the property type.
//
// Request body: { "propertyType": "nnn" }
func (h *Handler) ReextractOM(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		PropertyType string `json:"propertyType"`
	}
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.PropertyType == "" {
		httputil.BadRequest(w, "propertyType is required")
		return
	}

	// Load the stored OM file.
	row, err := h.store.Q().GetPropertyOMFile(r.Context(), queries.GetPropertyOMFileParams{
		ID:     propID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "property or OM file not found")
		return
	}

	// Read file bytes from volume or BYTEA.
	var fileBytes []byte
	if row.OmFilePath.Valid && row.OmFilePath.String != "" {
		baseDir := omFilesBaseDir()
		if baseDir == "" {
			httputil.Error(w, http.StatusServiceUnavailable, "OM file volume not configured")
			return
		}
		fullPath := filepath.Join(baseDir, row.OmFilePath.String)
		fileBytes, err = os.ReadFile(fullPath)
		if err != nil {
			h.logger.Error("failed to read OM file for re-extraction", "error", err, "path", fullPath)
			httputil.InternalError(w, fmt.Errorf("read OM file: %w", err))
			return
		}
	} else if len(row.OmFileData) > 0 {
		fileBytes = row.OmFileData
	} else {
		httputil.BadRequest(w, "no OM file stored for this property")
		return
	}

	mediaType := "application/pdf"
	if row.OmFileType.Valid && row.OmFileType.String != "" {
		mediaType = row.OmFileType.String
	}

	// Run Pass 2 only with the user-specified type.
	systemPrompt := omBuildExtractionSystemPrompt(req.PropertyType)
	omExtracted, err := h.callClaudeForOMData(r.Context(), mediaType, fileBytes, systemPrompt)
	if err != nil {
		h.logger.Error("ReextractOM Pass 2 failed", "propId", propID, "propertyType", req.PropertyType, "error", err)
		httputil.InternalError(w, err)
		return
	}

	// Preserve Pass 1 classification from existing om_data if available.
	prop, err := h.store.Q().GetPipelineProperty(r.Context(), queries.GetPipelinePropertyParams{
		ID:     propID,
		UserID: userID,
	})
	if err == nil && len(prop.OmData) > 2 {
		var existing omData
		if jerr := json.Unmarshal(prop.OmData, &existing); jerr == nil {
			if existing.DocumentClass != nil && omExtracted.DocumentClass == nil {
				omExtracted.DocumentClass = existing.DocumentClass
			}
			if existing.ClassificationConfidence != nil && omExtracted.ClassificationConfidence == nil {
				omExtracted.ClassificationConfidence = existing.ClassificationConfidence
			}
		}
	}
	// Stamp the user-requested property type.
	omExtracted.PropertyType = &req.PropertyType

	omDataJSON, jerr := json.Marshal(omExtracted)
	if jerr != nil {
		httputil.InternalError(w, fmt.Errorf("marshal re-extracted OM data: %w", jerr))
		return
	}

	updated, err := h.store.Q().UpdatePipelinePropertyOM(r.Context(), queries.UpdatePipelinePropertyOMParams{
		ID:            propID,
		OmData:        omDataJSON,
		BrokerCapRate: numericFromFloatPtr(omExtracted.CapRate),
		OmFilePath:    row.OmFilePath,
		OmFileData:    nil, // preserve existing — don't re-store
		OmFileName:    row.OmFileName,
		OmFileType:    row.OmFileType,
	})
	if err != nil {
		h.logger.Error("UpdatePipelinePropertyOM failed in ReextractOM", "error", err)
		httputil.InternalError(w, err)
		return
	}

	httputil.Success(w, map[string]any{
		"property": mapPropertyToResponse(updated),
		"omData":   json.RawMessage(omDataJSON),
	})
}

// MarkDealComplete handles PUT /api/pipeline/deals/{dealId}/complete
// ADR-107: marks a deal as input-complete, enabling analysis.
func (h *Handler) MarkDealComplete(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	dealID, err := uuid.Parse(chi.URLParam(r, "dealId"))
	if err != nil {
		httputil.BadRequest(w, "invalid deal ID")
		return
	}

	deal, err := h.store.Q().MarkDealInputComplete(r.Context(), queries.MarkDealInputCompleteParams{
		ID:     dealID,
		UserID: userID,
	})
	if err != nil {
		httputil.NotFound(w, "deal not found")
		return
	}

	httputil.Success(w, mapDealToResponse(deal))
}
