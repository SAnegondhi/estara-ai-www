package bls

import "strings"

// State FIPS codes for BLS Local Area Unemployment Statistics (LAUS)
var stateFIPS = map[string]string{
	"AL": "01", "AK": "02", "AZ": "04", "AR": "05", "CA": "06",
	"CO": "08", "CT": "09", "DE": "10", "DC": "11", "FL": "12",
	"GA": "13", "HI": "15", "ID": "16", "IL": "17", "IN": "18",
	"IA": "19", "KS": "20", "KY": "21", "LA": "22", "ME": "23",
	"MD": "24", "MA": "25", "MI": "26", "MN": "27", "MS": "28",
	"MO": "29", "MT": "30", "NE": "31", "NV": "32", "NH": "33",
	"NJ": "34", "NM": "35", "NY": "36", "NC": "37", "ND": "38",
	"OH": "39", "OK": "40", "OR": "41", "PA": "42", "RI": "44",
	"SC": "45", "SD": "46", "TN": "47", "TX": "48", "UT": "49",
	"VT": "50", "VA": "51", "WA": "53", "WV": "54", "WI": "55",
	"WY": "56",
}

// State name to code mapping
var stateNameToCode = map[string]string{
	"alabama": "AL", "alaska": "AK", "arizona": "AZ", "arkansas": "AR",
	"california": "CA", "colorado": "CO", "connecticut": "CT", "delaware": "DE",
	"district of columbia": "DC", "florida": "FL", "georgia": "GA", "hawaii": "HI",
	"idaho": "ID", "illinois": "IL", "indiana": "IN", "iowa": "IA",
	"kansas": "KS", "kentucky": "KY", "louisiana": "LA", "maine": "ME",
	"maryland": "MD", "massachusetts": "MA", "michigan": "MI", "minnesota": "MN",
	"mississippi": "MS", "missouri": "MO", "montana": "MT", "nebraska": "NE",
	"nevada": "NV", "new hampshire": "NH", "new jersey": "NJ", "new mexico": "NM",
	"new york": "NY", "north carolina": "NC", "north dakota": "ND", "ohio": "OH",
	"oklahoma": "OK", "oregon": "OR", "pennsylvania": "PA", "rhode island": "RI",
	"south carolina": "SC", "south dakota": "SD", "tennessee": "TN", "texas": "TX",
	"utah": "UT", "vermont": "VT", "virginia": "VA", "washington": "WA",
	"west virginia": "WV", "wisconsin": "WI", "wyoming": "WY",
}

// getStateFIPS returns the FIPS code for a state
func getStateFIPS(stateCode string) string {
	return stateFIPS[strings.ToUpper(stateCode)]
}

