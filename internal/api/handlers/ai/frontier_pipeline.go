package ai

// ADR-103: Pipeline → Frontier integration helpers.
// Maps pipeline_properties rows to investment.ScoredProperty so they can flow
// through the existing frontier BuildCohorts pipeline (pinned cohort semantics).
//
// ADR-113 (type-aware peer discovery): discoverPipelinePeers finds comparable
// CRE/residential properties so Configs 1 and 2 can populate when a pipeline deal
// is analysed. Commercial types route to Claude (LoopNet/Crexi); residential types
// route through the normal HasData/BrightData priority chain.

import (
	"context"
	"fmt"
	"sort"

	queries "github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/property/providers"
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

// mapPipelineTypeToProviderType converts a pipeline property type string to the
// corresponding providers.PropertyType for comparable peer search.
// Returns ("", false) for types where peer discovery is not applicable.
func mapPipelineTypeToProviderType(pipelineType string) (providers.PropertyType, bool) {
	switch pipelineType {
	case "sfh":
		return providers.PropertyTypeSingleFamily, true
	case "condo":
		return providers.PropertyTypeCondo, true
	case "townhouse":
		return providers.PropertyTypeTownhouse, true
	case "multifamily":
		return providers.PropertyTypeMultiFamilyCRE, true
	case "nnn":
		return providers.PropertyTypeNNN, true
	case "retail":
		return providers.PropertyTypeRetail, true
	case "office", "commercial":
		return providers.PropertyTypeOffice, true
	case "industrial":
		return providers.PropertyTypeIndustrial, true
	case "warehouse":
		return providers.PropertyTypeWarehouse, true
	case "self_storage":
		return providers.PropertyTypeSelfStorage, true
	case "mixed_use":
		return providers.PropertyTypeMixedUse, true
	case "student_housing":
		return providers.PropertyTypeStudentHousing, true
	case "senior_housing":
		return providers.PropertyTypeSeniorHousing, true
	default:
		return "", false // portfolio, other → skip peer discovery
	}
}

// peerGroupKey uniquely identifies a peer discovery group by city, state, and property type.
type peerGroupKey struct {
	city         string
	state        string
	propertyType string
}

// discoverPipelinePeers finds comparable properties for Frontier Configs 1/2.
// Groups pinned properties by (city, state, propertyType), searches for peers at
// ±40% of median group price, and returns non-pinned ScoredProperty slices.
// Peers are searched using the type-aware property finder (CRE → Claude/LoopNet,
// residential → HasData/BrightData priority chain).
func (h *Handler) discoverPipelinePeers(
	ctx context.Context,
	mapped []investment.ScoredProperty,
) []investment.ScoredProperty {
	if h.propertyFinder == nil {
		return nil
	}

	// Build a set of pinned IDs to avoid collisions.
	pinnedIDs := make(map[string]bool, len(mapped))
	for _, sp := range mapped {
		pinnedIDs[sp.Property.ID] = true
	}

	// Group by (city, state, propertyType) and collect prices.
	groups := make(map[peerGroupKey][]int)
	for _, sp := range mapped {
		key := peerGroupKey{
			city:         sp.Property.City,
			state:        sp.Property.State,
			propertyType: sp.Property.PropertyType,
		}
		groups[key] = append(groups[key], sp.Property.Price)
	}

	var peers []investment.ScoredProperty

	for key, prices := range groups {
		providerType, ok := mapPipelineTypeToProviderType(key.propertyType)
		if !ok {
			continue
		}

		// Compute median price for the group.
		sorted := make([]int, len(prices))
		copy(sorted, prices)
		sort.Ints(sorted)
		median := sorted[len(sorted)/2]

		minPrice := int(float64(median) * 0.6)
		maxPrice := int(float64(median) * 1.4)

		resp, err := h.propertyFinder.Search(ctx, providers.SearchParams{
			City:         key.city,
			State:        key.state,
			PropertyType: providerType,
			MinPrice:     minPrice,
			MaxPrice:     maxPrice,
			Limit:        20,
		})
		if err != nil {
			h.logger.Warn("peer discovery search failed",
				"city", key.city,
				"state", key.state,
				"type", key.propertyType,
				"error", err,
			)
			continue
		}

		for _, prop := range resp.Properties {
			if pinnedIDs[prop.ID] {
				continue
			}
			// Resolve income: for CRE types, NOI is EstimatedRent (annual);
			// for residential, use EstimatedRent directly.
			estimatedRent := prop.EstimatedRent
			if providers.IsCommercialPropertyType(providerType) && prop.NOI > 0 && estimatedRent == 0 {
				estimatedRent = prop.NOI
			}

			peers = append(peers, investment.ScoredProperty{
				Property: investment.Property{
					ID:            prop.ID,
					Address:       prop.Address,
					City:          prop.City,
					State:         prop.State,
					ZipCode:       prop.ZipCode,
					Price:         prop.Price,
					Beds:          prop.Beds,
					Baths:         prop.Baths,
					Sqft:          prop.Sqft,
					EstimatedRent: estimatedRent,
					YearBuilt:     prop.YearBuilt,
					PropertyType:  string(prop.PropertyType),
				},
				OverallScore: 50, // below pinned deal score (60) — won't dominate Config 0
			})

			if len(peers) >= 30 {
				return peers
			}
		}
	}

	return peers
}
