package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortfolioEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	email := RandomEmail()
	password := RandomPassword()
	defer CleanupTestData(t, env, email)

	user := CreateTestUser(t, env, email, password)

	t.Run("Create", testPortfolioCreate(env, user))
	t.Run("List", testPortfolioList(env, user))
	t.Run("Get", testPortfolioGet(env, user))
	t.Run("Update", testPortfolioUpdate(env, user))
	t.Run("Delete", testPortfolioDelete(env, user))
	t.Run("Metrics", testPortfolioMetrics(env, user))
}

func testPortfolioCreate(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		cases := []TestCase{
			{
				Name: "valid property",
				Request: Request{
					Method:      "POST",
					Path:        "/api/v2/portfolio",
					AccessToken: user.AccessToken,
					Body: map[string]interface{}{
						"address":       "456 Market St",
						"city":          "Austin",
						"state":         "TX",
						"zipCode":       "78701",
						"purchasePrice": 350000,
						"purchaseDate":  "2021-03-15",
						"bedrooms":      3,
						"bathrooms":     2.5,
						"squareFeet":    1800,
						"monthlyRent":   2500,
						"propertyType":  "single_family",
					},
				},
				WantStatus: http.StatusOK,
				Validate: func(t *testing.T, body []byte) {
					var resp struct {
						Property map[string]interface{} `json:"property"`
					}
					ParseJSON(t, body, &resp)

					require.NotNil(t, resp.Property)
					assert.NotEmpty(t, resp.Property["id"])
					assert.Equal(t, "456 Market St", resp.Property["address"])
					assert.Equal(t, "Austin", resp.Property["city"])
					assert.Equal(t, "TX", resp.Property["state"])
				},
			},
			{
				Name: "missing required fields",
				Request: Request{
					Method:      "POST",
					Path:        "/api/v2/portfolio",
					AccessToken: user.AccessToken,
					Body: map[string]interface{}{
						"address": "789 Test Ave",
					},
				},
				WantStatus: http.StatusBadRequest,
			},
			{
				Name: "unauthenticated",
				Request: Request{
					Method: "POST",
					Path:   "/api/v2/portfolio",
					Body: map[string]interface{}{
						"address": "789 Test Ave",
						"city":    "Dallas",
						"state":   "TX",
					},
				},
				WantStatus: http.StatusUnauthorized,
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testPortfolioList(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		// Create test properties
		prop1 := CreateTestPortfolioProperty(t, env, user)
		prop2Data := map[string]interface{}{
			"address":       "789 Oak St",
			"city":          "Dallas",
			"state":         "TX",
			"zipCode":       "75201",
			"purchasePrice": 280000,
			"purchaseDate":  "2020-06-01",
			"bedrooms":      2,
			"bathrooms":     2,
			"squareFeet":    1200,
		}
		body := MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/v2/portfolio",
			AccessToken: user.AccessToken,
			Body:        prop2Data,
			WantStatus:  http.StatusOK,
		})
		var prop2Resp struct {
			Property map[string]interface{} `json:"property"`
		}
		ParseJSON(t, body, &prop2Resp)

		// List properties
		body = MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var listResp struct {
			Properties []map[string]interface{} `json:"properties"`
		}
		ParseJSON(t, body, &listResp)

		require.Len(t, listResp.Properties, 2)

		// Verify both properties are in the list
		ids := make(map[string]bool)
		for _, prop := range listResp.Properties {
			ids[prop["id"].(string)] = true
		}
		assert.True(t, ids[prop1["id"].(string)])
		assert.True(t, ids[prop2Resp.Property["id"].(string)])
	}
}

