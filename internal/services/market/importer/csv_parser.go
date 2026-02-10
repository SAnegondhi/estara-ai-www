package importer

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// dateColumnRegex matches Zillow date columns like "2024-01-31"
var dateColumnRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ZillowRow represents a parsed row from a Zillow CSV file.
// Zillow CSVs have fixed metadata columns followed by dynamic date columns.
type ZillowRow struct {
	RegionID   string
	SizeRank   string
	RegionName string
	RegionType string
	StateName  string
	State      string // State code (e.g., "CA") — present in city/zip CSVs
	Metro      string // Metro name — present in city/zip CSVs
	City       string // City name — present in zip CSVs
	CountyName string // County name — present in zip CSVs

	// DateValues holds the time-series values keyed by column header (full date strings)
	DateValues map[string]string
}

// parseZillowCSV parses raw CSV bytes into ZillowRow structs.
func parseZillowCSV(data []byte) ([]ZillowRow, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.LazyQuotes = true

	// Read header
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	// Identify column indices
	colIdx := make(map[string]int, len(header))
	for i, col := range header {
		colIdx[col] = i
	}

	// Read all rows
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}

	rows := make([]ZillowRow, 0, len(records))
	for _, record := range records {
		row := ZillowRow{
			DateValues: make(map[string]string),
		}

		// Extract metadata columns
		if idx, ok := colIdx["RegionID"]; ok && idx < len(record) {
			row.RegionID = record[idx]
		}
		if idx, ok := colIdx["SizeRank"]; ok && idx < len(record) {
			row.SizeRank = record[idx]
		}
		if idx, ok := colIdx["RegionName"]; ok && idx < len(record) {
			row.RegionName = record[idx]
		}
		if idx, ok := colIdx["RegionType"]; ok && idx < len(record) {
			row.RegionType = record[idx]
		}
		if idx, ok := colIdx["StateName"]; ok && idx < len(record) {
			row.StateName = record[idx]
		}
		if idx, ok := colIdx["State"]; ok && idx < len(record) {
			row.State = record[idx]
		}
		if idx, ok := colIdx["Metro"]; ok && idx < len(record) {
			row.Metro = record[idx]
		}
		if idx, ok := colIdx["City"]; ok && idx < len(record) {
			row.City = record[idx]
		}
		if idx, ok := colIdx["CountyName"]; ok && idx < len(record) {
			row.CountyName = record[idx]
		}

		// Extract date columns
		for i, col := range header {
			if dateColumnRegex.MatchString(col) && i < len(record) && record[i] != "" {
				row.DateValues[col] = record[i]
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// extractTimeSeries converts a ZillowRow's date values into a map[string]float64
// using month keys (e.g., "2024-01" from "2024-01-31").
func extractTimeSeries(row ZillowRow) map[string]float64 {
	ts := make(map[string]float64, len(row.DateValues))
	for dateCol, value := range row.DateValues {
		monthKey := dateCol[:7] // "2024-01" from "2024-01-31"
		numValue, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}
		ts[monthKey] = numValue
	}
	return ts
}

// safeAtoi parses a string to int, returning 0 on error.
func safeAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// textOrNull converts a string to pgtype.Text (NULL if empty).
func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

// int4OrNull converts an int32 to pgtype.Int4 (NULL if 0).
func int4OrNull(n int32) pgtype.Int4 {
	if n == 0 {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: n, Valid: true}
}

// lookupMetro finds a metro_region_id from a metro name string.
func lookupMetro(metroName string, lookup map[string]int32) int32 {
	if lookup == nil || metroName == "" {
		return 0
	}
	key := normalizeMetroName(metroName)
	if id, ok := lookup[key]; ok {
		return id
	}
	return 0
}

// normalizeMetroName normalizes metro names for lookup matching.
func normalizeMetroName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// cityRegionHash generates a deterministic int32 region ID from city+state.
// Used for Redfin city data which doesn't have Zillow region IDs.
func cityRegionHash(city, state string) int32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(city + "," + state)))
	return int32(h.Sum32() & 0x7FFFFFFF) // ensure positive
}

func countyRegionHash(county, state string) int32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower("county:" + county + "," + state)))
	return int32(h.Sum32() & 0x7FFFFFFF)
}

func stateRegionHash(stateCode string) int32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower("state:" + stateCode)))
	return int32(h.Sum32() & 0x7FFFFFFF)
}

// stateNameToCode converts a full state name to its 2-letter code.
var stateNameToCodeMap = map[string]string{
	"alabama": "AL", "alaska": "AK", "arizona": "AZ", "arkansas": "AR",
	"california": "CA", "colorado": "CO", "connecticut": "CT", "delaware": "DE",
	"florida": "FL", "georgia": "GA", "hawaii": "HI", "idaho": "ID",
	"illinois": "IL", "indiana": "IN", "iowa": "IA", "kansas": "KS",
	"kentucky": "KY", "louisiana": "LA", "maine": "ME", "maryland": "MD",
	"massachusetts": "MA", "michigan": "MI", "minnesota": "MN", "mississippi": "MS",
	"missouri": "MO", "montana": "MT", "nebraska": "NE", "nevada": "NV",
	"new hampshire": "NH", "new jersey": "NJ", "new mexico": "NM", "new york": "NY",
	"north carolina": "NC", "north dakota": "ND", "ohio": "OH", "oklahoma": "OK",
	"oregon": "OR", "pennsylvania": "PA", "rhode island": "RI", "south carolina": "SC",
	"south dakota": "SD", "tennessee": "TN", "texas": "TX", "utah": "UT",
	"vermont": "VT", "virginia": "VA", "washington": "WA", "west virginia": "WV",
	"wisconsin": "WI", "wyoming": "WY", "district of columbia": "DC",
}

func stateNameToCode(name string) string {
	if code, ok := stateNameToCodeMap[strings.ToLower(strings.TrimSpace(name))]; ok {
		return code
	}
	// If it's already a 2-letter code, return it
	if len(name) == 2 {
		return strings.ToUpper(name)
	}
	return ""
}
