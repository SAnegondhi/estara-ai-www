package expenses

// PropertyTaxInfo contains property tax data for a state
type PropertyTaxInfo struct {
	Rate      float64 // Effective rate as % of home value
	Rank      int     // National ranking (1 = highest)
	MedianTax int     // Median annual tax in dollars
	Notes     string  // Special notes
}

// InsuranceInfo contains insurance data for a state
type InsuranceInfo struct {
	AvgPremium  int      // Average annual premium in dollars
	RatePercent float64  // Premium as % of median home value
	RiskFactors []string // Risk factors (hurricane, tornado, etc.)
	Notes       string   // Special notes
}

// propertyTaxRates contains state property tax rates
// Source: Tax Foundation 2024
// https://taxfoundation.org/data/all/state/property-taxes-by-state-county-2024/
var propertyTaxRates = map[string]PropertyTaxInfo{
	"AL": {Rate: 0.40, Rank: 49, MedianTax: 658, Notes: "Lowest rates in nation"},
	"AK": {Rate: 1.04, Rank: 26, MedianTax: 3464},
	"AZ": {Rate: 0.62, Rank: 42, MedianTax: 1707},
	"AR": {Rate: 0.62, Rank: 41, MedianTax: 878},
	"CA": {Rate: 0.74, Rank: 34, MedianTax: 4279, Notes: "Prop 13 limits increases"},
	"CO": {Rate: 0.51, Rank: 46, MedianTax: 2017},
	"CT": {Rate: 2.14, Rank: 3, MedianTax: 6153, Notes: "Very high rates"},
	"DE": {Rate: 0.57, Rank: 44, MedianTax: 1570},
	"FL": {Rate: 0.86, Rank: 30, MedianTax: 2143, Notes: "Homestead exemption available"},
	"GA": {Rate: 0.90, Rank: 29, MedianTax: 1850},
	"HI": {Rate: 0.28, Rank: 50, MedianTax: 1893, Notes: "Lowest effective rate"},
	"ID": {Rate: 0.67, Rank: 39, MedianTax: 1817},
	"IL": {Rate: 2.27, Rank: 2, MedianTax: 4942, Notes: "Second highest rates"},
	"IN": {Rate: 0.84, Rank: 31, MedianTax: 1308},
	"IA": {Rate: 1.52, Rank: 11, MedianTax: 2522},
	"KS": {Rate: 1.41, Rank: 15, MedianTax: 2235},
	"KY": {Rate: 0.83, Rank: 32, MedianTax: 1320},
	"LA": {Rate: 0.55, Rank: 45, MedianTax: 895},
	"ME": {Rate: 1.28, Rank: 19, MedianTax: 2756},
	"MD": {Rate: 1.07, Rank: 25, MedianTax: 3633},
	"MA": {Rate: 1.23, Rank: 21, MedianTax: 5327},
	"MI": {Rate: 1.54, Rank: 10, MedianTax: 2551},
	"MN": {Rate: 1.12, Rank: 23, MedianTax: 2767},
	"MS": {Rate: 0.79, Rank: 33, MedianTax: 1009},
	"MO": {Rate: 0.97, Rank: 27, MedianTax: 1676},
	"MT": {Rate: 0.74, Rank: 35, MedianTax: 2165},
	"NE": {Rate: 1.73, Rank: 7, MedianTax: 3015},
	"NV": {Rate: 0.60, Rank: 43, MedianTax: 1807},
	"NH": {Rate: 2.18, Rank: 4, MedianTax: 5701, Notes: "No income/sales tax, high property tax"},
	"NJ": {Rate: 2.47, Rank: 1, MedianTax: 8797, Notes: "Highest in nation"},
	"NM": {Rate: 0.67, Rank: 38, MedianTax: 1316},
	"NY": {Rate: 1.72, Rank: 8, MedianTax: 5884},
	"NC": {Rate: 0.82, Rank: 33, MedianTax: 1701},
	"ND": {Rate: 0.94, Rank: 28, MedianTax: 2025},
	"OH": {Rate: 1.59, Rank: 9, MedianTax: 2447},
	"OK": {Rate: 0.87, Rank: 29, MedianTax: 1278},
	"OR": {Rate: 0.97, Rank: 27, MedianTax: 3352},
	"PA": {Rate: 1.50, Rank: 12, MedianTax: 3022},
	"RI": {Rate: 1.53, Rank: 11, MedianTax: 4483},
	"SC": {Rate: 0.57, Rank: 44, MedianTax: 1024, Notes: "Owner-occupied exemptions"},
	"SD": {Rate: 1.22, Rank: 22, MedianTax: 2432, Notes: "No income tax"},
	"TN": {Rate: 0.66, Rank: 40, MedianTax: 1317, Notes: "No income tax"},
	"TX": {Rate: 1.80, Rank: 6, MedianTax: 3907, Notes: "No income tax, high property tax"},
	"UT": {Rate: 0.63, Rank: 41, MedianTax: 2197},
	"VT": {Rate: 1.90, Rank: 5, MedianTax: 4340},
	"VA": {Rate: 0.82, Rank: 33, MedianTax: 2534},
	"WA": {Rate: 0.92, Rank: 28, MedianTax: 4061, Notes: "No income tax"},
	"WV": {Rate: 0.58, Rank: 44, MedianTax: 756},
	"WI": {Rate: 1.76, Rank: 6, MedianTax: 3484},
	"WY": {Rate: 0.61, Rank: 42, MedianTax: 1563, Notes: "No income tax"},
	"DC": {Rate: 0.56, Rank: 45, MedianTax: 3463},
}

