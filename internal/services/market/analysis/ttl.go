package analysis

import (
	"time"
)

// DataSourceFreshness tracks when data was last updated and how often it updates
type DataSourceFreshness struct {
	Name         string
	LastUpdated  time.Time
	UpdateFreq   time.Duration
	IsAvailable  bool
}

// CalculateCacheTTL determines cache TTL based on data source freshness (ADR-087)
//
// The most volatile (most frequently updated) data source determines the cache TTL.
// If all sources are fresh, we use the longest safe period. If any source is stale,
// we shorten the TTL to encourage refresh when new data becomes available.
//
// Data Source Update Frequencies:
//   - ZHVI/ZORI (Zillow): Monthly (mid-month) → 30 days
//   - Redfin Market Data: Weekly → 7 days
//   - FRED Economic Data: Monthly → 30 days
//   - Census/BLS: Quarterly/Annual → 90 days
//   - AI Market Context (web search): Real-time → 24 hours
//
// Returns: TTL duration between 1 hour (minimum) and 30 days (maximum)
func CalculateCacheTTL(sources []DataSourceFreshness) time.Duration {
	if len(sources) == 0 {
		// No data sources tracked → default 7 days
		return 7 * 24 * time.Hour
	}

	// Find the shortest remaining TTL across all data sources
	minTTL := 30 * 24 * time.Hour // Start with maximum (30 days)

	for _, src := range sources {
		if !src.IsAvailable {
			continue // Skip unavailable sources
		}

		// How long ago was this data updated?
		age := time.Since(src.LastUpdated)

		// How much time until next expected update?
		remaining := src.UpdateFreq - age

		// If data is already stale (remaining < 0), use a short TTL
		// to encourage refresh when new data becomes available
		if remaining < 0 {
			remaining = 1 * time.Hour
		}

		// Track the shortest TTL
		if remaining < minTTL {
			minTTL = remaining
		}
	}

	// Apply bounds: minimum 1 hour, maximum 30 days
	if minTTL < 1*time.Hour {
		return 1 * time.Hour
	}
	if minTTL > 30*24*time.Hour {
		return 30 * 24 * time.Hour
	}

	return minTTL
}

// GetDefaultCacheTTL returns the default TTL for market analysis reports (ADR-087)
//
// Used when data source freshness information is not available.
// Based on the most volatile component: AI Context (web search) updates real-time,
// so we default to 24 hours to balance freshness and cost.
func GetDefaultCacheTTL() time.Duration {
	return 24 * time.Hour
}

// GetStandardDataSources returns expected update frequencies for standard data sources
//
// Used as a fallback when actual last-updated timestamps are not available.
// Assumes data was updated "now" and applies standard update frequencies.
func GetStandardDataSources() []DataSourceFreshness {
	now := time.Now()
	return []DataSourceFreshness{
		{
			Name:         "zhvi",
			LastUpdated:  now,
			UpdateFreq:   30 * 24 * time.Hour, // Monthly
			IsAvailable:  true,
		},
		{
			Name:         "redfin",
			LastUpdated:  now,
			UpdateFreq:   7 * 24 * time.Hour, // Weekly
			IsAvailable:  true,
		},
		{
			Name:         "fred",
			LastUpdated:  now,
			UpdateFreq:   30 * 24 * time.Hour, // Monthly
			IsAvailable:  true,
		},
		{
			Name:         "context",
			LastUpdated:  now,
			UpdateFreq:   24 * time.Hour, // Daily (AI context from web)
			IsAvailable:  true,
		},
	}
}

// EstimateTTLFromReportAge estimates a reasonable TTL based on report age
//
// For existing reports where we don't have data source metadata:
//   - Fresh reports (< 24h old): 24 hours
//   - Recent reports (< 7d old): 7 days
//   - Older reports (>= 7d): 1 hour (encourage refresh)
func EstimateTTLFromReportAge(reportGeneratedAt time.Time) time.Duration {
	age := time.Since(reportGeneratedAt)

	if age < 24*time.Hour {
		return 24 * time.Hour
	} else if age < 7*24*time.Hour {
		return 7 * 24 * time.Hour
	} else {
		// Old report - short TTL to encourage refresh
		return 1 * time.Hour
	}
}
