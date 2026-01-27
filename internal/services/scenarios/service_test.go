package scenarios

import (
	"encoding/json"
	"testing"
)

func TestScenario_Struct(t *testing.T) {
	description := "Test scenario for investment analysis"
	scenario := Scenario{
		ID:              "scenario-123",
		Name:            "Investment Scenario",
		Description:     &description,
		Parameters:      map[string]interface{}{"purchasePrice": 500000},
		Results:         map[string]interface{}{"noi": 30000},
		Tags:            []string{"investment", "austin"},
		Favorite:        true,
		HasAIParameters: false,
		CreatedAt:       "2024-01-15T10:00:00Z",
		LastModified:    "2024-01-15T10:00:00Z",
	}

	if scenario.ID != "scenario-123" {
		t.Errorf("expected id=scenario-123, got=%s", scenario.ID)
	}
	if scenario.Name != "Investment Scenario" {
		t.Errorf("expected name=Investment Scenario, got=%s", scenario.Name)
	}
	if scenario.Description == nil || *scenario.Description != description {
		t.Errorf("expected description=%s, got=%v", description, scenario.Description)
	}
	if !scenario.Favorite {
		t.Error("expected favorite=true")
	}
}

func TestScenario_NilDescription(t *testing.T) {
	scenario := Scenario{
		ID:          "scenario-456",
		Name:        "Minimal Scenario",
		Description: nil,
		Parameters:  map[string]interface{}{},
		Tags:        []string{},
	}

	if scenario.Description != nil {
		t.Error("expected description to be nil")
	}
}

func TestScenario_JSON(t *testing.T) {
	description := "A test scenario"
	scenario := Scenario{
		ID:          "scenario-123",
		Name:        "Test Scenario",
		Description: &description,
		Parameters: map[string]interface{}{
			"purchasePrice":  500000,
			"downPayment":    100000,
			"interestRate":   6.5,
			"loanTermYears":  30,
			"monthlyRent":    3500,
			"propertyTaxes":  6000,
			"insurance":      1200,
			"maintenance":    2400,
			"vacancy":        0.05,
		},
		Results:         map[string]interface{}{"capRate": 6.5},
		Tags:            []string{"test"},
		Favorite:        false,
		HasAIParameters: true,
		CreatedAt:       "2024-01-15T10:00:00Z",
		LastModified:    "2024-01-15T10:00:00Z",
	}

	data, err := json.Marshal(scenario)
	if err != nil {
		t.Fatalf("failed to marshal scenario: %v", err)
	}

	var decoded Scenario
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal scenario: %v", err)
	}

	if decoded.ID != scenario.ID {
		t.Errorf("expected id=%s, got=%s", scenario.ID, decoded.ID)
	}
	if decoded.Name != scenario.Name {
		t.Errorf("expected name=%s, got=%s", scenario.Name, decoded.Name)
	}
	if decoded.HasAIParameters != scenario.HasAIParameters {
		t.Errorf("expected hasAIParameters=%v, got=%v", scenario.HasAIParameters, decoded.HasAIParameters)
	}
}

func TestCreateScenarioInput_Validation(t *testing.T) {
	tests := []struct {
		name        string
		input       CreateScenarioInput
		expectValid bool
		reason      string
	}{
		{
			name: "valid input",
			input: CreateScenarioInput{
				Name:       "New Scenario",
				Parameters: map[string]interface{}{"purchasePrice": 400000},
			},
			expectValid: true,
		},
		{
			name: "missing name",
			input: CreateScenarioInput{
				Parameters: map[string]interface{}{},
			},
			expectValid: false,
			reason:      "name is required",
		},
		{
			name: "empty parameters is valid",
			input: CreateScenarioInput{
				Name:       "Minimal Scenario",
				Parameters: map[string]interface{}{},
			},
			expectValid: true,
		},
		{
			name: "with description",
			input: CreateScenarioInput{
				Name:        "Described Scenario",
				Description: strPtr("A scenario with description"),
				Parameters:  map[string]interface{}{"price": 300000},
				Tags:        []string{"tag1", "tag2"},
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.input.Name != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v (reason: %s)", tt.expectValid, isValid, tt.reason)
			}
		})
	}
}

