// Package refresh provides selective refresh guidance for market analysis data sources
package refresh

import (
	"context"
	"log/slog"

	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/timeseries"
	"github.com/jackc/pgx/v5/pgtype"
)

// SelectiveRefresher provides guidance on which data sources need refresh
// Note: Actual data fetching uses existing services (aggregator, economics, metro)
// which are fast and cached. The "selective" part is knowing which sources are stale.
type SelectiveRefresher struct {
	market    *aggregator.Aggregator
	economics *economics.Aggregator
	metro     *timeseries.MetroReader
	logger    *slog.Logger
}

// NewSelectiveRefresher creates a new selective refresher
func NewSelectiveRefresher(
	market *aggregator.Aggregator,
	econ *economics.Aggregator,
	metro *timeseries.MetroReader,
) *SelectiveRefresher {
	return &SelectiveRefresher{
		market:    market,
		economics: econ,
		metro:     metro,
		logger:    slog.Default().With("component", "selective_refresher"),
	}
}

// RefreshDataPayload fetches fresh data payload (all sources)
// The staleSources parameter is informational - helps with logging and versioning
func (sr *SelectiveRefresher) RefreshDataPayload(
	ctx context.Context,
	city, state string,
	staleSources []StaleSource,
) (*prompts.DataPayload, map[string]string, error) {
	sr.logger.Info("refreshing data payload",
		"city", city,
		"state", state,
		"stale_sources", len(staleSources),
	)

	// Build fresh data payload using existing services
	builder := prompts.NewDataPayloadBuilder(sr.market, sr.economics, sr.metro)
	payload, err := builder.BuildPayload(ctx, city, state)
	if err != nil {
		return nil, nil, err
	}

	// Build version map for updated sources
	// In practice, we always refresh ALL sources (simpler, faster, cached)
	// But we track which versions were updated for database records
	updatedVersions := make(map[string]string)
	for _, stale := range staleSources {
		updatedVersions[stale.Source+"_version"] = stale.Current
	}

	sr.logger.Info("data payload refreshed",
		"city", city,
		"state", state,
		"updated_versions", len(updatedVersions),
	)

	return payload, updatedVersions, nil
}

// BuildVersionParams creates sqlc params for updating report versions
func (sr *SelectiveRefresher) BuildVersionParams(updatedVersions map[string]string) map[string]pgtype.Text {
	params := make(map[string]pgtype.Text)

	for key, value := range updatedVersions {
		params[key] = pgtype.Text{String: value, Valid: true}
	}

	return params
}
