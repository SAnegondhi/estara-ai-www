package optimization

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/services/investment"
	"github.com/estara-ai/www/internal/services/market/timeseries"
)

// CorrelationAnalyzer computes pairwise Pearson correlations between markets
// using ZHVI (home value) and ZORI (rent) time series data.
// Ported from www_v1's marketCorrelationAnalyzer.
type CorrelationAnalyzer struct {
	metro  *timeseries.MetroReader
	logger *slog.Logger
}

// NewCorrelationAnalyzer creates a new correlation analyzer
func NewCorrelationAnalyzer(metro *timeseries.MetroReader) *CorrelationAnalyzer {
	return &CorrelationAnalyzer{
		metro:  metro,
		logger: slog.Default().With("component", "correlation_analyzer"),
	}
}

// CorrelationResult holds the full output of correlation analysis
type CorrelationResult struct {
	Correlations    []investment.MarketCorrelation `json:"correlations"`
	Opportunities   []DiversificationOpportunity   `json:"opportunities"`
	Score           float64                        `json:"score"`
	DataQualityNote string                         `json:"dataQualityNote,omitempty"`
}

// DiversificationOpportunity identifies a low-correlation market pair
type DiversificationOpportunity struct {
	Market1    string  `json:"market1"`
	Market2    string  `json:"market2"`
	Correlation float64 `json:"correlation"`
	Score      float64 `json:"score"` // 0-100, higher = better diversification
	Reasoning  string  `json:"reasoning"`
}

// timeSeriesPoint is a date-aligned value for correlation calculation
type timeSeriesPoint struct {
	date  string
	value float64
}

// locationTimeSeries holds ZHVI and ZORI series for one location
type locationTimeSeries struct {
	location string
	zhvi     []timeSeriesPoint
	zori     []timeSeriesPoint
	source   string // "metro", "city", "estimated"
}

// CalculateCorrelations computes pairwise market correlations for the given locations.
// Uses 5 years of monthly ZHVI/ZORI data from the metro_time_series table.
func (ca *CorrelationAnalyzer) CalculateCorrelations(ctx context.Context, locations []string) *CorrelationResult {
	if ca.metro == nil || len(locations) < 2 {
		return &CorrelationResult{
			Correlations:  []investment.MarketCorrelation{},
			Opportunities: []DiversificationOpportunity{},
			Score:         0,
			DataQualityNote: "Single location — diversification analysis not applicable",
		}
	}

	// Fetch time series for all locations
	seriesMap := ca.fetchTimeSeries(ctx, locations)

	locationsWithData := 0
	for _, loc := range locations {
		if ts, ok := seriesMap[loc]; ok && (len(ts.zhvi) > 0 || len(ts.zori) > 0) {
			locationsWithData++
		}
	}

	if locationsWithData < 2 {
		return &CorrelationResult{
			Correlations:  []investment.MarketCorrelation{},
			Opportunities: []DiversificationOpportunity{},
			Score:         float64(len(locations)) * 20, // Location-count fallback
			DataQualityNote: fmt.Sprintf("Only %d of %d locations had historical data — using location count for diversification", locationsWithData, len(locations)),
		}
	}

	// Compute pairwise correlations
	var correlations []investment.MarketCorrelation

	for i := 0; i < len(locations); i++ {
		for j := i + 1; j < len(locations); j++ {
			ts1, ok1 := seriesMap[locations[i]]
			ts2, ok2 := seriesMap[locations[j]]
			if !ok1 || !ok2 {
				continue
			}

			priceCorr := pearsonCorrelation(ts1.zhvi, ts2.zhvi)
			rentCorr := pearsonCorrelation(ts1.zori, ts2.zori)

			// Overall = average of price and rent correlation
			// If only one type has data, use that alone
			var overall float64
			count := 0
			if !math.IsNaN(priceCorr) {
				overall += priceCorr
				count++
			}
			if !math.IsNaN(rentCorr) {
				overall += rentCorr
				count++
			}
			if count > 0 {
				overall /= float64(count)
			}

			correlations = append(correlations, investment.MarketCorrelation{
				Market1:          locations[i],
				Market2:          locations[j],
				Correlation:      math.Round(overall*1000) / 1000, // 3 decimal places
				PriceCorrelation: math.Round(priceCorr*1000) / 1000,
				RentCorrelation:  math.Round(rentCorr*1000) / 1000,
			})

			ca.logger.Debug("correlation computed",
				"market1", locations[i],
				"market2", locations[j],
				"price", priceCorr,
				"rent", rentCorr,
				"overall", overall,
			)
		}
	}

	// Find diversification opportunities (low-correlation pairs)
	opportunities := findDiversificationOpportunities(correlations)

	// Calculate portfolio diversification score
	score := calculateCorrelationBasedScore(locations, correlations)

	var note string
	if locationsWithData < len(locations) {
		note = fmt.Sprintf("%d of %d locations had limited historical data", len(locations)-locationsWithData, len(locations))
	}

	return &CorrelationResult{
		Correlations:    correlations,
		Opportunities:   opportunities,
		Score:           score,
		DataQualityNote: note,
	}
}

