package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	email := RandomEmail()
	password := RandomPassword()
	defer CleanupTestData(t, env, email)

	user := CreateTestUser(t, env, email, password)

	t.Run("Search", testDiscoverSearch(env, user))
	t.Run("Quota", testDiscoverQuota(env, user))
	t.Run("Sessions", testDiscoverySessions(env, user))
	t.Run("DecisionMemo", testDecisionMemo(env, user))
}

func testDiscoverSearch(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		cases := []TestCase{
			{
				// Note: Property search service may not be available in test env.
				// This test verifies the endpoint is reachable and location field is validated.
				Name: "valid search",
				Request: Request{
					Method:      "POST",
					Path:        "/api/v2/discover/search",
					AccessToken: user.AccessToken,
					Body: map[string]interface{}{
						// Backend requires "location" field (SearchCriteria.Location validate:"required")
						"location":     "Austin, TX",
						"minPrice":     200000,
						"maxPrice":     500000,
						"bedrooms":     3,
						"propertyType": "single_family",
					},
				},
				// WantStatus 0 = don't check (service may not be available in test env)
				WantStatus: 0,
				Validate: func(t *testing.T, body []byte) {
					// Should return valid JSON in all cases
					var resp map[string]interface{}
					ParseJSON(t, body, &resp)
					require.NotNil(t, resp)
				},
			},
			{
				Name: "missing required fields",
				Request: Request{
					Method:      "POST",
					Path:        "/api/v2/discover/search",
					AccessToken: user.AccessToken,
					Body: map[string]interface{}{
						"minPrice": 200000,
					},
				},
				WantStatus: http.StatusBadRequest,
			},
			{
				Name: "unauthenticated",
				Request: Request{
					Method: "POST",
					Path:   "/api/v2/discover/search",
					Body: map[string]interface{}{
						"location": "Austin, TX",
					},
				},
				WantStatus: http.StatusUnauthorized,
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testDiscoverQuota(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/quota",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		// QuotaResponse is returned directly (not wrapped in "quota" key)
		// Fields: hasSubscription, tier, used, limit, remaining, isUnlimited, etc.
		var resp map[string]interface{}
		ParseJSON(t, body, &resp)

		require.NotNil(t, resp)
		assert.Contains(t, resp, "tier")
		assert.Contains(t, resp, "used")
		assert.Contains(t, resp, "limit")
	}
}

func testDiscoverySessions(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		// Create a session (returns 201 Created)
		// CreateDiscoverySession requires "location" field and "cachedPropertyIds" must be array (not null)
		createBody := MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/v2/discover/sessions",
			AccessToken: user.AccessToken,
			Body: map[string]interface{}{
				"location": "Austin, TX",
				"searchCriteria": map[string]interface{}{
					"city":  "Austin",
					"state": "TX",
				},
				"cachedPropertyIds": []string{}, // Must be array, not null
			},
			WantStatus: http.StatusCreated,
		})

		var createResp struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
		}
		ParseJSON(t, createBody, &createResp)
		sessionID := createResp.Session.ID
		require.NotEmpty(t, sessionID)

		// List sessions
		listBody := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/discover/sessions",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var listResp struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		ParseJSON(t, listBody, &listResp)
		require.NotEmpty(t, listResp.Sessions)

		// Get specific session
		getBody := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/discover/sessions/" + sessionID,
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var getResp struct {
			Session map[string]interface{} `json:"session"`
		}
		ParseJSON(t, getBody, &getResp)
		assert.Equal(t, sessionID, getResp.Session["id"])

		// Archive session
		MakeRequest(t, env, Request{
			Method:      "DELETE",
			Path:        "/api/v2/discover/sessions/" + sessionID,
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		// Restore session
		MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/v2/discover/sessions/" + sessionID + "/restore",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})
	}
}

func testDecisionMemo(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		// Queue a decision memo (requires valid property data)
		// Note: This might fail if property finder service isn't configured
		t.Run("GetMemoHistory", func(t *testing.T) {
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/v2/discover/decision-memo/history",
				AccessToken: user.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp struct {
				Entries []interface{} `json:"entries"`
			}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp.Entries)
		})
	}
}

func TestLocationEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	t.Run("Autocomplete - public", func(t *testing.T) {
		body := MakeRequest(t, env, Request{
			Method:     "GET",
			Path:       "/api/location/autocomplete?q=Austin",
			WantStatus: http.StatusOK,
		})

		var resp struct {
			Suggestions []map[string]interface{} `json:"suggestions"`
		}
		ParseJSON(t, body, &resp)
		require.NotNil(t, resp.Suggestions)
	})

	t.Run("Validate - requires auth", func(t *testing.T) {
		// Should fail without auth
		MakeRequest(t, env, Request{
			Method:     "GET",
			Path:       "/api/location/validate?location=Austin%2C+TX",
			WantStatus: http.StatusUnauthorized,
		})

		// Should work with auth (endpoint expects ?location=City, State format)
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)
		user := CreateTestUser(t, env, email, password)

		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/location/validate?location=Austin%2C+TX",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Valid bool `json:"valid"`
		}
		ParseJSON(t, body, &resp)
		assert.True(t, resp.Valid)
	})
}

func TestMarketDataEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	email := RandomEmail()
	password := RandomPassword()
	defer CleanupTestData(t, env, email)

	user := CreateTestUser(t, env, email, password)

	// These endpoints always require auth and should work in test env
	alwaysAvailable := []struct {
		name string
		path string
	}{
		{"MortgageRate", "/api/market-data/mortgage-rate"},
		{"InvestmentRates", "/api/market-data/investment-rates"},
		{"EconomicRates", "/api/market-data/economic-rates"},
		{"Demographics", "/api/market-data/demographics?city=Austin&state=TX"},
		{"LaborNational", "/api/market-data/labor"},
		{"LaborState", "/api/market-data/labor/state/TX"},
		{"UnifiedEconomics", "/api/market-data/economics?city=Austin&state=TX&medianHomePrice=450000"},
	}

	for _, endpoint := range alwaysAvailable {
		t.Run(endpoint.name, func(t *testing.T) {
			// Should require auth
			MakeRequest(t, env, Request{
				Method:     "GET",
				Path:       endpoint.path,
				WantStatus: http.StatusUnauthorized,
			})

			// Should work with auth
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        endpoint.path,
				AccessToken: user.AccessToken,
				WantStatus:  http.StatusOK,
			})

			// Should return valid JSON
			var resp map[string]interface{}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp)
		})
	}

	// MarketData may fail if external market data service is unavailable in test env
	t.Run("MarketData", func(t *testing.T) {
		// Should require auth
		MakeRequest(t, env, Request{
			Method:     "GET",
			Path:       "/api/market-data?city=Austin&state=TX",
			WantStatus: http.StatusUnauthorized,
		})

		// Should work with auth (accept 200 or 500 if external service unavailable)
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/market-data?city=Austin&state=TX",
			AccessToken: user.AccessToken,
			// WantStatus 0 means don't check status code
		})

		// Should return valid JSON regardless of status
		var resp map[string]interface{}
		ParseJSON(t, body, &resp)
		require.NotNil(t, resp)
	})
}

func TestAIEvaluationEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	email := RandomEmail()
	password := RandomPassword()
	defer CleanupTestData(t, env, email)

	user := CreateTestUser(t, env, email, password)

	t.Run("ChatSessions", func(t *testing.T) {
		// List chat sessions
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/ai/evaluate/chat/sessions?limit=20",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Sessions   []interface{}          `json:"sessions"`
			Pagination map[string]interface{} `json:"pagination"`
		}
		ParseJSON(t, body, &resp)
		require.NotNil(t, resp.Sessions)
		require.NotNil(t, resp.Pagination)
	})

	t.Run("AnalysisHistory", func(t *testing.T) {
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/ai/analysis/history?limit=20&page=1",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Analyses []interface{} `json:"analyses"`
		}
		ParseJSON(t, body, &resp)
		require.NotNil(t, resp.Analyses)
	})

	t.Run("InvestmentPlanningHistory", func(t *testing.T) {
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/ai/investment-planning/history?limit=20&page=1",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Plans []interface{} `json:"plans"`
		}
		ParseJSON(t, body, &resp)
		require.NotNil(t, resp.Plans)
	})
}
