package public

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/db/marketqueries"
	"github.com/estara-ai/www/pkg/httputil"
)

// Market pulse response types

type MarketPulseCounts struct {
	Cities int64 `json:"cities"`
	Metros int64 `json:"metros"`
}

type MarketPulseTickerItem struct {
	City    string  `json:"city"`
	State   string  `json:"state"`
	Price   int     `json:"price"`
	YoY     float64 `json:"yoy"`
	Rent    int     `json:"rent"`
	CapRate float64 `json:"capRate"`
}

type MarketPulseSparkline struct {
	Metro  string `json:"metro"`
	YoY    float64 `json:"yoy"`
	Points []int   `json:"points"`
}

type MarketPulseResponse struct {
	Counts     MarketPulseCounts       `json:"counts"`
	Ticker     []MarketPulseTickerItem `json:"ticker"`
	Sparklines []MarketPulseSparkline  `json:"sparklines"`
	UpdatedAt  string                  `json:"updatedAt"`
}

// In-memory cache for market pulse data
var (
	pulseCache     *MarketPulseResponse
	pulseCacheMu   sync.RWMutex
	pulseCacheTime time.Time
	pulseCacheTTL  = 1 * time.Hour
)

const sparklineCount = 4

// GetMarketPulse returns live market pulse data for the sign-in page.
// GET /api/public/market-pulse
func (h *Handler) GetMarketPulse(w http.ResponseWriter, r *http.Request) {
	// Check cache first
	pulseCacheMu.RLock()
	if pulseCache != nil && time.Since(pulseCacheTime) < pulseCacheTTL {
		cached := pulseCache
		pulseCacheMu.RUnlock()
		w.Header().Set("Cache-Control", "public, max-age=3600")
		httputil.JSON(w, http.StatusOK, cached)
		return
	}
	pulseCacheMu.RUnlock()

	ctx := r.Context()

	// Check that market database is available
	if h.db.Market == nil {
		h.logger.Warn("market database not available for market pulse")
		httputil.Error(w, http.StatusServiceUnavailable, "market data unavailable")
		return
	}

	mq := marketqueries.New(h.db.Market)

	// Run all three queries concurrently — sparklines get a separate 3s timeout
	// so a slow ZHVI scan never blocks the response.
	var (
		counts    marketqueries.GetMarketPulseCountsRow
		cities    []marketqueries.GetMarketPulseTickerCitiesRow
		allMetros []marketqueries.GetSparklineMetrosRow
		countsErr, citiesErr, metrosErr error
		wg sync.WaitGroup
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		counts, countsErr = mq.GetMarketPulseCounts(ctx)
	}()
	go func() {
		defer wg.Done()
		cities, citiesErr = mq.GetMarketPulseTickerCities(ctx)
	}()
	go func() {
		defer wg.Done()
		sparkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		allMetros, metrosErr = mq.GetSparklineMetros(sparkCtx)
	}()
	wg.Wait()

	if countsErr != nil {
		h.logger.Error("failed to get market pulse counts", "error", countsErr)
		httputil.Error(w, http.StatusInternalServerError, "failed to load market data")
		return
	}
	if citiesErr != nil {
		h.logger.Error("failed to get market pulse ticker cities", "error", citiesErr)
		httputil.Error(w, http.StatusInternalServerError, "failed to load market data")
		return
	}
	if metrosErr != nil {
		h.logger.Warn("sparkline metros unavailable, returning without", "error", metrosErr)
		allMetros = nil
	}

	ticker := make([]MarketPulseTickerItem, 0, len(cities))
	for _, c := range cities {
		ticker = append(ticker, MarketPulseTickerItem{
			City:    c.City,
			State:   c.State,
			Price:   numericToInt(c.MedianHomePrice),
			YoY:     numericToFloat(c.PriceYoyChange),
			Rent:    numericToInt(c.MedianRent),
			CapRate: numericToFloat(c.CapRate),
		})
	}

	// Filter to metros that actually have parseable ZHVI data with 12+ months
	type validMetro struct {
		name   string
		points []int
		yoy    float64
	}
	var validMetros []validMetro
	for _, m := range allMetros {
		points, yoy := extractSparklinePoints(m.ZhviData)
		if len(points) >= 12 {
			validMetros = append(validMetros, validMetro{
				name:   m.MetroName,
				points: points,
				yoy:    yoy,
			})
		}
	}

	// Select 4 metros using day-of-year as a rotating offset
	sparklines := make([]MarketPulseSparkline, 0, sparklineCount)
	if len(validMetros) > 0 {
		dayOfYear := time.Now().UTC().YearDay()
		offset := dayOfYear % len(validMetros)
		for i := 0; i < sparklineCount && i < len(validMetros); i++ {
			idx := (offset + i) % len(validMetros)
			m := validMetros[idx]
			sparklines = append(sparklines, MarketPulseSparkline{
				Metro:  m.name,
				YoY:    m.yoy,
				Points: m.points,
			})
		}
	}

	response := &MarketPulseResponse{
		Counts: MarketPulseCounts{
			Cities: counts.CityCount,
			Metros: counts.MetroCount,
		},
		Ticker:     ticker,
		Sparklines: sparklines,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// Cache the response
	pulseCacheMu.Lock()
	pulseCache = response
	pulseCacheTime = time.Now()
	pulseCacheMu.Unlock()

	w.Header().Set("Cache-Control", "public, max-age=3600")
	httputil.JSON(w, http.StatusOK, response)
}

// extractSparklinePoints extracts the last 12 months of ZHVI data from JSONB.
// Returns the points as rounded ints and the year-over-year change percentage.
func extractSparklinePoints(zhviRaw []byte) ([]int, float64) {
	if len(zhviRaw) == 0 {
		return nil, 0
	}

	var data map[string]float64
	if err := json.Unmarshal(zhviRaw, &data); err != nil {
		return nil, 0
	}

	if len(data) == 0 {
		return nil, 0
	}

	// Sort keys descending to get most recent months
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))

	// Take up to 12 most recent months
	n := 12
	if len(keys) < n {
		n = len(keys)
	}
	recentKeys := keys[:n]

	// Reverse to chronological order (oldest first)
	sort.Strings(recentKeys)

	points := make([]int, len(recentKeys))
	for i, k := range recentKeys {
		points[i] = int(math.Round(data[k]))
	}

	// Calculate YoY: compare last point to first point (approx 12 months apart)
	var yoy float64
	if len(points) >= 2 && points[0] > 0 {
		yoy = math.Round(((float64(points[len(points)-1])-float64(points[0]))/float64(points[0])*100)*100) / 100
	}

	return points, yoy
}

// numericToFloat converts pgtype.Numeric to float64, returning 0 if invalid.
func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0
	}
	return math.Round(f.Float64*100) / 100
}

// numericToInt converts pgtype.Numeric to int, returning 0 if invalid.
func numericToInt(n pgtype.Numeric) int {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0
	}
	return int(math.Round(f.Float64))
}
