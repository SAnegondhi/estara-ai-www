package investment

import (
	"sort"
	"time"
)

// PropertyFilters defines quality-gate criteria for auto-discovered properties.
// All multiplier fields express ratios against per-market computed benchmarks
// derived from the discovered pool itself.
type PropertyFilters struct {
	// MaxCapRateMultiplier rejects properties whose estimated cap rate exceeds
	// this multiple of the per-location average. Default 1.5 (150%).
	// Set to 0 to disable.
	MaxCapRateMultiplier float64 `json:"maxCapRateMultiplier"`

	// MinCapRateMultiplier rejects properties whose estimated cap rate is below
	// this multiple of the per-location average. Default 0.75 (75%).
	// Set to 0 to disable.
	MinCapRateMultiplier float64 `json:"minCapRateMultiplier"`

	// MinPriceMultiplier rejects properties priced below this fraction of the
	// per-location median price. Default 0.5 (50%).
	// Set to 0 to disable.
	MinPriceMultiplier float64 `json:"minPriceMultiplier"`

	// MaxPropertyAgeYears rejects properties built more than this many years ago.
	// Default 20. Set to 0 to disable.
	MaxPropertyAgeYears int `json:"maxPropertyAgeYears"`
}

// DefaultPropertyFilters returns the default quality-gate criteria.
func DefaultPropertyFilters() PropertyFilters {
	return PropertyFilters{
		MaxCapRateMultiplier: 1.5,
		MinCapRateMultiplier: 0.75,
		MinPriceMultiplier:   0.5,
		MaxPropertyAgeYears:  20,
	}
}

// MarketFilterBenchmark holds per-location benchmarks sourced from the market data aggregator.
// When available, these replace pool-derived benchmarks so cap rate gates compare against
// real market data rather than the (potentially small) discovered pool.
type MarketFilterBenchmark struct {
	MedianPrice float64 // Market median home price (from city_market_cache)
	// MedianRent is the ZORI value — monthly rent for a typical ~1,200 sqft unit (NOT per sqft).
	// Use FMRByBeds or (MedianRent/1200*sqft) for size-adjusted estimates.
	MedianRent float64 // Market median monthly rent from ZORI (city_market_cache)
	CapRate    float64 // Pre-computed market cap rate: NOI/price, 40% expense ratio
	HasData    bool    // false = market data unavailable, fall back to pool-derived
	// FMRByBeds holds HUD Fair Market Rent by bedroom count [0BR, 1BR, 2BR, 3BR, 4BR].
	// Non-zero entries are preferred over ZORI for per-property rent estimation.
	FMRByBeds [5]float64 // HUD FMR indexed by bedrooms (0=studio, 4=4BR)
}

// DefaultPropertyFiltersForStrategy returns quality-gate criteria tuned to the investor's
// strategy and risk tolerance. Call this instead of DefaultPropertyFilters() when strategy
// context is available. The returned filters can be further customised by the caller.
//
// Strategy baselines:
//   - CashFlow:     tighter min cap rate (≥90% of market); 15-year age cap
//   - Appreciation: looser min cap rate (≥50% of market); 40-year age cap
//   - RiskAdjusted: balanced-defensive (≥80% cap rate; 18-year age cap)
//   - Balanced:     defaults (unchanged from DefaultPropertyFilters)
//
// Risk overlay (applied after strategy baseline):
//   - Conservative: tightens age cap to 15 yr max; raises min cap rate to ≥85%
//   - Aggressive:   raises age cap to 35 yr min; widens max cap rate to 2×
func DefaultPropertyFiltersForStrategy(strategy InvestmentStrategy, risk RiskTolerance) PropertyFilters {
	base := DefaultPropertyFilters()

	switch strategy {
	case StrategyCashFlow:
		base.MinCapRateMultiplier = 0.90 // must be ≥90% of market cap rate
		base.MaxPropertyAgeYears = 15
	case StrategyAppreciation:
		base.MinCapRateMultiplier = 0.50 // below-market yield acceptable for growth plays
		base.MaxPropertyAgeYears = 40
	case StrategyRiskAdjusted:
		base.MinCapRateMultiplier = 0.80
		base.MaxPropertyAgeYears = 18
	// StrategyBalanced: keep DefaultPropertyFilters values unchanged
	}

	switch risk {
	case RiskConservative:
		if base.MaxPropertyAgeYears > 15 {
			base.MaxPropertyAgeYears = 15
		}
		if base.MinCapRateMultiplier < 0.85 {
			base.MinCapRateMultiplier = 0.85
		}
	case RiskAggressive:
		if base.MaxPropertyAgeYears < 35 {
			base.MaxPropertyAgeYears = 35
		}
		base.MaxCapRateMultiplier = 2.0 // allow outlier high-yield listings
	}

	return base
}