// fetchTimeSeries retrieves ZHVI/ZORI time series for each location.
// Maps city to parent metro for time series lookup.
func (ca *CorrelationAnalyzer) fetchTimeSeries(ctx context.Context, locations []string) map[string]*locationTimeSeries {
	result := make(map[string]*locationTimeSeries)

	endDate := time.Now()
	startDate := endDate.AddDate(-5, 0, 0) // 5 years of data

	for _, loc := range locations {
		city, state := splitLocation(loc)
		if city == "" || state == "" {
			continue
		}

		ts := &locationTimeSeries{location: loc}

		// Find parent metro for this city
		mapping, err := ca.metro.GetCityMetroMapping(ctx, city, state)
		if err != nil {
			ca.logger.Debug("no metro mapping for city, skipping", "location", loc, "error", err)
			result[loc] = ts
			continue
		}

		if mapping.MetroName == "" {
			ca.logger.Debug("no metro name for city", "location", loc)
			result[loc] = ts
			continue
		}

		// Fetch time series from metro
		series, err := ca.metro.GetTimeSeries(ctx, mapping.MetroName, "msa", startDate, endDate)
		if err != nil {
			ca.logger.Debug("failed to fetch time series", "location", loc, "metro", mapping.MetroName, "error", err)
			result[loc] = ts
			continue
		}

		// Convert to timeSeriesPoint slices
		for _, dp := range series {
			dateKey := dp.Date.Format("2006-01")
			if dp.ZHVI > 0 {
				ts.zhvi = append(ts.zhvi, timeSeriesPoint{date: dateKey, value: dp.ZHVI})
			}
			if dp.ZORI > 0 {
				ts.zori = append(ts.zori, timeSeriesPoint{date: dateKey, value: dp.ZORI})
			}
		}
		ts.source = "metro"

		ca.logger.Debug("fetched time series",
			"location", loc,
			"metro", mapping.MetroName,
			"zhvi_points", len(ts.zhvi),
			"zori_points", len(ts.zori),
		)

		result[loc] = ts
	}

	return result
}

// pearsonCorrelation computes the Pearson correlation coefficient between two time series.
// Returns NaN if insufficient aligned data points (< 6 months).
func pearsonCorrelation(series1, series2 []timeSeriesPoint) float64 {
	if len(series1) == 0 || len(series2) == 0 {
		return math.NaN()
	}

	// Align series by date
	values1, values2 := alignSeries(series1, series2)

	n := len(values1)
	if n < 6 { // Need at least 6 months for meaningful correlation
		return math.NaN()
	}

	// Calculate means
	var sum1, sum2 float64
	for i := 0; i < n; i++ {
		sum1 += values1[i]
		sum2 += values2[i]
	}
	mean1 := sum1 / float64(n)
	mean2 := sum2 / float64(n)

	// Calculate Pearson coefficient: r = Σ((xi-x̄)(yi-ȳ)) / √(Σ(xi-x̄)² × Σ(yi-ȳ)²)
	var numerator, denom1, denom2 float64
	for i := 0; i < n; i++ {
		d1 := values1[i] - mean1
		d2 := values2[i] - mean2
		numerator += d1 * d2
		denom1 += d1 * d1
		denom2 += d2 * d2
	}

	denominator := math.Sqrt(denom1 * denom2)
	if denominator == 0 {
		return math.NaN() // No variance in one or both series
	}

	return numerator / denominator
}

