package importer

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RedfinRecord represents a parsed row from a Redfin TSV file.
type RedfinRecord struct {
	Region       string
	RegionType   string
	RegionTypeID string
	State        string
	StateCode    string
	PropertyType string // "All Residential", "Single Family", "Condo/Co-op", etc.
	PeriodBegin  string // "2024-01-01"
	PeriodEnd    string

	// Metrics (parsed from string columns)
	MedianSalePrice    *float64
	MedianSalePriceYoy *float64
	MedianListPrice    *float64
	MedianListPriceYoy *float64
	MedianPpsf         *float64
	MedianDOM          *int
	HomesSold          *int
	HomesSoldYoy       *float64
	NewListings        *int
	Inventory          *int
	MonthsOfSupply     *float64
	AvgSaleToList      *float64
	SoldAboveList      *float64
	PriceDrops         *float64
	OffMarketIn2Weeks  *float64
}

// RedfinMetrics is the JSONB-serializable metrics for a single period.
type RedfinMetrics struct {
	MedianSalePrice    *float64 `json:"medianSalePrice,omitempty"`
	MedianSalePriceYoy *float64 `json:"medianSalePriceYoy,omitempty"`
	MedianListPrice    *float64 `json:"medianListPrice,omitempty"`
	MedianListPriceYoy *float64 `json:"medianListPriceYoy,omitempty"`
	MedianPpsf         *float64 `json:"medianPpsf,omitempty"`
	MedianDOM          *int     `json:"medianDom,omitempty"`
	HomesSold          *int     `json:"homesSold,omitempty"`
	HomesSoldYoy       *float64 `json:"homesSoldYoy,omitempty"`
	NewListings        *int     `json:"newListings,omitempty"`
	Inventory          *int     `json:"inventory,omitempty"`
	MonthsOfSupply     *float64 `json:"monthsOfSupply,omitempty"`
	AvgSaleToList      *float64 `json:"avgSaleToList,omitempty"`
	SoldAboveList      *float64 `json:"soldAboveList,omitempty"`
	PriceDrops         *float64 `json:"priceDrops,omitempty"`
	OffMarketIn2Weeks  *float64 `json:"offMarketIn2Weeks,omitempty"`
}

// parseRedfinMetrics converts a RedfinRecord to serializable metrics.
func parseRedfinMetrics(r RedfinRecord) RedfinMetrics {
	return RedfinMetrics{
		MedianSalePrice:    r.MedianSalePrice,
		MedianSalePriceYoy: r.MedianSalePriceYoy,
		MedianListPrice:    r.MedianListPrice,
		MedianListPriceYoy: r.MedianListPriceYoy,
		MedianPpsf:         r.MedianPpsf,
		MedianDOM:          r.MedianDOM,
		HomesSold:          r.HomesSold,
		HomesSoldYoy:       r.HomesSoldYoy,
		NewListings:        r.NewListings,
		Inventory:          r.Inventory,
		MonthsOfSupply:     r.MonthsOfSupply,
		AvgSaleToList:      r.AvgSaleToList,
		SoldAboveList:      r.SoldAboveList,
		PriceDrops:         r.PriceDrops,
		OffMarketIn2Weeks:  r.OffMarketIn2Weeks,
	}
}

// streamRedfinTSV downloads and streams a gzipped Redfin TSV, calling onRecord for each row.
// This supports files of any size since it processes one record at a time.
func streamRedfinTSV(ctx context.Context, url string, onRecord func(RedfinRecord) error) error {
	reader, _, err := downloadGzipStream(ctx, url)
	if err != nil {
		return err
	}
	defer reader.Close() // gzipReadCloser.Close() handles gzip + body + cancel

	// Create TSV reader
	tsvReader := csv.NewReader(bufio.NewReaderSize(reader, 256*1024)) // 256KB buffer
	tsvReader.Comma = '\t'
	tsvReader.LazyQuotes = true
	tsvReader.FieldsPerRecord = -1 // Variable field count

	// Read header
	header, err := tsvReader.Read()
	if err != nil {
		return fmt.Errorf("read TSV header: %w", err)
	}

	colIdx := make(map[string]int, len(header))
	for i, col := range header {
		// Normalize to lowercase — Redfin uses UPPERCASE headers
		colIdx[strings.ToLower(strings.TrimSpace(col))] = i
	}

	// Stream records
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		fields, err := tsvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed rows
		}

		record := parseRedfinFields(fields, colIdx)
		if record.PeriodBegin == "" || len(record.PeriodBegin) < 7 {
			continue // skip records without valid period
		}

		if err := onRecord(record); err != nil {
			return err
		}
	}

	return nil
}

// parseRedfinFields extracts a RedfinRecord from raw TSV fields using column indices.
func parseRedfinFields(fields []string, colIdx map[string]int) RedfinRecord {
	get := func(col string) string {
		if idx, ok := colIdx[col]; ok && idx < len(fields) {
			return strings.TrimSpace(fields[idx])
		}
		return ""
	}

	return RedfinRecord{
		Region:             get("region"),
		RegionType:         get("region_type"),
		RegionTypeID:       get("region_type_id"),
		State:              get("state"),
		StateCode:          get("state_code"),
		PropertyType:       get("property_type"),
		PeriodBegin:        get("period_begin"),
		PeriodEnd:          get("period_end"),
		MedianSalePrice:    safeParseFloat(get("median_sale_price")),
		MedianSalePriceYoy: safeParseFloat(get("median_sale_price_yoy")),
		MedianListPrice:    safeParseFloat(get("median_list_price")),
		MedianListPriceYoy: safeParseFloat(get("median_list_price_yoy")),
		MedianPpsf:         safeParseFloat(get("median_ppsf")),
		MedianDOM:          safeParseInt(get("median_dom")),
		HomesSold:          safeParseInt(get("homes_sold")),
		HomesSoldYoy:       safeParseFloat(get("homes_sold_yoy")),
		NewListings:        safeParseInt(get("new_listings")),
		Inventory:          safeParseInt(get("inventory")),
		MonthsOfSupply:     safeParseFloat(get("months_of_supply")),
		AvgSaleToList:      safeParseFloat(get("avg_sale_to_list")),
		SoldAboveList:      safeParseFloat(get("sold_above_list")),
		PriceDrops:         safeParseFloat(get("price_drops")),
		OffMarketIn2Weeks:  safeParseFloat(get("off_market_in_two_weeks")),
	}
}

// safeParseFloat parses a string to *float64, returning nil for empty/NA/invalid values.
func safeParseFloat(s string) *float64 {
	if s == "" || s == "NA" || s == "null" || s == "NaN" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// safeParseInt parses a string to *int, returning nil for empty/NA/invalid values.
func safeParseInt(s string) *int {
	if s == "" || s == "NA" || s == "null" || s == "NaN" {
		return nil
	}
	// Handle floats like "1234.0"
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	n := int(f)
	return &n
}