// ApplyPropertyFiltersWithBenchmarks is the preferred filter function when market data
// is available. It uses authoritative per-location benchmarks (from aggregator L0/L1/L2
// cache) so cap rate gates fire even when providers do not return EstimatedRent.
//
// When a location has no market benchmark (HasData=false), pool-derived statistics are
// computed as a fallback (same behaviour as ApplyPropertyFilters).
func ApplyPropertyFiltersWithBenchmarks(props []Property, f PropertyFilters, benchmarks map[string]MarketFilterBenchmark) (filtered []Property, rejectedCount int) {
	if len(props) == 0 {
		return props, 0
	}

	// ── Pool-derived fallback stats (used when market benchmark unavailable) ──
	type locData struct {
		prices   []int
		capRates []float64
	}
	byLoc := make(map[string]*locData, 8)
	for _, p := range props {
		key := p.City + "|" + p.State
		if byLoc[key] == nil {
			byLoc[key] = &locData{}
		}
		byLoc[key].prices = append(byLoc[key].prices, p.Price)
		if p.EstimatedRent > 0 && p.Price > 0 {
			byLoc[key].capRates = append(byLoc[key].capRates, CapRatePct(p.EstimatedRent, p.Price, p.YearBuilt))
		}
	}

	type poolBench struct {
		medianPrice float64
		avgCapRate  float64
		hasCapRate  bool
	}
	poolBenches := make(map[string]poolBench, len(byLoc))
	for key, d := range byLoc {
		sorted := make([]int, len(d.prices))
		copy(sorted, d.prices)
		sort.Ints(sorted)
		n := len(sorted)
		var med float64
		if n > 0 {
			if n%2 == 0 {
				med = float64(sorted[n/2-1]+sorted[n/2]) / 2
			} else {
				med = float64(sorted[n/2])
			}
		}
		var avgCR float64
		if len(d.capRates) > 0 {
			sum := 0.0
			for _, cr := range d.capRates {
				sum += cr
			}
			avgCR = sum / float64(len(d.capRates))
		}
		poolBenches[key] = poolBench{medianPrice: med, avgCapRate: avgCR, hasCapRate: len(d.capRates) > 0}
	}

	currentYear := time.Now().Year()
	result := make([]Property, 0, len(props))

	for _, p := range props {
		locKey := p.City + "|" + p.State
		mkt := benchmarks[locKey]
		pool := poolBenches[locKey]

		// ── Price floor ──────────────────────────────────────────────
		// Prefer market median; fall back to pool median.
		refMedianPrice := pool.medianPrice
		if mkt.HasData && mkt.MedianPrice > 0 {
			refMedianPrice = mkt.MedianPrice
		}
		if f.MinPriceMultiplier > 0 && refMedianPrice > 0 {
			if float64(p.Price) < f.MinPriceMultiplier*refMedianPrice {
				continue
			}
		}

		// ── Cap rate gates ───────────────────────────────────────────
		// Prefer market cap rate as reference; fall back to pool average.
		// For the property's own cap rate, use EstimatedRent if available;
		// otherwise use market MedianRent so every property is evaluated.
		if f.MaxCapRateMultiplier > 0 || f.MinCapRateMultiplier > 0 {
			refCapRate := 0.0
			hasRef := false
			if mkt.HasData && mkt.CapRate > 0 {
				refCapRate = mkt.CapRate
				hasRef = true
			} else if pool.hasCapRate {
				refCapRate = pool.avgCapRate
				hasRef = true
			}

			if hasRef && p.Price > 0 {
				// Determine property's effective monthly rent for cap rate estimation.
				// Priority order:
				//   1. Provider-returned EstimatedRent (most accurate)
				//   2. HUD FMR for the property's bedroom count (bedroom-adjusted)
				//   3. ZORI / 1200 * sqft (size-adjusted; ZORI is for ~1,200 sqft typical unit)
				//   4. Flat ZORI (last resort when sqft unknown)
				effectiveRent := 0
				if p.EstimatedRent > 0 {
					effectiveRent = p.EstimatedRent
				} else if mkt.HasData {
					beds := max(0, min(p.Beds, 4))
					if mkt.FMRByBeds[beds] > 0 {
						// HUD FMR — bedroom-adjusted, most accurate when available
						effectiveRent = int(mkt.FMRByBeds[beds])
					} else if mkt.MedianRent > 0 && p.Sqft > 0 {
						// ZORI size-adjusted: ZORI/1200 * sqft (slight underestimate intentional)
						effectiveRent = int(mkt.MedianRent / 1200 * float64(p.Sqft))
					} else if mkt.MedianRent > 0 {
						// Flat ZORI — last resort, only when sqft unknown
						effectiveRent = int(mkt.MedianRent)
					}
				}

				if effectiveRent > 0 {
					propCapRate := CapRatePct(effectiveRent, p.Price, p.YearBuilt)
					if f.MaxCapRateMultiplier > 0 && propCapRate > f.MaxCapRateMultiplier*refCapRate {
						continue
					}
					if f.MinCapRateMultiplier > 0 && propCapRate < f.MinCapRateMultiplier*refCapRate {
						continue
					}
				}
			}
		}

		// ── Age gate ─────────────────────────────────────────────────
		if f.MaxPropertyAgeYears > 0 && p.YearBuilt > 0 {
			if currentYear-p.YearBuilt > f.MaxPropertyAgeYears {
				continue
			}
		}

		result = append(result, p)
	}

	return result, len(props) - len(result)
}

