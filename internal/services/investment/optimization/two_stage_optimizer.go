package optimization

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/estara-ai/www/internal/services/investment"
)

// OptimizableProperty is normalized for two-stage optimization.
type OptimizableProperty struct {
	ID             string
	MarketID       string
	SubmarketID    string
	TypeID         string
	Price          int
	FullPrice      int
	ExpectedReturn float64
	RiskScore      float64
	Latitude       float64
	Longitude      float64
	OriginalIndex  int
}

// OptimizerConfig defines two-stage optimizer parameters.
type OptimizerConfig struct {
	Budget         int
	RiskCap        float64
	RiskProfile    investment.RiskTolerance
	AlphaMarket    float64
	AlphaSubmarket float64
	AlphaType      float64
	AlphaGeo       float64
	EpsilonReturn  float64
	GeoDecayKm     float64
}

// OptimizeResult captures the two-stage optimizer output.
type OptimizeResult struct {
	Selection         []bool
	SelectedIndices   []int
	TotalReturn       float64
	MaxPossibleReturn float64
	TotalInvested     int
	TotalRisk         float64
	PropertyCount     int
	Concentration     investment.ConcentrationMetrics
}

var defaultConfigByRisk = map[investment.RiskTolerance]OptimizerConfig{
	investment.RiskConservative: {
		EpsilonReturn:  0.01,
		AlphaMarket:    0.40,
		AlphaSubmarket: 0.30,
		AlphaType:      0.20,
		AlphaGeo:       0.10,
		GeoDecayKm:     50,
	},
	investment.RiskModerate: {
		EpsilonReturn:  0.025,
		AlphaMarket:    0.40,
		AlphaSubmarket: 0.30,
		AlphaType:      0.20,
		AlphaGeo:       0.10,
		GeoDecayKm:     50,
	},
	investment.RiskAggressive: {
		EpsilonReturn:  0.05,
		AlphaMarket:    0.35,
		AlphaSubmarket: 0.25,
		AlphaType:      0.25,
		AlphaGeo:       0.15,
		GeoDecayKm:     50,
	},
}

// BuildOptimizerConfig builds config using the risk profile defaults.
func BuildOptimizerConfig(budget int, riskTolerance investment.RiskTolerance) OptimizerConfig {
	defaults, ok := defaultConfigByRisk[riskTolerance]
	if !ok {
		defaults = defaultConfigByRisk[investment.RiskModerate]
	}

	riskCapMap := map[investment.RiskTolerance]float64{
		investment.RiskConservative: 3.0,
		investment.RiskModerate:     5.0,
		investment.RiskAggressive:   8.0,
	}

	return OptimizerConfig{
		Budget:         budget,
		RiskCap:        riskCapMap[riskTolerance],
		RiskProfile:    riskTolerance,
		AlphaMarket:    defaults.AlphaMarket,
		AlphaSubmarket: defaults.AlphaSubmarket,
		AlphaType:      defaults.AlphaType,
		AlphaGeo:       defaults.AlphaGeo,
		EpsilonReturn:  defaults.EpsilonReturn,
		GeoDecayKm:     defaults.GeoDecayKm,
	}
}

// OptimizePortfolioTwoStage runs the two-stage optimization.
func OptimizePortfolioTwoStage(properties []OptimizableProperty, config OptimizerConfig) OptimizeResult {
	if len(properties) == 0 {
		return OptimizeResult{
			Selection:       []bool{},
			SelectedIndices: []int{},
			Concentration: investment.ConcentrationMetrics{
				HHIMarket: 0, HHISubmarket: 0, HHIType: 0, GeoCluster: 0, ConcentrationIndex: 0,
			},
		}
	}

	similarity := buildGeoSimilarityMatrix(properties, config.GeoDecayKm)

	stage1Selection, maxReturn := solveStage1(properties, config)
	finalSelection := solveStage2(properties, stage1Selection, maxReturn, similarity, config)

	totalReturn := calculateTotalReturn(finalSelection, properties)
	totalInvested := calculateTotalInvestment(finalSelection, properties)
	totalRisk := calculateTotalRisk(finalSelection, properties)
	concentration := calculateConcentration(finalSelection, properties, similarity, config)

	selectedIndices := make([]int, 0)
	for i, selected := range finalSelection {
		if selected {
			selectedIndices = append(selectedIndices, i)
		}
	}

	return OptimizeResult{
		Selection:         finalSelection,
		SelectedIndices:   selectedIndices,
		TotalReturn:       totalReturn,
		MaxPossibleReturn: maxReturn,
		TotalInvested:     totalInvested,
		TotalRisk:         totalRisk,
		PropertyCount:     len(selectedIndices),
		Concentration:     concentration,
	}
}