func testPortfolioGet(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		prop := CreateTestPortfolioProperty(t, env, user)
		propID := prop["id"].(string)

		cases := []TestCase{
			{
				Name: "valid property ID",
				Request: Request{
					Method:      "GET",
					Path:        "/api/v2/portfolio/" + propID,
					AccessToken: user.AccessToken,
				},
				WantStatus: http.StatusOK,
				Validate: func(t *testing.T, body []byte) {
					var resp struct {
						Property map[string]interface{} `json:"property"`
					}
					ParseJSON(t, body, &resp)

					assert.Equal(t, propID, resp.Property["id"])
					assert.Equal(t, "123 Test St", resp.Property["address"])
				},
			},
			{
				Name: "nonexistent property",
				Request: Request{
					Method:      "GET",
					Path:        "/api/v2/portfolio/nonexistent-id",
					AccessToken: user.AccessToken,
				},
				WantStatus: http.StatusNotFound,
			},
			{
				Name: "unauthenticated",
				Request: Request{
					Method: "GET",
					Path:   "/api/v2/portfolio/" + propID,
				},
				WantStatus: http.StatusUnauthorized,
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testPortfolioUpdate(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		prop := CreateTestPortfolioProperty(t, env, user)
		propID := prop["id"].(string)

		updateData := map[string]interface{}{
			"monthlyRent":      2800,
			"currentValue":     325000,
			"mortgageBalance":  200000,
			"mortgagePayment":  1500,
			"vacancyRate":      5,
		}

		body := MakeRequest(t, env, Request{
			Method:      "PUT",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user.AccessToken,
			Body:        updateData,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Property map[string]interface{} `json:"property"`
		}
		ParseJSON(t, body, &resp)

		assert.Equal(t, propID, resp.Property["id"])
		assert.Equal(t, float64(2800), resp.Property["monthlyRent"])
		assert.Equal(t, float64(325000), resp.Property["currentValue"])

		// Verify update persisted
		body = MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})
		ParseJSON(t, body, &resp)
		assert.Equal(t, float64(2800), resp.Property["monthlyRent"])
	}
}

func testPortfolioDelete(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		prop := CreateTestPortfolioProperty(t, env, user)
		propID := prop["id"].(string)

		// Delete property
		MakeRequest(t, env, Request{
			Method:      "DELETE",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		// Verify it's gone
		MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusNotFound,
		})

		// Verify list doesn't include it
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var listResp struct {
			Properties []map[string]interface{} `json:"properties"`
		}
		ParseJSON(t, body, &listResp)

		for _, p := range listResp.Properties {
			assert.NotEqual(t, propID, p["id"])
		}
	}
}

func testPortfolioMetrics(env *TestEnv, user *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		// Create properties with known values
		CreateTestPortfolioProperty(t, env, user)

		// Get metrics
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio/metrics",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Metrics map[string]interface{} `json:"metrics"`
		}
		ParseJSON(t, body, &resp)

		require.NotNil(t, resp.Metrics)

		// Should have metrics fields
		assert.Contains(t, resp.Metrics, "totalProperties")
		assert.Contains(t, resp.Metrics, "totalValue")

		// Total properties should be >= 1
		totalProps, ok := resp.Metrics["totalProperties"].(float64)
		require.True(t, ok)
		assert.GreaterOrEqual(t, totalProps, float64(1))
	}
}

func TestPortfolioAuthorization(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create two users
	user1Email := RandomEmail()
	user1Password := RandomPassword()
	defer CleanupTestData(t, env, user1Email)
	user1 := CreateTestUser(t, env, user1Email, user1Password)

	user2Email := RandomEmail()
	user2Password := RandomPassword()
	defer CleanupTestData(t, env, user2Email)
	user2 := CreateTestUser(t, env, user2Email, user2Password)

	// User 1 creates a property
	prop := CreateTestPortfolioProperty(t, env, user1)
	propID := prop["id"].(string)

	t.Run("User cannot access another user's property", func(t *testing.T) {
		// User 2 tries to get User 1's property
		MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user2.AccessToken,
			WantStatus:  http.StatusNotFound, // Should get 404, not 403 (security)
		})

		// User 2 tries to update User 1's property
		MakeRequest(t, env, Request{
			Method:      "PUT",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user2.AccessToken,
			Body: map[string]interface{}{
				"monthlyRent": 5000,
			},
			WantStatus: http.StatusNotFound,
		})

		// User 2 tries to delete User 1's property
		MakeRequest(t, env, Request{
			Method:      "DELETE",
			Path:        "/api/v2/portfolio/" + propID,
			AccessToken: user2.AccessToken,
			WantStatus:  http.StatusNotFound,
		})
	})

	t.Run("User can only see their own properties in list", func(t *testing.T) {
		// Create property for user 2
		user2PropData := map[string]interface{}{
			"address":       "999 Private St",
			"city":          "Houston",
			"state":         "TX",
			"zipCode":       "77001",
			"purchasePrice": 400000,
			"purchaseDate":  "2022-01-01",
			"bedrooms":      4,
			"bathrooms":     3,
			"squareFeet":    2200,
		}
		body := MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/v2/portfolio",
			AccessToken: user2.AccessToken,
			Body:        user2PropData,
			WantStatus:  http.StatusOK,
		})
		var user2PropResp struct {
			Property map[string]interface{} `json:"property"`
		}
		ParseJSON(t, body, &user2PropResp)
		user2PropID := user2PropResp.Property["id"].(string)

		// User 2 lists their properties
		body = MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/v2/portfolio",
			AccessToken: user2.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var listResp struct {
			Properties []map[string]interface{} `json:"properties"`
		}
		ParseJSON(t, body, &listResp)

		// Should only see their own property
		require.Len(t, listResp.Properties, 1)
		assert.Equal(t, user2PropID, listResp.Properties[0]["id"])
		assert.NotEqual(t, propID, listResp.Properties[0]["id"])
	})
}