// ApplyPropertyFilters removes properties that fail quality gates.
//
// Per-location market benchmarks (median price, average estimated cap rate) are
// derived from the discovered pool itself so no extra DB queries are required.
// Cap rate is estimated as annualRent/price when EstimatedRent > 0; properties
// lacking rent data are exempt from cap-rate gates but still subject to price
// and age gates.
//
// Prefer ApplyPropertyFiltersWithBenchmarks when market data is available.
// Returns all properties when props is empty or all gates are disabled (zero).
func ApplyPropertyFilters(props []Property, f PropertyFilters) (filtered []Property, rejectedCount int) {
	if len(props) == 0 {
		return props, 0
	}

	// ── Per-location stats ────────────────────────────────────────────────
	type locData struct {
		prices   []int
		capRates []float64
	}
	byLoc := make(map[string]*locData, 8)
	for _, p := range props {
		key := p.City + "|" + p.State
		if byLoc[key] == nil {
			byLoc[key] = &locData{}
		}
		byLoc[key].prices = append(byLoc[key].prices, p.Price)
		if p.EstimatedRent > 0 && p.Price > 0 {
			byLoc[key].capRates = append(byLoc[key].capRates, CapRatePct(p.EstimatedRent, p.Price, p.YearBuilt))
		}
	}

	type benchmarks struct {
		medianPrice float64
		avgCapRate  float64
		hasCapRate  bool
	}
	bench := make(map[string]benchmarks, len(byLoc))
	for key, d := range byLoc {
		// Median price
		sorted := make([]int, len(d.prices))
		copy(sorted, d.prices)
		sort.Ints(sorted)
		n := len(sorted)
		var med float64
		if n > 0 {
			if n%2 == 0 {
				med = float64(sorted[n/2-1]+sorted[n/2]) / 2
			} else {
				med = float64(sorted[n/2])
			}
		}

		// Average cap rate
		var avgCR float64
		if len(d.capRates) > 0 {
			sum := 0.0
			for _, cr := range d.capRates {
				sum += cr
			}
			avgCR = sum / float64(len(d.capRates))
		}

		bench[key] = benchmarks{
			medianPrice: med,
			avgCapRate:  avgCR,
			hasCapRate:  len(d.capRates) > 0,
		}
	}

	// ── Filter ────────────────────────────────────────────────────────────
	currentYear := time.Now().Year()
	result := make([]Property, 0, len(props))

	for _, p := range props {
		bm := bench[p.City+"|"+p.State]

		// Price floor
		if f.MinPriceMultiplier > 0 && bm.medianPrice > 0 {
			if float64(p.Price) < f.MinPriceMultiplier*bm.medianPrice {
				continue
			}
		}

		// Cap rate gates (only when both property and market have cap rate data)
		if bm.hasCapRate && p.EstimatedRent > 0 && p.Price > 0 {
			cr := CapRatePct(p.EstimatedRent, p.Price, p.YearBuilt)
			if f.MaxCapRateMultiplier > 0 && cr > f.MaxCapRateMultiplier*bm.avgCapRate {
				continue
			}
			if f.MinCapRateMultiplier > 0 && cr < f.MinCapRateMultiplier*bm.avgCapRate {
				continue
			}
		}

		// Age gate
		if f.MaxPropertyAgeYears > 0 && p.YearBuilt > 0 {
			if currentYear-p.YearBuilt > f.MaxPropertyAgeYears {
				continue
			}
		}

		result = append(result, p)
	}

	return result, len(props) - len(result)
}