func solveStage1(properties []OptimizableProperty, config OptimizerConfig) ([]bool, float64) {
	indices := make([]int, len(properties))
	for i := range properties {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		return properties[indices[i]].ExpectedReturn > properties[indices[j]].ExpectedReturn
	})

	selection := make([]bool, len(properties))
	remainingBudget := config.Budget
	remainingRisk := config.RiskCap
	totalReturn := 0.0

	for _, idx := range indices {
		prop := properties[idx]
		if prop.Price > remainingBudget {
			continue
		}
		if prop.RiskScore > remainingRisk {
			continue
		}
		selection[idx] = true
		remainingBudget -= prop.Price
		remainingRisk -= prop.RiskScore
		totalReturn += prop.ExpectedReturn * float64(prop.Price)
	}

	return selection, totalReturn
}

func solveStage2(
	properties []OptimizableProperty,
	stage1Selection []bool,
	maxReturn float64,
	similarity [][]float64,
	config OptimizerConfig,
) []bool {
	minReturn := (1 - config.EpsilonReturn) * maxReturn
	current := append([]bool{}, stage1Selection...)
	bestConcentration := calculateConcentration(current, properties, similarity, config).ConcentrationIndex

	improved := true
	iterations := 0
	for improved && iterations < 100 {
		improved = false
		iterations++
		for i := range properties {
			if !current[i] {
				continue
			}
			for j := range properties {
				if current[j] {
					continue
				}
				if properties[i].MarketID == properties[j].MarketID {
					continue
				}

				candidate := append([]bool{}, current...)
				candidate[i] = false
				candidate[j] = true

				if !meetsConstraints(candidate, properties, config, minReturn) {
					continue
				}

				newConcentration := calculateConcentration(candidate, properties, similarity, config).ConcentrationIndex
				if newConcentration < bestConcentration {
					current = candidate
					bestConcentration = newConcentration
					improved = true
					break
				}
			}
			if improved {
				break
			}
		}
	}

	return current
}

func meetsConstraints(selection []bool, properties []OptimizableProperty, config OptimizerConfig, minReturn float64) bool {
	return calculateTotalInvestment(selection, properties) <= config.Budget &&
		calculateTotalRisk(selection, properties) <= config.RiskCap &&
		calculateTotalReturn(selection, properties) >= minReturn
}

func calculateTotalReturn(selection []bool, properties []OptimizableProperty) float64 {
	total := 0.0
	for i, selected := range selection {
		if selected {
			total += properties[i].ExpectedReturn * float64(properties[i].Price)
		}
	}
	return total
}

func calculateTotalInvestment(selection []bool, properties []OptimizableProperty) int {
	total := 0
	for i, selected := range selection {
		if selected {
			total += properties[i].Price
		}
	}
	return total
}

func calculateTotalRisk(selection []bool, properties []OptimizableProperty) float64 {
	total := 0.0
	for i, selected := range selection {
		if selected {
			total += properties[i].RiskScore
		}
	}
	return total
}

func calculateConcentration(
	selection []bool,
	properties []OptimizableProperty,
	similarity [][]float64,
	config OptimizerConfig,
) investment.ConcentrationMetrics {
	hhiMarket := calculateHHI(selection, properties, func(p OptimizableProperty) string { return p.MarketID })
	hhiSubmarket := calculateHHI(selection, properties, func(p OptimizableProperty) string { return p.SubmarketID })
	hhiType := calculateHHI(selection, properties, func(p OptimizableProperty) string { return p.TypeID })
	geoCluster := calculateGeoCluster(selection, similarity)

	concentrationIndex := config.AlphaMarket*hhiMarket +
		config.AlphaSubmarket*hhiSubmarket +
		config.AlphaType*hhiType +
		config.AlphaGeo*geoCluster

	return investment.ConcentrationMetrics{
		HHIMarket:          hhiMarket,
		HHISubmarket:       hhiSubmarket,
		HHIType:            hhiType,
		GeoCluster:         geoCluster,
		ConcentrationIndex: concentrationIndex,
	}
}

// CalculateConcentrationMetrics recomputes concentration metrics for a selection.
func CalculateConcentrationMetrics(
	selection []bool,
	properties []OptimizableProperty,
	config OptimizerConfig,
) investment.ConcentrationMetrics {
	similarity := buildGeoSimilarityMatrix(properties, config.GeoDecayKm)
	return calculateConcentration(selection, properties, similarity, config)
}