// alignSeries matches two time series by date and returns aligned value slices
func alignSeries(series1, series2 []timeSeriesPoint) ([]float64, []float64) {
	// Build date map for series2
	map2 := make(map[string]float64, len(series2))
	for _, pt := range series2 {
		map2[pt.date] = pt.value
	}

	// Find common dates
	var vals1, vals2 []float64
	for _, pt := range series1 {
		if v2, ok := map2[pt.date]; ok {
			vals1 = append(vals1, pt.value)
			vals2 = append(vals2, v2)
		}
	}

	return vals1, vals2
}

// findDiversificationOpportunities identifies low-correlation market pairs
func findDiversificationOpportunities(correlations []investment.MarketCorrelation) []DiversificationOpportunity {
	var opportunities []DiversificationOpportunity

	for _, corr := range correlations {
		absCorr := math.Abs(corr.Correlation)

		var opp DiversificationOpportunity
		opp.Market1 = corr.Market1
		opp.Market2 = corr.Market2
		opp.Correlation = corr.Correlation

		if absCorr < 0.3 {
			// Strong diversification
			opp.Score = (1 - absCorr) * 100
			opp.Reasoning = fmt.Sprintf("Strong diversification (%.0f%% correlation) — markets move independently",
				absCorr*100)
			opportunities = append(opportunities, opp)
		} else if absCorr < 0.5 {
			// Moderate diversification
			opp.Score = (1 - absCorr) * 100 * 0.7
			opp.Reasoning = fmt.Sprintf("Moderate diversification (%.0f%% correlation) — some independent movement",
				absCorr*100)
			opportunities = append(opportunities, opp)
		}
		// Correlation >= 0.5 = no diversification benefit
	}

	// Sort by score descending
	sort.Slice(opportunities, func(i, j int) bool {
		return opportunities[i].Score > opportunities[j].Score
	})

	return opportunities
}

// calculateCorrelationBasedScore computes a 0-100 diversification score.
// Based on average pairwise correlation, with a bonus for more locations.
func calculateCorrelationBasedScore(locations []string, correlations []investment.MarketCorrelation) float64 {
	if len(locations) <= 1 {
		return 0
	}

	if len(correlations) == 0 {
		// No correlation data — fall back to location count
		return math.Min(100, float64(len(locations))*20)
	}

	// Average absolute correlation across all pairs
	var totalCorr float64
	for _, c := range correlations {
		totalCorr += math.Abs(c.Correlation)
	}
	avgCorr := totalCorr / float64(len(correlations))

	// Base score: lower correlation = higher score
	baseScore := (1 - avgCorr) * 100

	// Location bonus: +2 per additional location (max +10 for 5+ locations)
	locationBonus := float64(len(locations)-1) * 2
	if locationBonus > 10 {
		locationBonus = 10
	}

	score := baseScore + locationBonus
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	return math.Round(score*10) / 10 // 1 decimal place
}