// insuranceRates contains state homeowners insurance rates
// Source: Insurance Information Institute / NAIC 2024
// https://www.iii.org/fact-statistic/facts-statistics-homeowners-and-renters-insurance
var insuranceRates = map[string]InsuranceInfo{
	"AL": {AvgPremium: 2384, RatePercent: 1.25, RiskFactors: []string{"hurricane", "tornado"}},
	"AK": {AvgPremium: 1296, RatePercent: 0.40, RiskFactors: []string{"earthquake"}},
	"AZ": {AvgPremium: 1745, RatePercent: 0.50, RiskFactors: []string{"wildfire", "monsoon"}},
	"AR": {AvgPremium: 2530, RatePercent: 1.45, RiskFactors: []string{"tornado", "hail"}},
	"CA": {AvgPremium: 1565, RatePercent: 0.28, RiskFactors: []string{"wildfire", "earthquake"}, Notes: "Earthquake requires separate policy"},
	"CO": {AvgPremium: 2988, RatePercent: 0.60, RiskFactors: []string{"hail", "wildfire"}},
	"CT": {AvgPremium: 1842, RatePercent: 0.55, RiskFactors: []string{"winter_storm"}},
	"DE": {AvgPremium: 1076, RatePercent: 0.35, RiskFactors: []string{}},
	"FL": {AvgPremium: 4231, RatePercent: 1.15, RiskFactors: []string{"hurricane", "flood"}, Notes: "Highest in nation due to hurricane risk"},
	"GA": {AvgPremium: 2137, RatePercent: 0.75, RiskFactors: []string{"hurricane", "tornado"}},
	"HI": {AvgPremium: 1285, RatePercent: 0.18, RiskFactors: []string{"hurricane", "volcano"}},
	"ID": {AvgPremium: 1157, RatePercent: 0.30, RiskFactors: []string{"wildfire"}},
	"IL": {AvgPremium: 1869, RatePercent: 0.80, RiskFactors: []string{"tornado", "hail"}},
	"IN": {AvgPremium: 1467, RatePercent: 0.75, RiskFactors: []string{"tornado"}},
	"IA": {AvgPremium: 1753, RatePercent: 0.95, RiskFactors: []string{"tornado", "hail"}},
	"KS": {AvgPremium: 3246, RatePercent: 1.80, RiskFactors: []string{"tornado", "hail"}, Notes: "High tornado risk"},
	"KY": {AvgPremium: 2165, RatePercent: 1.15, RiskFactors: []string{"tornado", "flood"}},
	"LA": {AvgPremium: 3394, RatePercent: 1.85, RiskFactors: []string{"hurricane", "flood"}, Notes: "Second highest rates"},
	"ME": {AvgPremium: 1254, RatePercent: 0.40, RiskFactors: []string{"winter_storm"}},
	"MD": {AvgPremium: 1548, RatePercent: 0.40, RiskFactors: []string{"hurricane"}},
	"MA": {AvgPremium: 1714, RatePercent: 0.35, RiskFactors: []string{"winter_storm", "hurricane"}},
	"MI": {AvgPremium: 1521, RatePercent: 0.70, RiskFactors: []string{"winter_storm"}},
	"MN": {AvgPremium: 2126, RatePercent: 0.70, RiskFactors: []string{"hail", "tornado"}},
	"MS": {AvgPremium: 2626, RatePercent: 1.90, RiskFactors: []string{"hurricane", "tornado"}},
	"MO": {AvgPremium: 2167, RatePercent: 1.10, RiskFactors: []string{"tornado", "hail"}},
	"MT": {AvgPremium: 2161, RatePercent: 0.60, RiskFactors: []string{"wildfire", "hail"}},
	"NE": {AvgPremium: 3885, RatePercent: 1.95, RiskFactors: []string{"tornado", "hail"}, Notes: "Very high hail risk"},
	"NV": {AvgPremium: 1194, RatePercent: 0.32, RiskFactors: []string{"wildfire"}},
	"NH": {AvgPremium: 1198, RatePercent: 0.35, RiskFactors: []string{"winter_storm"}},
	"NJ": {AvgPremium: 1516, RatePercent: 0.35, RiskFactors: []string{"hurricane", "flood"}},
	"NM": {AvgPremium: 1801, RatePercent: 0.75, RiskFactors: []string{"wildfire", "hail"}},
	"NY": {AvgPremium: 1702, RatePercent: 0.45, RiskFactors: []string{"winter_storm"}},
	"NC": {AvgPremium: 1729, RatePercent: 0.60, RiskFactors: []string{"hurricane"}},
	"ND": {AvgPremium: 2320, RatePercent: 1.00, RiskFactors: []string{"hail", "tornado"}},
	"OH": {AvgPremium: 1281, RatePercent: 0.65, RiskFactors: []string{"tornado"}},
	"OK": {AvgPremium: 4043, RatePercent: 2.40, RiskFactors: []string{"tornado", "hail"}, Notes: "Third highest - Tornado Alley"},
	"OR": {AvgPremium: 1011, RatePercent: 0.25, RiskFactors: []string{"wildfire", "earthquake"}},
	"PA": {AvgPremium: 1257, RatePercent: 0.55, RiskFactors: []string{"winter_storm"}},
	"RI": {AvgPremium: 2084, RatePercent: 0.55, RiskFactors: []string{"hurricane"}},
	"SC": {AvgPremium: 2119, RatePercent: 0.95, RiskFactors: []string{"hurricane"}},
	"SD": {AvgPremium: 2568, RatePercent: 1.15, RiskFactors: []string{"hail", "tornado"}},
	"TN": {AvgPremium: 2056, RatePercent: 0.85, RiskFactors: []string{"tornado"}},
	"TX": {AvgPremium: 3875, RatePercent: 1.40, RiskFactors: []string{"hurricane", "hail", "tornado"}, Notes: "High due to multiple perils"},
	"UT": {AvgPremium: 1032, RatePercent: 0.25, RiskFactors: []string{"wildfire"}},
	"VT": {AvgPremium: 1059, RatePercent: 0.35, RiskFactors: []string{"winter_storm"}},
	"VA": {AvgPremium: 1534, RatePercent: 0.45, RiskFactors: []string{"hurricane"}},
	"WA": {AvgPremium: 1285, RatePercent: 0.25, RiskFactors: []string{"earthquake", "wildfire"}},
	"WV": {AvgPremium: 1286, RatePercent: 0.90, RiskFactors: []string{"flood"}},
	"WI": {AvgPremium: 1246, RatePercent: 0.55, RiskFactors: []string{"winter_storm", "hail"}},
	"WY": {AvgPremium: 1874, RatePercent: 0.65, RiskFactors: []string{"hail", "wildfire"}},
	"DC": {AvgPremium: 1389, RatePercent: 0.25, RiskFactors: []string{}},
}