func calculateHHI(selection []bool, properties []OptimizableProperty, dimension func(OptimizableProperty) string) float64 {
	totalInvested := 0
	categoryInvestment := map[string]int{}
	for i, selected := range selection {
		if !selected {
			continue
		}
		prop := properties[i]
		category := dimension(prop)
		totalInvested += prop.Price
		categoryInvestment[category] += prop.Price
	}
	if totalInvested == 0 {
		return 0
	}

	hhi := 0.0
	for _, investment := range categoryInvestment {
		share := float64(investment) / float64(totalInvested)
		hhi += share * share
	}
	return hhi
}

func buildGeoSimilarityMatrix(properties []OptimizableProperty, decayKm float64) [][]float64 {
	n := len(properties)
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
	}

	for i := 0; i < n; i++ {
		matrix[i][i] = 1
		for j := i + 1; j < n; j++ {
			if properties[i].Latitude == 0 || properties[i].Longitude == 0 ||
				properties[j].Latitude == 0 || properties[j].Longitude == 0 {
				matrix[i][j] = 0
				matrix[j][i] = 0
				continue
			}
			distance := haversineKm(properties[i].Latitude, properties[i].Longitude, properties[j].Latitude, properties[j].Longitude)
			similarity := math.Exp(-math.Pow(distance/decayKm, 2))
			matrix[i][j] = similarity
			matrix[j][i] = similarity
		}
	}
	return matrix
}

func calculateGeoCluster(selection []bool, similarity [][]float64) float64 {
	indices := make([]int, 0)
	for i, selected := range selection {
		if selected {
			indices = append(indices, i)
		}
	}

	if len(indices) <= 1 {
		return 0
	}

	total := 0.0
	pairs := 0
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			total += similarity[indices[i]][indices[j]]
			pairs++
		}
	}

	if pairs == 0 {
		return 0
	}
	return total / float64(pairs)
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func NormalizePropertyType(propertyType string) string {
	if propertyType == "" {
		return "UNKNOWN"
	}
	pt := strings.ToLower(propertyType)
	switch {
	case strings.Contains(pt, "single") || strings.Contains(pt, "house") || pt == "sfh":
		return "SFH"
	case strings.Contains(pt, "condo") || strings.Contains(pt, "apartment"):
		return "CONDO"
	case strings.Contains(pt, "town") || strings.Contains(pt, "attached"):
		return "TOWNHOUSE"
	case strings.Contains(pt, "multi") || strings.Contains(pt, "duplex") || strings.Contains(pt, "triplex") || strings.Contains(pt, "fourplex"):
		return "MULTIFAMILY"
	case strings.Contains(pt, "land") || strings.Contains(pt, "lot"):
		return "LAND"
	default:
		return "OTHER"
	}
}

func MapRiskScore(recommendation investment.Recommendation, overallScore float64) float64 {
	switch recommendation {
	case investment.RecommendationStrongBuy:
		return 0.2
	case investment.RecommendationBuy:
		return 0.35
	case investment.RecommendationHold:
		return 0.6
	case investment.RecommendationPass:
		return 0.8
	default:
		return math.Max(0.2, 1-(overallScore/100))
	}
}

func MapToOptimizableProperties(
	scored []investment.ScoredProperty,
	downPaymentPct float64,
	mortgageRate float64,
	calculator func(investment.Property, float64, float64) investment.PropertyInPortfolio,
) []OptimizableProperty {
	optimizable := make([]OptimizableProperty, 0, len(scored))
	for i, sp := range scored {
		pp := calculator(sp.Property, downPaymentPct, mortgageRate)
		cashOnCash := pp.CashOnCash / 100
		expectedReturn := cashOnCash + 0.03
		price := pp.DownPayment
		if price <= 0 {
			price = int(float64(sp.Property.Price) * downPaymentPct)
		}
		optimizable = append(optimizable, OptimizableProperty{
			ID:             sp.Property.ID,
			MarketID:       fmt.Sprintf("%s, %s", sp.Property.City, sp.Property.State),
			SubmarketID:    sp.Property.ZipCode,
			TypeID:         NormalizePropertyType(sp.Property.PropertyType),
			Price:          price,
			FullPrice:      sp.Property.Price,
			ExpectedReturn: expectedReturn,
			RiskScore:      MapRiskScore(sp.Recommendation, sp.OverallScore),
			Latitude:       sp.Property.Latitude,
			Longitude:      sp.Property.Longitude,
			OriginalIndex:  i,
		})
	}
	return optimizable
}