// ComputeMarketVolatilities fetches ZHVI time series for the given locations and returns
// a map of "City, State" -> annualized price volatility (%). Computed as the annualized
// standard deviation of monthly log-returns over 5 years.
// Returns an empty map when the metro reader is unavailable or data is insufficient.
func (ca *CorrelationAnalyzer) ComputeMarketVolatilities(ctx context.Context, locations []string) map[string]float64 {
	result := make(map[string]float64)
	if ca.metro == nil || len(locations) == 0 {
		return result
	}

	seriesMap := ca.fetchTimeSeries(ctx, locations)

	for _, loc := range locations {
		ts, ok := seriesMap[loc]
		if !ok || len(ts.zhvi) < 12 {
			continue // Need at least 12 monthly observations
		}

		// Sort by date ascending
		sort.Slice(ts.zhvi, func(i, j int) bool {
			return ts.zhvi[i].date < ts.zhvi[j].date
		})

		// Compute monthly log-returns
		logReturns := make([]float64, 0, len(ts.zhvi)-1)
		for i := 1; i < len(ts.zhvi); i++ {
			if ts.zhvi[i-1].value <= 0 || ts.zhvi[i].value <= 0 {
				continue
			}
			lr := math.Log(ts.zhvi[i].value / ts.zhvi[i-1].value)
			logReturns = append(logReturns, lr)
		}
		if len(logReturns) < 12 {
			continue
		}

		// Standard deviation of monthly log-returns → annualize by ×√12
		var sum, sumSq float64
		n := float64(len(logReturns))
		for _, r := range logReturns {
			sum += r
			sumSq += r * r
		}
		mean := sum / n
		variance := (sumSq/n - mean*mean)
		if variance <= 0 {
			continue
		}
		monthlyStdDev := math.Sqrt(variance)
		annualVol := monthlyStdDev * math.Sqrt(12) * 100 // convert to percentage

		// Sanity-clamp to [2%, 20%] — outside this range the data is likely anomalous
		if annualVol < 2 {
			annualVol = 2
		}
		if annualVol > 20 {
			annualVol = 20
		}

		result[loc] = math.Round(annualVol*10) / 10 // 1 decimal place
		ca.logger.Debug("computed market volatility",
			"location", loc,
			"annualVol_pct", result[loc],
			"samples", len(logReturns),
		)
	}

	return result
}

// GenerateAllocationRationale creates a human-readable explanation for why
// each market received its allocation percentage.
func GenerateAllocationRationale(
	allocations map[string]investment.LocationAllocation,
	marketQuality []investment.LocationMarketAnalysis,
	correlations []investment.MarketCorrelation,
) map[string]string {
	rationale := make(map[string]string)

	// Build quality lookup
	qualityByLoc := make(map[string]investment.LocationMarketAnalysis)
	for _, mq := range marketQuality {
		qualityByLoc[mq.Location] = mq
	}

	// Build correlation lookup for each location
	corrByLoc := make(map[string][]float64)
	for _, c := range correlations {
		corrByLoc[c.Market1] = append(corrByLoc[c.Market1], c.Correlation)
		corrByLoc[c.Market2] = append(corrByLoc[c.Market2], c.Correlation)
	}

	for loc, alloc := range allocations {
		var parts []string

		// Quality score
		if mq, ok := qualityByLoc[loc]; ok {
			parts = append(parts, fmt.Sprintf("Market quality: %d/100 (%s)", mq.MarketQualityScore, mq.MarketQualityRating))

			if mq.EmploymentGrowthRate != nil && *mq.EmploymentGrowthRate > 1 {
				parts = append(parts, fmt.Sprintf("Strong employment growth (%.1f%%)", *mq.EmploymentGrowthRate))
			}
			if mq.PopulationGrowth != nil && *mq.PopulationGrowth > 0.5 {
				parts = append(parts, fmt.Sprintf("Population growth (%.1f%%)", *mq.PopulationGrowth))
			}
		}

		// Correlation insight
		if corrs, ok := corrByLoc[loc]; ok && len(corrs) > 0 {
			var avgCorr float64
			for _, c := range corrs {
				avgCorr += math.Abs(c)
			}
			avgCorr /= float64(len(corrs))

			if avgCorr < 0.3 {
				parts = append(parts, "Low correlation with other markets (good diversifier)")
			} else if avgCorr < 0.5 {
				parts = append(parts, "Moderate correlation with other markets")
			} else {
				parts = append(parts, "Highly correlated with other markets")
			}
		}

		// Allocation size context
		parts = append(parts, fmt.Sprintf("Allocation: %.0f%% (%d properties)", alloc.Percentage, alloc.PropertyCount))

		rationale[loc] = strings.Join(parts, ". ")
	}

	return rationale
}
