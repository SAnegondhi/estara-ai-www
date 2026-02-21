package history

import (
	"context"
	"fmt"
	"time"

	"github.com/estara-ai/www/internal/db"
)

// Service handles market historical data fetching and analysis
type Service struct {
	store *db.Store
}

// NewService creates a new market history service
func NewService(store *db.Store) *Service {
	return &Service{
		store: store,
	}
}

// MarketHistoryData holds historical analysis for a market
type MarketHistoryData struct {
	MarketID            string    `json:"marketId"`
	HistoricalPeakDrop  float64   `json:"historicalPeakDrop"`  // Worst 12-month return in 20-year window
	LastUpdated         time.Time `json:"lastUpdated"`
}

// ComputeHistoricalPeakDrop calculates the worst 12-month return in a 20-year ZHVI window
// Returns value as percentage (e.g., -15.5 for 15.5% drop)
func ComputeHistoricalPeakDrop(zhviData []float64) float64 {
	if len(zhviData) < 12 {
		return 0.0 // Not enough data
	}

	worstReturn := 0.0 // Start at 0, we're looking for negative returns

	// Rolling 12-month window
	for i := 12; i < len(zhviData); i++ {
		currentValue := zhviData[i]
		priorValue := zhviData[i-12]

		if priorValue == 0 {
			continue // Skip if prior value is zero (avoid division by zero)
		}

		// Calculate 12-month return as percentage
		returnPct := ((currentValue - priorValue) / priorValue) * 100.0

		// Track worst (most negative) return
		if returnPct < worstReturn {
			worstReturn = returnPct
		}
	}

	return worstReturn
}

// FetchAndStoreHistoricalData fetches 20-year ZHVI data and computes HistoricalPeakDrop
// This is intended to run as a weekly background job
func (s *Service) FetchAndStoreHistoricalData(ctx context.Context, marketID string) error {
	// TODO: Phase 1 implementation will fetch from metro_time_series.zhvf_data
	// For now, this is a placeholder that would:
	// 1. Query metro_time_series table for marketID
	// 2. Extract ZHVI data from zhvf_data JSONB column
	// 3. Call ComputeHistoricalPeakDrop()
	// 4. Store result in market_history table via sqlc

	return fmt.Errorf("not implemented: Phase 1 will integrate with metro_time_series")
}

// GetMarketHistory retrieves historical data for a market
func (s *Service) GetMarketHistory(ctx context.Context, marketID string) (*MarketHistoryData, error) {
	// TODO: Phase 1 implementation will query market_history table via sqlc
	// For now, return placeholder
	return nil, fmt.Errorf("not implemented: Phase 1 will query market_history table")
}

// RefreshStaleData refreshes historical data for markets that haven't been updated in 7+ days
// This should be called by a weekly cron job
func (s *Service) RefreshStaleData(ctx context.Context) (int, error) {
	// TODO: Phase 1 implementation will:
	// 1. Query market_history for records with last_updated > 7 days ago
	// 2. For each stale record, call FetchAndStoreHistoricalData()
	// 3. Return count of refreshed markets

	return 0, fmt.Errorf("not implemented: Phase 1 will implement weekly refresh")
}
