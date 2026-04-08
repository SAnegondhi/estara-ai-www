// Package unified provides a standard market data service that orchestrates
// the market aggregator (Zillow/FRED/DB) with a Haiku AI fallback for cities
// not covered by structured data sources. All features (pipeline memo,
// evaluation, frontier, discovery) should use this as the single entry point
// for location market data.
//
// Caching: L0 24h in-memory (per-process). The aggregator itself provides
// L0/L1/L2 caching (memory→Redis→Postgres, 7d TTL) so most calls are sub-ms.
package unified

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/estimation"
)

const (
	memCacheTTL    = 24 * time.Hour
	memCacheNilTTL = 1 * time.Hour // shorter TTL for negative results
)

// LocationMarketData is the normalized, source-agnostic result from any market data source.
// All callers receive the same type regardless of whether data came from the DB or Haiku.
type LocationMarketData struct {
	City                 string
	State                string
	MedianHomePrice      int
	MedianRent           int
	CapRate              float64
	MortgageRate30       float64
	VacancyRate          float64
	YearOverYearPct      float64
	RentYearOverYear     float64
	UnemploymentRate     float64
	EmploymentGrowthRate float64
	PopulationGrowthRate float64
	MedianDaysOnMarket   *int
	Source               string  // "database" | "ai_estimated" | "haiku_estimated"
	IsEstimated          bool
	Confidence           float64
	DataDate             time.Time
}

// memEntry holds an in-memory cache entry. data may be nil (cached negative result).
type memEntry struct {
	data      *LocationMarketData
	expiresAt time.Time
}

// Service orchestrates market data from the aggregator with a Haiku fallback.
// Construct via New(); all methods are safe for concurrent use.
type Service struct {
	aggregator *aggregator.Aggregator
	estimator  *estimation.AIEstimator // backed by a Haiku-model client
	mem        sync.Map
	logger     *slog.Logger
}

// New creates a unified market service. apiKey is the Anthropic key used to
// instantiate a dedicated Haiku client for the AI fallback. Pass "" to disable
// the fallback (aggregator-only mode).
func New(agg *aggregator.Aggregator, apiKey string) *Service {
	s := &Service{
		aggregator: agg,
		logger:     slog.Default().With("component", "unified_market"),
	}
	if apiKey != "" {
		haikuClient := anthropic.NewClient(anthropic.ClientConfig{
			APIKey:    apiKey,
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 1024,
		})
		s.estimator = estimation.NewAIEstimator(haikuClient)
		s.logger.Info("unified market service: Haiku fallback enabled")
	} else {
		s.logger.Warn("unified market service: Haiku fallback disabled (no API key)")
	}
	return s
}

// Get returns market data for city/state. Always non-fatal — returns nil when
// no data is available from any source. Results are cached in memory for 24h
// (1h for negative results). The underlying aggregator has its own 3-tier
// cache (memory→Redis→Postgres, 7d TTL).
func (s *Service) Get(ctx context.Context, city, state string) *LocationMarketData {
	if strings.TrimSpace(city) == "" || strings.TrimSpace(state) == "" {
		return nil
	}

	key := cacheKey(city, state)

	// L0: memory cache
	if v, ok := s.mem.Load(key); ok {
		e := v.(memEntry)
		if time.Now().Before(e.expiresAt) {
			return e.data // may be nil (cached negative)
		}
	}

	data := s.fetch(ctx, city, state)

	ttl := memCacheTTL
	if data == nil {
		ttl = memCacheNilTTL
	}
	s.mem.Store(key, memEntry{data: data, expiresAt: time.Now().Add(ttl)})

	return data
}

// fetch executes the two-step lookup: aggregator first, Haiku fallback.
func (s *Service) fetch(ctx context.Context, city, state string) *LocationMarketData {
	// Step 1: Market aggregator (Zillow/FRED/DB — 3-tier cached, 7d TTL)
	if s.aggregator != nil {
		md, err := s.aggregator.GetMarketData(ctx, city, state)
		if err == nil && md != nil && md.MedianRent > 0 {
			source := "database"
			if md.IsAIEstimated {
				source = "ai_estimated"
			}
			s.logger.Debug("unified market: aggregator hit", "city", city, "state", state, "source", source)
			return fromAggregator(md, source)
		}
		if err != nil {
			s.logger.Debug("unified market: aggregator miss", "city", city, "state", state, "err", err)
		}
	}

	// Step 2: Haiku fallback — uses AI training data for cities not in Zillow
	if s.estimator != nil && s.estimator.IsConfigured() {
		s.logger.Info("unified market: Haiku fallback", "city", city, "state", state)
		est, err := s.estimator.EstimateMarketData(ctx, city, state)
		if err == nil && est != nil {
			s.logger.Debug("unified market: Haiku estimation complete", "city", city, "state", state, "confidence", est.Confidence)
			return fromEstimated(est)
		}
		if err != nil {
			s.logger.Warn("unified market: Haiku fallback failed", "city", city, "state", state, "err", err)
		}
	}

	s.logger.Debug("unified market: no data available", "city", city, "state", state)
	return nil
}

