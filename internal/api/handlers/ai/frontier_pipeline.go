package ai

// ADR-103: Pipeline → Frontier integration helpers.
// Maps pipeline_properties rows to investment.ScoredProperty so they can flow
// through the existing frontier BuildCohorts pipeline (pinned cohort semantics).

import (
	"context"
	"fmt"

	queries "github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// mapPipelinePropertiesToScored fetches all properties for the given pipeline deal
// and maps them to investment.ScoredProperty. Properties missing city, state, or a
// non-zero price are skipped. Returns (mapped, skippedCount).
//
// Mapping rules (from ADR-103):
//   - Price:         target_price preferred; fallback to asking_price; skip if both null/zero
//   - EstimatedRent: system_rent × 12 (annual); 0 if null (Frontier flags "no rent data")
//   - City/State:    required — skip property if either is null/empty
//   - Beds/Baths/Sqft/PropertyType: 0/"other" defaults when null
//   - OverallScore:  60 (neutral — Frontier ranking and BuildCohorts determine relative order)
func (h *Handler) mapPipelinePropertiesToScored(
	ctx context.Context,
	dealID uuid.UUID,
	userID string,
) ([]investment.ScoredProperty, int) {
	rows, err := h.store.Q().ListPipelineProperties(ctx, queries.ListPipelinePropertiesParams{
		PipelineDealID: dealID,
		UserID:         userID,
	})
	if err != nil {
		h.logger.Error("ListPipelineProperties for frontier failed",
			"pipeline_deal_id", dealID,
			"error", err,
		)
		return nil, 0
	}

	mapped := make([]investment.ScoredProperty, 0, len(rows))
	skipped := 0

	for _, row := range rows {
		city, state, ok := requireCityState(row)
		if !ok {
			h.logger.Warn("pipeline property skipped: missing city or state",
				"property_id", row.ID,
				"address", row.Address,
			)
			skipped++
			continue
		}

		price := resolvePrice(row)
		if price <= 0 {
			h.logger.Warn("pipeline property skipped: no valid price",
				"property_id", row.ID,
				"address", row.Address,
			)
			skipped++
			continue
		}

		estimatedRent := 0
		if sr, ok := numericToFloat(row.SystemRent); ok && sr > 0 {
			estimatedRent = int(sr * 12) // monthly → annual
		}

		beds := 0
		if b, ok := numericToFloat(row.Beds); ok {
			beds = int(b)
		}
		baths := 0.0
		if b, ok := numericToFloat(row.Baths); ok {
			baths = b
		}
		sqft := 0
		if row.Sqft.Valid {
			sqft = int(row.Sqft.Int32)
		}
		yearBuilt := 0
		if row.YearBuilt.Valid {
			yearBuilt = int(row.YearBuilt.Int32)
		}
		propType := "other"
		if row.PropertyType.Valid && row.PropertyType.String != "" {
			propType = row.PropertyType.String
		}
		zip := ""
		if row.Zip.Valid {
			zip = row.Zip.String
		}

		mapped = append(mapped, investment.ScoredProperty{
			Property: investment.Property{
				ID:            fmt.Sprintf("pipeline-%s", row.ID.String()),
				Address:       row.Address,
				City:          city,
				State:         state,
				ZipCode:       zip,
				Price:         price,
				Beds:          beds,
				Baths:         baths,
				Sqft:          sqft,
				EstimatedRent: estimatedRent,
				YearBuilt:     yearBuilt,
				PropertyType:  propType,
			},
			OverallScore: 60, // neutral score — frontier ranking determines relative order
		})
	}

	return mapped, skipped
}

// requireCityState returns (city, state, true) if both are present, else ("", "", false).
func requireCityState(row queries.PipelineProperty) (string, string, bool) {
	if !row.City.Valid || row.City.String == "" {
		return "", "", false
	}
	if !row.State.Valid || row.State.String == "" {
		return "", "", false
	}
	return row.City.String, row.State.String, true
}

// resolvePrice returns target_price if set and > 0, else asking_price; 0 if neither.
func resolvePrice(row queries.PipelineProperty) int {
	if tp, ok := numericToFloat(row.TargetPrice); ok && tp > 0 {
		return int(tp)
	}
	if ap, ok := numericToFloat(row.AskingPrice); ok && ap > 0 {
		return int(ap)
	}
	return 0
}

// numericToFloat converts a pgtype.Numeric to float64. Returns (0, false) if invalid.
func numericToFloat(n pgtype.Numeric) (float64, bool) {
	if !n.Valid {
		return 0, false
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0, false
	}
	return f.Float64, true
}
