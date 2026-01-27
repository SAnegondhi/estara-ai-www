package finder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/estara-ai/www/internal/services/property/providers"
)

// MockProvider implements providers.Provider for testing
type MockProvider struct {
	name        string
	enabled     bool
	priority    int
	searchFunc  func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error)
	getFunc     func(ctx context.Context, propertyID string) (*providers.Property, error)
	healthFunc  func(ctx context.Context) error
}

func (m *MockProvider) Name() string { return m.name }
func (m *MockProvider) IsEnabled() bool { return m.enabled }
func (m *MockProvider) Priority() int { return m.priority }

func (m *MockProvider) Search(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, params)
	}
	return &providers.SearchResult{
		Properties: []providers.Property{
			{ID: "test-1", Address: "123 Test St", City: params.City, State: params.State},
		},
		Total:    1,
		Provider: m.name,
	}, nil
}

func (m *MockProvider) GetProperty(ctx context.Context, propertyID string) (*providers.Property, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, propertyID)
	}
	return &providers.Property{ID: propertyID}, nil
}

func (m *MockProvider) HealthCheck(ctx context.Context) error {
	if m.healthFunc != nil {
		return m.healthFunc(ctx)
	}
	return nil
}

func TestNormalizeString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Austin", "austin"},
		{"TEXAS", "texas"},
		{"New York", "newyork"},
		{"San Francisco", "sanfrancisco"},
		{"Austin, TX", "austintx"},
		{"123 Main St", "123mainst"},
		{"Über-city", "bercity"},
		{"", ""},
		{"  spaces  ", "spaces"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildCacheKey(t *testing.T) {
	o := &Orchestrator{}

	tests := []struct {
		name     string
		params   providers.SearchParams
		contains []string
	}{
		{
			name: "basic location",
			params: providers.SearchParams{
				City:  "Austin",
				State: "TX",
			},
			contains: []string{"properties:", "austin", "tx"},
		},
		{
			name: "with price filters",
			params: providers.SearchParams{
				City:     "Austin",
				State:    "TX",
				MinPrice: 100000,
				MaxPrice: 500000,
			},
			contains: []string{"minp100000", "maxp500000"},
		},
		{
			name: "with beds filter",
			params: providers.SearchParams{
				City:    "Austin",
				State:   "TX",
				MinBeds: 3,
			},
			contains: []string{"minb3"},
		},
		{
			name: "with property type",
			params: providers.SearchParams{
				City:         "Austin",
				State:        "TX",
				PropertyType: providers.PropertyTypeSingleFamily,
			},
			contains: []string{"typesingle_family"},
		},
		{
			name: "with pagination",
			params: providers.SearchParams{
				City:   "Austin",
				State:  "TX",
				Limit:  25,
				Offset: 50,
			},
			contains: []string{"lim25", "off50"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := o.buildCacheKey(tt.params)
			for _, substr := range tt.contains {
				assert.Contains(t, key, substr)
			}
		})
	}
}

func TestNewOrchestrator(t *testing.T) {
	provider1 := &MockProvider{name: "provider1", enabled: true, priority: 1}
	provider2 := &MockProvider{name: "provider2", enabled: true, priority: 2}

	o := NewOrchestrator(OrchestratorConfig{
		Providers:          []providers.Provider{provider2, provider1}, // Wrong order
		PriorityOrder:      []string{"provider1", "provider2"},
		ConcurrentFallback: false,
	})

	assert.NotNil(t, o)
	assert.Len(t, o.providers, 2)
	assert.False(t, o.concurrentFallback)

	// Check priority order
	statuses := o.GetProviderStatus()
	assert.Equal(t, "provider1", statuses[0].Name)
	assert.Equal(t, "provider2", statuses[1].Name)
}

func TestSearch_Success(t *testing.T) {
	provider := &MockProvider{
		name:     "test-provider",
		enabled:  true,
		priority: 1,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			return &providers.SearchResult{
				Properties: []providers.Property{
					{ID: "prop-1", Address: "123 Test St", City: params.City, State: params.State, Price: 300000},
					{ID: "prop-2", Address: "456 Test Ave", City: params.City, State: params.State, Price: 400000},
				},
				Total:    2,
				Provider: "test-provider",
			}, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider},
	})

	result, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	require.NoError(t, err)
	assert.Len(t, result.Properties, 2)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, "test-provider", result.Metrics.ProviderUsed)
	assert.False(t, result.Metrics.CacheHit)
}