// IsConfigured returns true if the service has at least one data source.
func (s *Service) IsConfigured() bool {
	return s.aggregator != nil || (s.estimator != nil && s.estimator.IsConfigured())
}

// cacheKey returns a normalized key for a city/state pair.
func cacheKey(city, state string) string {
	return fmt.Sprintf("unified_mkt:%s_%s",
		strings.ToLower(strings.TrimSpace(city)),
		strings.ToLower(strings.TrimSpace(state)))
}

// fromAggregator converts an aggregator.MarketData to LocationMarketData.
func fromAggregator(md *aggregator.MarketData, source string) *LocationMarketData {
	return &LocationMarketData{
		City:                 md.City,
		State:                md.State,
		MedianHomePrice:      md.MedianHomePrice,
		MedianRent:           md.MedianRent,
		CapRate:              md.CapRate,
		MortgageRate30:       md.MortgageRate30,
		VacancyRate:          md.VacancyRate,
		YearOverYearPct:      md.YearOverYearPct,
		RentYearOverYear:     md.RentYearOverYear,
		UnemploymentRate:     md.UnemploymentRate,
		EmploymentGrowthRate: md.EmploymentGrowthRate,
		PopulationGrowthRate: md.PopulationGrowthRate,
		MedianDaysOnMarket:   md.MedianDaysOnMarket,
		Source:               source,
		IsEstimated:          source != "database",
		Confidence:           md.Confidence,
		DataDate:             md.DataDate,
	}
}

// fromEstimated converts an estimation.AIEstimatedData to LocationMarketData.
func fromEstimated(est *estimation.AIEstimatedData) *LocationMarketData {
	return &LocationMarketData{
		City:            est.City,
		State:           est.State,
		MedianHomePrice: est.MedianHomePrice,
		MedianRent:      est.MedianRent,
		CapRate:         est.CapRate,
		YearOverYearPct: est.YearOverYearPct,
		Source:          "haiku_estimated",
		IsEstimated:     true,
		Confidence:      est.Confidence,
		DataDate:        est.GeneratedAt,
	}
}

// ForPrompt formats the market data as a markdown block for injection into any
// AI prompt. Returns "" for nil receiver. The block is self-contained and can
// be inserted into any section of a property analysis prompt.
func (d *LocationMarketData) ForPrompt() string {
	if d == nil {
		return ""
	}

	var sb strings.Builder

	sourceLabel := "Zillow/FRED database"
	switch d.Source {
	case "ai_estimated":
		sourceLabel = "AI-estimated (city not in Zillow; directional only)"
	case "haiku_estimated":
		sourceLabel = "AI-estimated (training data; verify independently)"
	}

	sb.WriteString(fmt.Sprintf("\n**Independent Market Context** (source: %s", sourceLabel))
	if !d.DataDate.IsZero() {
		sb.WriteString(fmt.Sprintf(", %s", d.DataDate.Format("Jan 2006")))
	}
	sb.WriteString(")\n")

	if d.MedianRent > 0 {
		sb.WriteString(fmt.Sprintf("- Market median rent: $%d/mo\n", d.MedianRent))
	}
	if d.MedianHomePrice > 0 {
		sb.WriteString(fmt.Sprintf("- Median home price: $%s\n", fmtInt(d.MedianHomePrice)))
	}
	if d.CapRate > 0 {
		sb.WriteString(fmt.Sprintf("- Market cap rate: %.1f%%\n", d.CapRate))
	}
	if d.VacancyRate > 0 {
		sb.WriteString(fmt.Sprintf("- Rental vacancy rate: %.1f%%\n", d.VacancyRate))
	}
	if d.YearOverYearPct != 0 {
		sb.WriteString(fmt.Sprintf("- Home price appreciation YoY: %+.1f%%\n", d.YearOverYearPct))
	}
	if d.RentYearOverYear != 0 {
		sb.WriteString(fmt.Sprintf("- Rent growth YoY: %+.1f%%\n", d.RentYearOverYear))
	}
	if d.MortgageRate30 > 0 {
		sb.WriteString(fmt.Sprintf("- 30yr mortgage rate: %.2f%%\n", d.MortgageRate30))
	}
	if d.UnemploymentRate > 0 {
		sb.WriteString(fmt.Sprintf("- Unemployment: %.1f%%\n", d.UnemploymentRate))
	}
	if d.EmploymentGrowthRate != 0 {
		sb.WriteString(fmt.Sprintf("- Employment growth YoY: %+.1f%%\n", d.EmploymentGrowthRate))
	}
	if d.MedianDaysOnMarket != nil {
		sb.WriteString(fmt.Sprintf("- Median days on market: %d\n", *d.MedianDaysOnMarket))
	}
	if d.IsEstimated {
		sb.WriteString(fmt.Sprintf("- Data confidence: %.0f%% (AI-estimated)\n", d.Confidence*100))
	}
	sb.WriteString("*Compare broker claims against these independent figures.*\n")

	return sb.String()
}

// fmtInt formats a large integer with K/M suffix.
func fmtInt(v int) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.0fK", float64(v)/1_000)
	}
	return fmt.Sprintf("%d", v)
}