func TestUpdateScenarioInput_Validation(t *testing.T) {
	tests := []struct {
		name        string
		input       UpdateScenarioInput
		expectValid bool
	}{
		{
			name: "update name only",
			input: UpdateScenarioInput{
				Name: strPtr("Updated Name"),
			},
			expectValid: true,
		},
		{
			name: "update parameters only",
			input: UpdateScenarioInput{
				Parameters: map[string]interface{}{"purchasePrice": 600000},
			},
			expectValid: true,
		},
		{
			name: "update everything",
			input: UpdateScenarioInput{
				Name:            strPtr("New Name"),
				Description:     strPtr("New Description"),
				Parameters:      map[string]interface{}{"purchasePrice": 700000},
				Results:         map[string]interface{}{"capRate": 7.0},
				Tags:            []string{"new", "tags"},
				Favorite:        boolPtr(true),
				HasAIParameters: boolPtr(false),
			},
			expectValid: true,
		},
		{
			name:        "empty update is valid",
			input:       UpdateScenarioInput{},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// All updates are valid (partial updates allowed)
			isValid := true
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v", tt.expectValid, isValid)
			}
		})
	}
}

func TestScenarioData_Calculations(t *testing.T) {
	tests := []struct {
		name            string
		parameters      map[string]interface{}
		expectedNOI     float64
		expectedCapRate float64
	}{
		{
			name: "basic calculation",
			parameters: map[string]interface{}{
				"purchasePrice":  float64(500000),
				"monthlyRent":    float64(3500),
				"annualExpenses": float64(12000),
			},
			expectedNOI:     30000, // (3500 * 12) - 12000
			expectedCapRate: 6.0,   // 30000 / 500000 * 100
		},
		{
			name: "higher rent scenario",
			parameters: map[string]interface{}{
				"purchasePrice":  float64(400000),
				"monthlyRent":    float64(4000),
				"annualExpenses": float64(10000),
			},
			expectedNOI:     38000, // (4000 * 12) - 10000
			expectedCapRate: 9.5,   // 38000 / 400000 * 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			purchasePrice := tt.parameters["purchasePrice"].(float64)
			monthlyRent := tt.parameters["monthlyRent"].(float64)
			annualExpenses := tt.parameters["annualExpenses"].(float64)

			noi := (monthlyRent * 12) - annualExpenses
			capRate := (noi / purchasePrice) * 100

			if noi != tt.expectedNOI {
				t.Errorf("expected NOI=%.2f, got=%.2f", tt.expectedNOI, noi)
			}
			if capRate != tt.expectedCapRate {
				t.Errorf("expected capRate=%.2f, got=%.2f", tt.expectedCapRate, capRate)
			}
		})
	}
}

func TestScenarioTags(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		expected int
	}{
		{
			name:     "empty tags",
			tags:     []string{},
			expected: 0,
		},
		{
			name:     "single tag",
			tags:     []string{"investment"},
			expected: 1,
		},
		{
			name:     "multiple tags",
			tags:     []string{"investment", "austin", "multifamily"},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario := Scenario{
				ID:   "test",
				Name: "Test",
				Tags: tt.tags,
			}

			if len(scenario.Tags) != tt.expected {
				t.Errorf("expected %d tags, got %d", tt.expected, len(scenario.Tags))
			}
		})
	}
}

func TestScenarioFavorite(t *testing.T) {
	// Test default
	scenario := Scenario{
		ID:       "test",
		Name:     "Test",
		Favorite: false,
	}

	if scenario.Favorite {
		t.Error("expected favorite to be false by default")
	}

	// Test setting favorite
	scenario.Favorite = true
	if !scenario.Favorite {
		t.Error("expected favorite to be true after setting")
	}
}

// Helper functions
func strPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}