func TestSearch_DefaultLimit(t *testing.T) {
	var capturedParams providers.SearchParams

	provider := &MockProvider{
		name:     "test-provider",
		enabled:  true,
		priority: 1,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			capturedParams = params
			return &providers.SearchResult{
				Properties: []providers.Property{},
				Provider:   "test-provider",
			}, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider},
	})

	_, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
		// No limit specified
	})

	require.NoError(t, err)
	assert.Equal(t, defaultSearchLimit, capturedParams.Limit)
}

func TestSearch_NoProviders(t *testing.T) {
	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{},
	})

	_, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no property providers available")
}

func TestSearch_AllProvidersFail(t *testing.T) {
	provider1 := &MockProvider{
		name:     "provider1",
		enabled:  true,
		priority: 1,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			return nil, errors.New("provider1 error")
		},
	}

	provider2 := &MockProvider{
		name:     "provider2",
		enabled:  true,
		priority: 2,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			return nil, errors.New("provider2 error")
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider1, provider2},
	})

	_, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all providers failed")
}

func TestSearch_FallbackOnFirstFailure(t *testing.T) {
	provider1 := &MockProvider{
		name:     "failing-provider",
		enabled:  true,
		priority: 1,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			return nil, errors.New("provider failed")
		},
	}

	provider2 := &MockProvider{
		name:     "working-provider",
		enabled:  true,
		priority: 2,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			return &providers.SearchResult{
				Properties: []providers.Property{{ID: "fallback-prop"}},
				Total:      1,
				Provider:   "working-provider",
			}, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider1, provider2},
	})

	result, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	require.NoError(t, err)
	assert.Equal(t, "working-provider", result.Metrics.ProviderUsed)
	assert.Contains(t, result.Metrics.ProvidersAttempted, "failing-provider")
	assert.Contains(t, result.Metrics.ProvidersAttempted, "working-provider")
}

func TestSearch_DisabledProviderSkipped(t *testing.T) {
	disabledProvider := &MockProvider{
		name:     "disabled-provider",
		enabled:  false, // Disabled
		priority: 1,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			t.Error("disabled provider should not be called")
			return nil, nil
		},
	}

	enabledProvider := &MockProvider{
		name:     "enabled-provider",
		enabled:  true,
		priority: 2,
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{disabledProvider, enabledProvider},
	})

	result, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	require.NoError(t, err)
	assert.Equal(t, "enabled-provider", result.Metrics.ProviderUsed)
}

func TestGetProperty_SpecificProvider(t *testing.T) {
	provider1 := &MockProvider{
		name:    "provider1",
		enabled: true,
		getFunc: func(ctx context.Context, propertyID string) (*providers.Property, error) {
			return &providers.Property{ID: propertyID, ProviderName: "provider1"}, nil
		},
	}

	provider2 := &MockProvider{
		name:    "provider2",
		enabled: true,
		getFunc: func(ctx context.Context, propertyID string) (*providers.Property, error) {
			t.Error("should not call provider2 when provider1 is specified")
			return nil, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider1, provider2},
	})

	property, err := o.GetProperty(context.Background(), "provider1", "prop-123")

	require.NoError(t, err)
	assert.Equal(t, "prop-123", property.ID)
	assert.Equal(t, "provider1", property.ProviderName)
}

func TestGetProperty_FallbackToAll(t *testing.T) {
	provider := &MockProvider{
		name:    "test-provider",
		enabled: true,
		getFunc: func(ctx context.Context, propertyID string) (*providers.Property, error) {
			return &providers.Property{ID: propertyID}, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider},
	})

	property, err := o.GetProperty(context.Background(), "unknown-provider", "prop-123")

	require.NoError(t, err)
	assert.Equal(t, "prop-123", property.ID)
}

func TestGetProperty_NotFound(t *testing.T) {
	provider := &MockProvider{
		name:    "test-provider",
		enabled: true,
		getFunc: func(ctx context.Context, propertyID string) (*providers.Property, error) {
			return nil, errors.New("not found")
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider},
	})

	_, err := o.GetProperty(context.Background(), "", "nonexistent")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "property not found")
}

