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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/queries"
	anthropic "github.com/estara-ai/www/internal/services/ai/anthropic"
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

	// Create the property — populate structured columns from OM extraction so edit form is pre-filled.
	propAddress := "See OM"
	if extractedAddress != "" {
		propAddress = extractedAddress
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
	prop, err := h.store.Q().CreatePipelineProperty(r.Context(), queries.CreatePipelinePropertyParams{
		PipelineDealID: deal.ID,
		Address:        propAddress,
		SourceType:     "document_upload",
		PropertyType:   textVal(extractedPropType),
		Notes:          textVal(parsedOM.PropertyDescription),
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

PROPERTY ADDRESS vs DESCRIPTION — ALWAYS EXTRACT SEPARATELY:
- propertyAddress = the full street address of the asset (e.g. "123 Main Street, Garden City, NY 11530"). Include street number, street name, city, state, and ZIP if present. This is a physical locator, not a description.
- propertyDescription = the marketing summary or tagline used to describe the asset (e.g. "19-unit mixed-use property", "Trophy Class-A office tower with below-market rents", "Value-add multifamily opportunity"). This is typically a short phrase on the cover page or in the executive summary. Do NOT put the address here.
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

- For financials: extract GPR (Gross Potential Rent), vacancy, EGI, individual expense line items, NOI.
- For expense line items: capture as array of [label, value] pairs (e.g. [["Property Taxes", "$42,000"], ["Insurance", "$8,500"]]).

RENT BY UNIT TYPE — IMPORTANT:
- OMs often state rents broken down by unit type (e.g. "studios $450 · 1bd $600 · 2bd $700").
- These are per-type rent rates, NOT unit counts. Do NOT put them in expenseLineItems.
- Extract them into rentByUnitType as one row per unit type, merging all stated information:
  * rentCurrent = broker-stated / current / in-place rent for that unit type (number)
  * rentProForma = pro forma / projected / stabilized rent (number)
  * rentMarket = market rent (string — may be a range like "$750-900")
  * count = number of units of this type (integer, if stated in a rent roll or unit mix table)
  * sqftPerUnit = rentable area per unit of this type in square feet (number, if stated)
  * parkingSlots = parking spaces included or allocated per unit of this type (integer, if stated)
  * amenities = unit-level amenities for this unit type as a string array (e.g. ["in-unit W/D", "balcony", "dishwasher", "A/C"]). Only include amenities that differ by unit type. If all units share the same amenities, leave this null and put them in buildingAmenities instead.
- Standard bedrooms mapping: studio/efficiency → 0, 1bd/1br → 1, 2bd/2br → 2, 3bd/3br → 3, 4bd → 4.
- Use a clean label for unitType: "Studio", "1 Bed", "2 Bed", "3 Bed", etc.
- Leave any field null if not stated for a given type.

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

const omExtractionUserPrompt = `Extract all information from this Offering Memorandum. Return this exact JSON structure (null for missing fields):

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
  "rentalYield": number | null,
  "propertyAddress": string | null,
  "propertyDescription": string | null,
  "investmentHighlights": string[] | null,
  "yearBuilt": number | null,
  "yearRenovated": number | null,
  "stories": number | null,
  "zoning": string | null,
  "construction": string | null,
  "parking": string | null,
  "buildingAmenities": string[] | null,
  "lotSqft": number | null,
  "buildingSqft": number | null,
  "rentByUnitType": [
    {
      "unitType": "Studio",
      "bedrooms": 0,
      "count": number | null,
      "sqftPerUnit": number | null,
      "parkingSlots": number | null,
      "rentCurrent": number | null,
      "rentProForma": number | null,
      "rentMarket": string | null,
      "amenities": string[] | null
    }
  ] | null,
  "brokerNOI": number | null,
  "brokerNOIStabilized": number | null,
  "grossPotentialRent": number | null,
  "effectiveGrossIncome": number | null,
  "totalExpenses": number | null,
  "expenseLineItems": [["label", "value"], ...] | null,
  "vacancyAssumption": number | null,
  "vacancyLabel": string | null,
  "dscr": number | null,
  "stabilizedCashOnCash": number | null,
  "financingInterestAnnual": number | null,
  "pricePerUnit": number | null,
  "pricePerSF": number | null,
  "grm": number | null,
  "yearOneCashOnCash": number | null,
  "fiveYearIRR": number | null,
  "assumableDebt": number | null,
  "marketOverviewText": string | null,
  "additionalMetrics": [{"label": string, "value": string}, ...] | null,
  "additionalSections": [{"title": string, "content": string}, ...] | null
}`

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
2. Full P&L: grossPotentialRentCurrent (and grossPotentialRentProforma if stated), vacancyPct, effectiveGrossIncome, all named expense rows in expenseItems (label, amount, pctOfEGI), total expenses, NOI.
3. NOI cross-check: extract noiSummaryStated (from cover/summary box) AND noiComputedFromStatement (from P&L) separately — even if they differ.
4. Return metrics: cashOnCashCurrent (Year 1 CoC), cashOnCashStabilized, fiveYearIRR, grm.
5. Assumable debt: populate assumableDebtDetail (balance, interestRate, maturityDate, loanType) if stated.
6. Value-add: populate valueAdd (lowEstimate, highEstimate, unrenovatedUnits, costPerUnit) if stated.
7. Broker market section: populate brokerMarket (vacancyRate, rentGrowthYoY, jobGrowth, medianHHI, walkScore, capRateComprBps, submarketName, dataSource).`
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

	// Build the memo prompt.
	prompt := buildPipelineMemoPrompt(deal.Name, props)

	sendEvent("progress", `{"phase":"Generating decision memo...","pct":30}`)

	// Use streaming Claude API so the SSE connection stays alive during generation.
	aiClient := anthropic.NewClient(anthropic.ClientConfig{
		APIKey:    h.cfg.AI.AnthropicAPIKey,
		Model:     "claude-sonnet-4-6",
		MaxTokens: 4096,
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

	// Bump memo count on the deal.
	_ = h.store.Q().BumpPipelineDealMemoCount(r.Context(), dealID)

	// Send the complete event with the full memo.
	memoJSON, _ := json.Marshal(map[string]any{
		"memo":   memo,
		"dealId": dealID.String(),
	})
	sendEvent("complete", string(memoJSON))
}

// buildPipelineMemoPrompt constructs the Claude prompt for a pipeline decision memo.
func buildPipelineMemoPrompt(dealName string, props []queries.PipelineProperty) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Generate a comprehensive investment decision memo for the following deal: **%s**\n\n", dealName))
	sb.WriteString("## Properties in this Deal\n\n")

	for i, p := range props {
		sb.WriteString(fmt.Sprintf("### Property %d: %s\n", i+1, p.Address))

		if p.City.Valid && p.State.Valid {
			sb.WriteString(fmt.Sprintf("**Location**: %s, %s\n", p.City.String, p.State.String))
		}
		if p.PropertyType.Valid {
			sb.WriteString(fmt.Sprintf("**Type**: %s\n", p.PropertyType.String))
		}
		if p.Units.Valid {
			sb.WriteString(fmt.Sprintf("**Units**: %d\n", p.Units.Int32))
		}
		if p.AskingPrice.Valid {
			f, _ := p.AskingPrice.Float64Value()
			sb.WriteString(fmt.Sprintf("**Asking Price**: $%.0f\n", f.Float64))
		}
		if p.TargetPrice.Valid {
			f, _ := p.TargetPrice.Float64Value()
			sb.WriteString(fmt.Sprintf("**Target Price**: $%.0f\n", f.Float64))
		}
		if p.BrokerRent.Valid {
			f, _ := p.BrokerRent.Float64Value()
			sb.WriteString(fmt.Sprintf("**Broker Rent**: $%.0f/mo\n", f.Float64))
		}
		if p.SystemRent.Valid {
			f, _ := p.SystemRent.Float64Value()
			sb.WriteString(fmt.Sprintf("**System Rent Estimate**: $%.0f/mo\n", f.Float64))
		}
		if p.BrokerCapRate.Valid {
			f, _ := p.BrokerCapRate.Float64Value()
			sb.WriteString(fmt.Sprintf("**Broker Cap Rate**: %.2f%%\n", f.Float64*100))
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
		if p.DownPaymentPct.Valid {
			f, _ := p.DownPaymentPct.Float64Value()
			sb.WriteString(fmt.Sprintf("**Down Payment**: %.0f%%\n", f.Float64*100))
		}
		if p.InterestRate.Valid {
			f, _ := p.InterestRate.Float64Value()
			sb.WriteString(fmt.Sprintf("**Interest Rate**: %.2f%%\n", f.Float64*100))
		}

		// ADR-107: prefer om_validated_data (OM + user-supplied answers merged) over raw om_data.
		omForMemo := p.OmData
		if len(p.OmValidatedData) > 0 && string(p.OmValidatedData) != "null" {
			omForMemo = p.OmValidatedData
		}

		// ADR-108: extract propertyType from om_data for type-specific guidance.
		var omParsed struct {
			PropertyType              *string  `json:"propertyType"`
			NOISummaryStated          *float64 `json:"noiSummaryStated"`
			NOIComputedFromStatement  *float64 `json:"noiComputedFromStatement"`
			TenantSchedule            []struct {
				TenantName  string   `json:"tenantName"`
				AnnualRent  *float64 `json:"annualRent"`
				LeaseExpiry *string  `json:"leaseExpiry"`
			} `json:"tenantSchedule"`
			BrokerMarket *struct {
				VacancyRate   *float64 `json:"vacancyRate"`
				RentGrowthYoY *float64 `json:"rentGrowthYoY"`
				SubmarketName *string  `json:"submarketName"`
			} `json:"brokerMarket"`
		}
		propTypeForMemo := ""
		if p.PropertyType.Valid {
			propTypeForMemo = p.PropertyType.String
		}
		if len(omForMemo) > 2 {
			if jerr := json.Unmarshal(omForMemo, &omParsed); jerr == nil {
				if omParsed.PropertyType != nil && propTypeForMemo == "" {
					propTypeForMemo = *omParsed.PropertyType
				}
			}
		}

		if len(omForMemo) > 0 && string(omForMemo) != "null" {
			label := "Offering Memorandum Data"
			if len(p.OmValidatedData) > 0 && string(p.OmValidatedData) != "null" {
				label = "Offering Memorandum Data (validated + user-corrected)"
			}
			sb.WriteString(fmt.Sprintf("\n**%s**:\n```json\n", label))
			// Truncate large OM blobs to keep the prompt within a manageable token budget.
			// Most critical fields (askingPrice, capRate, NOI, rentByUnitType) appear early in the JSON.
			const maxOMBytes = 3000
			if len(omForMemo) > maxOMBytes {
				sb.Write(omForMemo[:maxOMBytes])
				sb.WriteString("\n... [truncated for brevity]\n")
			} else {
				sb.Write(omForMemo)
			}
			sb.WriteString("\n```\n")
		}

		// Type-specific analysis instructions injected inline per property (ADR-108).
		switch propTypeForMemo {
		case "multifamily", "student_housing":
			sb.WriteString("\n**Multifamily-specific analysis required:**\n")
			sb.WriteString("- NOI cross-check: if `noiSummaryStated` and `noiComputedFromStatement` differ, flag the gap in basis points and explain what it means for the stated cap rate.\n")
			sb.WriteString("- Rent roll: compare `rentCurrent` vs `rentProForma` vs `rentMarket` per unit type — quantify the rent-to-market upside or risk.\n")
			sb.WriteString("- Vacancy: if `vacancyLabel` = 'bad debt', note this is credit loss not physical vacancy — adjust NOI sensitivity.\n")
			sb.WriteString("- Expense ratio: if `expenseRatioPct` available, benchmark against 35-45% norm for multifamily.\n")
			sb.WriteString("- Value-add: if `valueAdd` present, calculate total renovation cost vs. rent premium capture.\n")
		case "nnn", "retail", "office":
			sb.WriteString("\n**NNN/Commercial-specific analysis required:**\n")
			sb.WriteString("- Tenant schedule: evaluate credit quality, lease concentration risk (top tenant pct of total rent), nearest lease expiry.\n")
			sb.WriteString("- WALR: weighted average lease remaining — quantify re-leasing risk if < 5 years.\n")
			sb.WriteString("- Escalations: annual rent bumps vs. market inflation — does the income stream keep pace?\n")
			sb.WriteString("- Cap rate vs. tenant risk: NNN with speculative tenants should trade at higher cap than investment-grade.\n")
			if len(omParsed.TenantSchedule) > 0 {
				total := 0.0
				for _, t := range omParsed.TenantSchedule {
					if t.AnnualRent != nil {
						total += *t.AnnualRent
					}
				}
				if total > 0 {
					sb.WriteString(fmt.Sprintf("- Total tenant schedule annual rent: $%.0f\n", total))
				}
			}
		case "industrial", "warehouse":
			sb.WriteString("\n**Industrial/Warehouse-specific analysis required:**\n")
			sb.WriteString("- Physical spec: clear height, dock doors, column spacing relative to market standard for the tenant use case.\n")
			sb.WriteString("- Lease structure: gross vs. NNN — landlord exposure to opex.\n")
			sb.WriteString("- Location: last-mile vs. bulk distribution — cap rate benchmark differs by 50-150bps.\n")
		case "mixed_use":
			sb.WriteString("\n**Mixed-use-specific analysis required:**\n")
			sb.WriteString("- Residential vs. commercial income split — which component drives value?\n")
			sb.WriteString("- Commercial vacancy risk: retail turnover in mixed-use is higher than pure-play.\n")
			sb.WriteString("- Zoning restrictions on residential-to-commercial conversion (if value-add angle).\n")
		case "self_storage":
			sb.WriteString("\n**Self-storage-specific analysis required:**\n")
			sb.WriteString("- Occupancy trend: self-storage typically stabilizes at 85-92% — flag if stated occupancy is outside this range.\n")
			sb.WriteString("- Climate-controlled mix: premium units should trade at higher $/sqft.\n")
			sb.WriteString("- Revenue per available square foot vs. local market.\n")
		case "portfolio":
			sb.WriteString("\n**Portfolio-specific analysis required:**\n")
			sb.WriteString("- Portfolio discount: blended cap rate may embed poor performers — identify outliers in individual property NOI/cap rates.\n")
			sb.WriteString("- Geographic/type concentration risk.\n")
			sb.WriteString("- Are all properties available individually, or is portfolio acquisition required?\n")
		}
		if omParsed.BrokerMarket != nil {
			sb.WriteString("\n**Broker market context stated in OM:**\n")
			if omParsed.BrokerMarket.SubmarketName != nil {
				sb.WriteString(fmt.Sprintf("- Submarket: %s\n", *omParsed.BrokerMarket.SubmarketName))
			}
			if omParsed.BrokerMarket.VacancyRate != nil {
				sb.WriteString(fmt.Sprintf("- Broker-stated vacancy rate: %.1f%%\n", *omParsed.BrokerMarket.VacancyRate*100))
			}
			if omParsed.BrokerMarket.RentGrowthYoY != nil {
				sb.WriteString(fmt.Sprintf("- Broker-stated rent growth YoY: %.1f%%\n", *omParsed.BrokerMarket.RentGrowthYoY*100))
			}
			sb.WriteString("- Cross-check broker market claims against system market data in your analysis.\n")
		}

		sb.WriteString("\n")
	}

	sb.WriteString(`
## Memo Structure

Write a decision memo with these sections:

### Executive Summary
Brief verdict on the deal. Investment thesis or red flags.

### Property Analysis
For each property: location, physical condition assessment, market positioning.

### Financial Analysis
Review asking price vs. target price. Analyze rental income and cap rate.

### Broker vs. System Underwriting
Compare broker-stated figures against system conservative underwriting:
- Broker cap rate vs. system cap rate (note delta in basis points)
- Broker rent vs. system rent estimate (note variance)
- Broker NOI vs. system NOI (if available from OM)
- Implications of any gap (risk premium, negotiation leverage)

### OM Critique
(Include only if OM data is present for any property)
Assess the quality and reliability of the broker's offering memorandum:
- Are investment highlights substantiated by the financials?
- Are expense assumptions reasonable vs. market norms?
- Are market claims verifiable?
- Notable omissions or red flags in the OM presentation?

### Risk Assessment
Key risks: market, property, financing, execution. Rate each: Low / Medium / High.

### Recommendation
Clear verdict: Proceed / Pass / Negotiate. Specific conditions or next steps.

---
Write the memo in professional but direct prose. Use headers and bullet points. Be specific — cite actual numbers from the data above.`)

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