// metroCBSA maps "City,STATE" to the 5-digit Census CBSA FIPS code for BLS LAUS MSA series.
// Series format: LAUSMSA{CBSA}03 = unemployment rate, LAUSMSA{CBSA}06 = employment (thousands).
// Source: Census CBSA delineation file (Feb 2023). Covers top ~120 metros by analysis frequency.
var metroCBSA = map[string]string{
	// Top 50 metros by population
	"New York,NY":       "35620",
	"Los Angeles,CA":    "31080",
	"Chicago,IL":        "16980",
	"Dallas,TX":         "19100",
	"Houston,TX":        "26420",
	"Washington,DC":     "47900",
	"Miami,FL":          "33100",
	"Philadelphia,PA":   "37980",
	"Atlanta,GA":        "12060",
	"Phoenix,AZ":        "38060",
	"Boston,MA":         "14460",
	"Riverside,CA":      "40140",
	"Seattle,WA":        "42660",
	"San Francisco,CA":  "41860",
	"Detroit,MI":        "19820",
	"San Diego,CA":      "41740",
	"Minneapolis,MN":    "33460",
	"Tampa,FL":          "45300",
	"Denver,CO":         "19740",
	"St. Louis,MO":      "41180",
	"Baltimore,MD":      "12580",
	"Orlando,FL":        "36740",
	"San Antonio,TX":    "41700",
	"Portland,OR":       "38900",
	"Sacramento,CA":     "40900",
	"Pittsburgh,PA":     "38300",
	"Las Vegas,NV":      "29820",
	"Austin,TX":         "12420",
	"Cincinnati,OH":     "17140",
	"Kansas City,MO":    "28140",
	"Columbus,OH":       "18140",
	"Indianapolis,IN":   "26900",
	"Cleveland,OH":      "17460",
	"San Jose,CA":       "41940",
	"Nashville,TN":      "34980",
	"Virginia Beach,VA": "47260",
	"Providence,RI":     "39300",
	"Jacksonville,FL":   "27260",
	"Milwaukee,WI":      "33340",
	"Oklahoma City,OK":  "36420",
	"Raleigh,NC":        "39580",
	"Memphis,TN":        "32820",
	"Richmond,VA":       "40060",
	"Louisville,KY":     "31140",
	"New Orleans,LA":    "35380",
	"Hartford,CT":       "25540",
	"Buffalo,NY":        "15380",
	"Birmingham,AL":     "13820",
	"Salt Lake City,UT": "41620",
	"Rochester,NY":      "40380",
	// Additional high-frequency markets
	"Charlotte,NC":        "16740",
	"Tucson,AZ":           "46060",
	"Fresno,CA":           "23420",
	"Omaha,NE":            "36540",
	"Albuquerque,NM":      "10740",
	"Bakersfield,CA":      "12540",
	"Baton Rouge,LA":      "12940",
	"Honolulu,HI":         "26180",
	"Anchorage,AK":        "11260",
	"Colorado Springs,CO": "17820",
	"Knoxville,TN":        "28940",
	"El Paso,TX":          "21340",
	"Boise,ID":            "14260",
	"Greenville,SC":       "24860",
	"Tulsa,OK":            "46140",
	"Worcester,MA":        "49340",
	"Columbia,SC":         "17900",
	"Stockton,CA":         "44700",
	"Cape Coral,FL":       "15980",
	"Oxnard,CA":           "37100",
	"Bridgeport,CT":       "14860",
	"McAllen,TX":          "32580",
	"Lakeland,FL":         "29460",
	"Greensboro,NC":       "24660",
	"Akron,OH":            "10420",
	"Des Moines,IA":       "19780",
	"Madison,WI":          "31540",
	"Little Rock,AR":      "30780",
	"Wichita,KS":          "48620",
	"Grand Rapids,MI":     "24340",
	"Dayton,OH":           "19430",
	"Palm Bay,FL":         "37340",
	"Chattanooga,TN":      "16860",
	"Syracuse,NY":         "45060",
	"Spokane,WA":          "44060",
	"Ogden,UT":            "36260",
	"Winston-Salem,NC":    "49180",
	"Provo,UT":            "39340",
	"Durham,NC":           "20500",
	"Fayetteville,NC":     "22180",
	"Modesto,CA":          "33700",
	"Pensacola,FL":        "37860",
	"Scranton,PA":         "42540",
	"Harrisburg,PA":       "25420",
	"Springfield,MA":      "44140",
	"Youngstown,OH":       "49660",
	"Jackson,MS":          "27140",
	"Augusta,GA":          "12260",
	"Fort Wayne,IN":       "23060",
	"Shreveport,LA":       "43340",
	"Sarasota,FL":         "42100",
	"Deltona,FL":          "19660",
	"Lexington,KY":        "30460",
	"Lincoln,NE":          "30700",
	"Reno,NV":             "39900",
	"Lansing,MI":          "29620",
	"Naples,FL":           "34940",
}

// GetMetroCBSA returns the 5-digit CBSA FIPS code for a city/state pair.
// Returns "" if the metro is not in the lookup table.
func GetMetroCBSA(city, state string) string {
	key := strings.TrimSpace(city) + "," + strings.ToUpper(strings.TrimSpace(state))
	return metroCBSA[key]
}

// normalizeStateCode converts state name or code to 2-letter code
func normalizeStateCode(state string) string {
	state = strings.TrimSpace(state)

	// Already a 2-letter code
	if len(state) == 2 {
		return strings.ToUpper(state)
	}

	// Look up full state name
	if code, ok := stateNameToCode[strings.ToLower(state)]; ok {
		return code
	}

	return strings.ToUpper(state)
}