func TestHealthCheck(t *testing.T) {
	healthyProvider := &MockProvider{
		name:    "healthy",
		enabled: true,
		healthFunc: func(ctx context.Context) error {
			return nil
		},
	}

	unhealthyProvider := &MockProvider{
		name:    "unhealthy",
		enabled: true,
		healthFunc: func(ctx context.Context) error {
			return errors.New("connection failed")
		},
	}

	disabledProvider := &MockProvider{
		name:    "disabled",
		enabled: false,
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{healthyProvider, unhealthyProvider, disabledProvider},
	})

	results := o.HealthCheck(context.Background())

	assert.NoError(t, results["healthy"])
	assert.Error(t, results["unhealthy"])
	assert.Error(t, results["disabled"])
	assert.Contains(t, results["disabled"].Error(), "disabled")
}

func TestGetProviderStatus(t *testing.T) {
	provider1 := &MockProvider{name: "provider1", enabled: true, priority: 1}
	provider2 := &MockProvider{name: "provider2", enabled: false, priority: 2}
	provider3 := &MockProvider{name: "provider3", enabled: true, priority: 3}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider3, provider1, provider2}, // Out of order
	})

	statuses := o.GetProviderStatus()

	assert.Len(t, statuses, 3)
	// Should be sorted by priority
	assert.Equal(t, "provider1", statuses[0].Name)
	assert.True(t, statuses[0].Enabled)
	assert.Equal(t, "provider2", statuses[1].Name)
	assert.False(t, statuses[1].Enabled)
	assert.Equal(t, "provider3", statuses[2].Name)
	assert.True(t, statuses[2].Enabled)
}

func TestAddProvider(t *testing.T) {
	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{},
	})

	assert.Len(t, o.providers, 0)

	newProvider := &MockProvider{name: "new-provider", enabled: true}
	o.AddProvider(newProvider)

	assert.Len(t, o.providers, 1)
	assert.Equal(t, "new-provider", o.providers[0].Name())
}

func TestInvalidateCache_NilCache(t *testing.T) {
	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{},
		Cache:     nil,
	})

	// Should not error when cache is nil
	err := o.InvalidateCache(context.Background(), "Austin", "TX")
	assert.NoError(t, err)
}

func TestSearchConcurrent(t *testing.T) {
	callCount := 0
	successProvider := &MockProvider{
		name:     "success-provider",
		enabled:  true,
		priority: 1,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			callCount++
			return &providers.SearchResult{
				Properties: []providers.Property{{ID: "concurrent-prop"}},
				Provider:   "success-provider",
			}, nil
		},
	}

	slowProvider := &MockProvider{
		name:     "slow-provider",
		enabled:  true,
		priority: 2,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			time.Sleep(100 * time.Millisecond)
			return &providers.SearchResult{
				Provider: "slow-provider",
			}, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers:          []providers.Provider{successProvider, slowProvider},
		ConcurrentFallback: true,
	})

	result, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	require.NoError(t, err)
	// First success should win
	assert.Equal(t, "success-provider", result.Metrics.ProviderUsed)
}

func TestSearchMetrics(t *testing.T) {
	provider := &MockProvider{
		name:    "test-provider",
		enabled: true,
		searchFunc: func(ctx context.Context, params providers.SearchParams) (*providers.SearchResult, error) {
			return &providers.SearchResult{
				Properties: []providers.Property{{ID: "1"}, {ID: "2"}, {ID: "3"}},
				Total:      100,
				HasMore:    true,
				NextOffset: 3,
				Provider:   "test-provider",
				SearchTime: 50 * time.Millisecond,
			}, nil
		},
	}

	o := NewOrchestrator(OrchestratorConfig{
		Providers: []providers.Provider{provider},
	})

	result, err := o.Search(context.Background(), providers.SearchParams{
		City:  "Austin",
		State: "TX",
	})

	require.NoError(t, err)
	assert.Equal(t, 3, result.Metrics.ResultCount)
	assert.Equal(t, "test-provider", result.Metrics.ProviderUsed)
	assert.Contains(t, result.Metrics.ProvidersAttempted, "test-provider")
	assert.False(t, result.Metrics.CacheHit)
	assert.NotZero(t, result.Metrics.TotalTime)
	assert.True(t, result.HasMore)
	assert.Equal(t, 3, result.NextOffset)
	assert.Equal(t, 100, result.Total)
}
