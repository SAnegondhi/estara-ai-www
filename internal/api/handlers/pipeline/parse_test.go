// Package pipeline — unit tests for broker OM extraction logic (ADR-102).
//
// These tests cover parseExtractionJSON directly, using pre-canned JSON payloads
// that simulate Claude's response for each of the three canonical OM test cases
// documented in ADR-102 Appendix A. No HTTP mocking required.
package pipeline

import (
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// ptr helpers
func pf(v float64) *float64 { return &v }
func pi(v int) *int          { return &v }
func ps(v string) *string    { return &v }

// ---------------------------------------------------------------------------
// OM 1 — Stabilized Multifamily, Port Orchard WA (Lund Pointe Apartments)
// Structured income statement + rent roll. Only current in-place rent.
// ---------------------------------------------------------------------------

const om1JSON = `{
  "address": "3300 Valentine Ln SE",
  "city": "Port Orchard",
  "state": "WA",
  "zip": "98366",
  "propertyType": "multifamily",
  "beds": null,
  "baths": null,
  "sqft": 23760,
  "yearBuilt": 1995,
  "units": 25,
  "askingPrice": 3150000,
  "brokerRentCurrent": 22285,
  "brokerRentProForma": null,
  "brokerRentMarket": null,
  "brokerCapRate": 0.050,
  "vacancyRate": 0.030,
  "downPaymentPct": null,
  "interestRate": null,
  "financingType": null,
  "confidence": {
    "address": "high",
    "city": "high",
    "state": "high",
    "zip": "high",
    "propertyType": "high",
    "sqft": "high",
    "yearBuilt": "high",
    "units": "high",
    "askingPrice": "high",
    "brokerRentCurrent": "high",
    "brokerCapRate": "medium",
    "vacancyRate": "high"
  }
}`

func TestParseExtractionJSON_OM1_StabilizedMultifamily(t *testing.T) {
	result, err := parseExtractionJSON(om1JSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Extracted {
		t.Error("expected Extracted=true for OM1")
	}
	if result.Note != "" {
		t.Errorf("expected empty Note for successful extraction, got: %q", result.Note)
	}

	// Core property fields
	assertStr(t, "address", result.Address, "3300 Valentine Ln SE")
	assertStr(t, "city", result.City, "Port Orchard")
	assertStr(t, "state", result.State, "WA")
	assertStr(t, "zip", result.Zip, "98366")
	assertStr(t, "propertyType", result.PropertyType, "multifamily")
	assertInt(t, "sqft", result.Sqft, 23760)
	assertInt(t, "yearBuilt", result.YearBuilt, 1995)
	assertInt(t, "units", result.Units, 25)
	assertFloat(t, "askingPrice", result.AskingPrice, 3150000)

	// Rent fields: only current in-place; pro forma and market must be null
	assertFloat(t, "brokerRentCurrent", result.BrokerRentCurrent, 22285)
	assertNilFloat(t, "brokerRentProForma", result.BrokerRentProForma)
	assertNilFloat(t, "brokerRentMarket", result.BrokerRentMarket)

	// Cap rate and vacancy
	assertFloat(t, "brokerCapRate", result.BrokerCapRate, 0.050)
	assertFloat(t, "vacancyRate", result.VacancyRate, 0.030)

	// Financing fields not stated in OM1
	assertNilFloat(t, "downPaymentPct", result.DownPaymentPct)
	assertNilFloat(t, "interestRate", result.InterestRate)
	assertNilStr(t, "financingType", result.FinancingType)

	// Confidence
	if result.Confidence["askingPrice"] != "high" {
		t.Errorf("expected askingPrice confidence=high, got %q", result.Confidence["askingPrice"])
	}
	if result.Confidence["brokerCapRate"] != "medium" {
		t.Errorf("expected brokerCapRate confidence=medium (implied from NOI/price), got %q", result.Confidence["brokerCapRate"])
	}
}

// ---------------------------------------------------------------------------
// OM 2 — Value-Add Multifamily, Garden City CO (Garden City Apartments)
// Three rent columns: current in-place, pro forma, and market. Primary stress
// test for rent field disambiguation — must never conflate the three.
// ---------------------------------------------------------------------------

const om2JSON = `{
  "address": "609-615 E 26th Street",
  "city": "Garden City",
  "state": "CO",
  "zip": "80631",
  "propertyType": "multifamily",
  "beds": null,
  "baths": null,
  "sqft": null,
  "yearBuilt": 1955,
  "units": 19,
  "askingPrice": 1375000,
  "brokerRentCurrent": 9950,
  "brokerRentProForma": 12150,
  "brokerRentMarket": 15500,
  "brokerCapRate": 0.060,
  "vacancyRate": 0.050,
  "downPaymentPct": null,
  "interestRate": null,
  "financingType": null,
  "confidence": {
    "address": "high",
    "city": "high",
    "state": "high",
    "zip": "high",
    "propertyType": "high",
    "yearBuilt": "high",
    "units": "high",
    "askingPrice": "high",
    "brokerRentCurrent": "high",
    "brokerRentProForma": "high",
    "brokerRentMarket": "medium",
    "brokerCapRate": "high",
    "vacancyRate": "high"
  }
}`

func TestParseExtractionJSON_OM2_ValueAdd_ThreeRentColumns(t *testing.T) {
	result, err := parseExtractionJSON(om2JSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Extracted {
		t.Error("expected Extracted=true for OM2")
	}

	assertStr(t, "address", result.Address, "609-615 E 26th Street")
	assertStr(t, "city", result.City, "Garden City")
	assertStr(t, "state", result.State, "CO")
	assertInt(t, "units", result.Units, 19)
	assertFloat(t, "askingPrice", result.AskingPrice, 1375000)

	// The core of OM2: all three rent fields must be distinct and non-null.
	assertFloat(t, "brokerRentCurrent", result.BrokerRentCurrent, 9950)
	assertFloat(t, "brokerRentProForma", result.BrokerRentProForma, 12150)
	assertFloat(t, "brokerRentMarket", result.BrokerRentMarket, 15500)

	// Verify they are not conflated (none should equal the others).
	if *result.BrokerRentCurrent == *result.BrokerRentProForma {
		t.Error("brokerRentCurrent must not equal brokerRentProForma")
	}
	if *result.BrokerRentProForma == *result.BrokerRentMarket {
		t.Error("brokerRentProForma must not equal brokerRentMarket")
	}

	// Current < ProForma < Market (the value-add trajectory)
	if *result.BrokerRentCurrent >= *result.BrokerRentProForma {
		t.Errorf("OM2: current rent (%v) should be below pro forma (%v)", *result.BrokerRentCurrent, *result.BrokerRentProForma)
	}
	if *result.BrokerRentProForma >= *result.BrokerRentMarket {
		t.Errorf("OM2: pro forma rent (%v) should be below market (%v)", *result.BrokerRentProForma, *result.BrokerRentMarket)
	}

	assertFloat(t, "brokerCapRate", result.BrokerCapRate, 0.060)
	assertFloat(t, "vacancyRate", result.VacancyRate, 0.050)

	// Confidence: market rent is medium (cited as range, not specific figure)
	if result.Confidence["brokerRentMarket"] != "medium" {
		t.Errorf("expected brokerRentMarket confidence=medium, got %q", result.Confidence["brokerRentMarket"])
	}
}

// ---------------------------------------------------------------------------
// OM 3 — Modified NNN Commercial, Atlanta GA (349-353 Edgewood Ave)
// Prose-only OM with no structured tables. Validates:
//   - extracted=true when price + cap rate found (partial extraction is success)
//   - all rent fields null (no hallucination)
//   - no expense breakdown, no unit mix
// ---------------------------------------------------------------------------

const om3JSON = `{
  "address": "349-353 Edgewood Avenue",
  "city": "Atlanta",
  "state": "GA",
  "zip": "30312",
  "propertyType": "nnn",
  "beds": null,
  "baths": null,
  "sqft": 3140,
  "yearBuilt": 1946,
  "units": null,
  "askingPrice": 1085000,
  "brokerRentCurrent": null,
  "brokerRentProForma": null,
  "brokerRentMarket": null,
  "brokerCapRate": 0.070,
  "vacancyRate": null,
  "downPaymentPct": null,
  "interestRate": null,
  "financingType": null,
  "confidence": {
    "address": "high",
    "city": "high",
    "state": "high",
    "zip": "high",
    "propertyType": "high",
    "sqft": "high",
    "yearBuilt": "high",
    "askingPrice": "high",
    "brokerCapRate": "high"
  }
}`

func TestParseExtractionJSON_OM3_ProseOnly_NNN(t *testing.T) {
	result, err := parseExtractionJSON(om3JSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// OM3 is a partial extraction — price and cap rate found — so extracted=true.
	// This is the correct behaviour: a valid partial extraction is not a failure.
	if !result.Extracted {
		t.Error("expected Extracted=true for OM3 (price + cap rate found; partial extraction is success)")
	}
	if result.Note != "" {
		t.Errorf("expected empty Note for partial-but-valid extraction, got: %q", result.Note)
	}

	assertStr(t, "address", result.Address, "349-353 Edgewood Avenue")
	assertStr(t, "city", result.City, "Atlanta")
	assertStr(t, "state", result.State, "GA")
	assertStr(t, "propertyType", result.PropertyType, "nnn")
	assertFloat(t, "askingPrice", result.AskingPrice, 1085000)
	assertFloat(t, "brokerCapRate", result.BrokerCapRate, 0.070)
	assertInt(t, "sqft", result.Sqft, 3140)
	assertInt(t, "yearBuilt", result.YearBuilt, 1946)

	// Anti-hallucination: all rent fields must be null for a prose-only NNN OM.
	// If any of these fail, the extraction prompt is hallucinating.
	assertNilFloat(t, "brokerRentCurrent", result.BrokerRentCurrent)
	assertNilFloat(t, "brokerRentProForma", result.BrokerRentProForma)
	assertNilFloat(t, "brokerRentMarket", result.BrokerRentMarket)

	// No vacancy assumption in a prose NNN OM.
	assertNilFloat(t, "vacancyRate", result.VacancyRate)

	// No unit count (commercial building, not counted in residential units).
	assertNilInt(t, "units", result.Units)

	// No financing terms stated.
	assertNilFloat(t, "downPaymentPct", result.DownPaymentPct)
	assertNilFloat(t, "interestRate", result.InterestRate)
	assertNilStr(t, "financingType", result.FinancingType)

	// Confidence should only contain keys for extracted fields.
	if _, ok := result.Confidence["brokerRentCurrent"]; ok {
		t.Error("confidence map must not contain brokerRentCurrent for OM3 (field was null)")
	}
	if _, ok := result.Confidence["vacancyRate"]; ok {
		t.Error("confidence map must not contain vacancyRate for OM3 (field was null)")
	}
	if result.Confidence["askingPrice"] != "high" {
		t.Errorf("expected askingPrice confidence=high, got %q", result.Confidence["askingPrice"])
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestParseExtractionJSON_AllNulls_ExtractedFalse(t *testing.T) {
	// Completely null response — nothing extracted.
	rawJSON := `{
		"address": null, "city": null, "state": null, "zip": null,
		"propertyType": null, "beds": null, "baths": null, "sqft": null,
		"yearBuilt": null, "units": null, "askingPrice": null,
		"brokerRentCurrent": null, "brokerRentProForma": null, "brokerRentMarket": null,
		"brokerCapRate": null, "vacancyRate": null,
		"downPaymentPct": null, "interestRate": null, "financingType": null,
		"confidence": {}
	}`
	result, err := parseExtractionJSON(rawJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Extracted {
		t.Error("expected Extracted=false when all fields are null")
	}
	if result.Note == "" {
		t.Error("expected non-empty Note when Extracted=false")
	}
	if !strings.Contains(result.Note, "manually") {
		t.Errorf("Note should contain 'manually', got: %q", result.Note)
	}
}

func TestParseExtractionJSON_CodeFenceStripping(t *testing.T) {
	// Claude sometimes wraps JSON in markdown code fences — verify they are stripped
	// before parseExtractionJSON is called (stripping happens in extractDocumentFields).
	// Test that parseExtractionJSON handles clean JSON correctly.
	rawJSON := `{"askingPrice": 500000, "city": "Denver", "brokerRentCurrent": null, "brokerRentProForma": null, "brokerRentMarket": null, "confidence": {"askingPrice": "high", "city": "high"}}`
	result, err := parseExtractionJSON(rawJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Extracted {
		t.Error("expected Extracted=true")
	}
	assertFloat(t, "askingPrice", result.AskingPrice, 500000)
	assertStr(t, "city", result.City, "Denver")
}

func TestParseExtractionJSON_InvalidJSON_ReturnsError(t *testing.T) {
	_, err := parseExtractionJSON(`{ this is not valid JSON`)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseExtractionJSON_OnlyCapRateFound_ExtractedTrue(t *testing.T) {
	// A prose OM that states only a cap rate in the executive summary.
	// extracted should be true because brokerCapRate is a substantive field.
	rawJSON := `{
		"address": null, "city": null, "state": null, "zip": null,
		"propertyType": "commercial", "beds": null, "baths": null, "sqft": null,
		"yearBuilt": null, "units": null, "askingPrice": null,
		"brokerRentCurrent": null, "brokerRentProForma": null, "brokerRentMarket": null,
		"brokerCapRate": 0.065, "vacancyRate": null,
		"downPaymentPct": null, "interestRate": null, "financingType": null,
		"confidence": {"brokerCapRate": "high", "propertyType": "high"}
	}`
	result, err := parseExtractionJSON(rawJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Extracted {
		t.Error("expected Extracted=true when brokerCapRate is found")
	}
	assertFloat(t, "brokerCapRate", result.BrokerCapRate, 0.065)
}

func TestParseExtractionJSON_RentFieldsAreIndependent(t *testing.T) {
	// Verify that setting one rent field does not affect the others.
	rawJSON := `{
		"askingPrice": 1000000,
		"brokerRentCurrent": 5000,
		"brokerRentProForma": null,
		"brokerRentMarket": null,
		"confidence": {"askingPrice": "high", "brokerRentCurrent": "high"}
	}`
	result, err := parseExtractionJSON(rawJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFloat(t, "brokerRentCurrent", result.BrokerRentCurrent, 5000)
	assertNilFloat(t, "brokerRentProForma", result.BrokerRentProForma)
	assertNilFloat(t, "brokerRentMarket", result.BrokerRentMarket)
}

// ---------------------------------------------------------------------------
// excelToText tests
// ---------------------------------------------------------------------------

// makeMinimalXLSX creates a minimal valid XLSX file in memory using excelize.
func makeMinimalXLSX(t *testing.T, sheetData map[string][][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	first := true
	for sheet, rows := range sheetData {
		if first {
			// Rename the default Sheet1 to the first sheet name.
			if err := f.SetSheetName("Sheet1", sheet); err != nil {
				t.Fatalf("rename sheet: %v", err)
			}
			first = false
		} else {
			f.NewSheet(sheet)
		}
		for r, row := range rows {
			for c, val := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
				f.SetCellValue(sheet, cell, val)
			}
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestExcelToText_SingleSheet(t *testing.T) {
	xlsx := makeMinimalXLSX(t, map[string][][]string{
		"Financials": {
			{"Asking Price", "$3,150,000"},
			{"Cap Rate", "5.0%"},
			{"Units", "25"},
		},
	})

	text, err := excelToText(xlsx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "Financials") {
		t.Error("output should contain sheet name 'Financials'")
	}
	if !strings.Contains(text, "Asking Price") {
		t.Error("output should contain 'Asking Price'")
	}
	if !strings.Contains(text, "3,150,000") {
		t.Error("output should contain asking price value")
	}
	if !strings.Contains(text, "Cap Rate") {
		t.Error("output should contain 'Cap Rate'")
	}
}

func TestExcelToText_MultipleSheets(t *testing.T) {
	xlsx := makeMinimalXLSX(t, map[string][][]string{
		"Summary":  {{"Property", "Lund Pointe Apartments"}, {"Price", "3150000"}},
		"RentRoll": {{"Unit", "Type", "Rent"}, {"101", "2bd/1ba", "1200"}},
	})

	text, err := excelToText(xlsx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "Lund Pointe") {
		t.Error("output should contain property name from Summary sheet")
	}
	if !strings.Contains(text, "RentRoll") || !strings.Contains(text, "Summary") {
		t.Error("output should include both sheet names")
	}
}

func TestExcelToText_EmptyFile_ReturnsError(t *testing.T) {
	xlsx := makeMinimalXLSX(t, map[string][][]string{
		"Sheet1": {},
	})
	_, err := excelToText(xlsx)
	if err == nil {
		t.Error("expected error for empty Excel file, got nil")
	}
}

func TestExcelToText_InvalidBytes_ReturnsError(t *testing.T) {
	_, err := excelToText([]byte("this is not an xlsx file"))
	if err == nil {
		t.Error("expected error for invalid xlsx bytes")
	}
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func assertStr(t *testing.T, field string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: expected %q, got nil", field, want)
		return
	}
	if *got != want {
		t.Errorf("%s: expected %q, got %q", field, want, *got)
	}
}

func assertNilStr(t *testing.T, field string, got *string) {
	t.Helper()
	if got != nil {
		t.Errorf("%s: expected nil, got %q", field, *got)
	}
}

func assertFloat(t *testing.T, field string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: expected %v, got nil", field, want)
		return
	}
	if *got != want {
		t.Errorf("%s: expected %v, got %v", field, want, *got)
	}
}

func assertNilFloat(t *testing.T, field string, got *float64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s: expected nil, got %v", field, *got)
	}
}

func assertInt(t *testing.T, field string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: expected %d, got nil", field, want)
		return
	}
	if *got != want {
		t.Errorf("%s: expected %d, got %d", field, want, *got)
	}
}

func assertNilInt(t *testing.T, field string, got *int) {
	t.Helper()
	if got != nil {
		t.Errorf("%s: expected nil, got %d", field, *got)
	}
}
