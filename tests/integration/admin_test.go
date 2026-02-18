package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	// Create regular user
	userEmail := RandomEmail()
	userPassword := RandomPassword()
	defer CleanupTestData(t, env, userEmail)
	regularUser := CreateTestUser(t, env, userEmail, userPassword)

	// Create admin user
	adminEmail := RandomEmail()
	adminPassword := RandomPassword()
	defer CleanupTestData(t, env, adminEmail)
	adminUser := CreateTestAdminUser(t, env, adminEmail, adminPassword)

	t.Run("UserManagement", testAdminUserManagement(env, adminUser, regularUser))
	t.Run("CacheManagement", testAdminCacheManagement(env, adminUser, regularUser))
	t.Run("AIMetrics", testAdminAIMetrics(env, adminUser, regularUser))
	t.Run("Analytics", testAdminAnalytics(env, adminUser, regularUser))
	t.Run("Subscriptions", testAdminSubscriptions(env, adminUser, regularUser))
}

func testAdminUserManagement(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("ListUsers", func(t *testing.T) {
			cases := []TestCase{
				{
					Name: "admin can list users",
					Request: Request{
						Method:      "GET",
						Path:        "/api/admin/users",
						AccessToken: adminUser.AccessToken,
					},
					WantStatus: http.StatusOK,
					Validate: func(t *testing.T, body []byte) {
						// Response shape: {"success": true, "data": {"users": [...], "pagination": {...}}}
						var resp struct {
							Data struct {
								Users []map[string]interface{} `json:"users"`
							} `json:"data"`
						}
						ParseJSON(t, body, &resp)
						require.NotNil(t, resp.Data.Users)
						// Should have at least the admin and regular user
						assert.GreaterOrEqual(t, len(resp.Data.Users), 2)
					},
				},
				{
					Name: "regular user cannot list users",
					Request: Request{
						Method:      "GET",
						Path:        "/api/admin/users",
						AccessToken: regularUser.AccessToken,
					},
					WantStatus: http.StatusForbidden,
				},
				{
					Name: "unauthenticated cannot list users",
					Request: Request{
						Method: "GET",
						Path:   "/api/admin/users",
					},
					WantStatus: http.StatusUnauthorized,
				},
			}
			RunTestCases(t, env, cases)
		})

		t.Run("GetUser", func(t *testing.T) {
			// Admin can get regular user's details
			// Response shape: {"success": true, "data": {user fields...}}
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/users/" + regularUser.ID,
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp struct {
				Data map[string]interface{} `json:"data"`
			}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp.Data)
			assert.Equal(t, regularUser.ID, resp.Data["id"])
			assert.Equal(t, regularUser.Email, resp.Data["email"])

			// Regular user cannot access admin endpoint
			MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/users/" + regularUser.ID,
				AccessToken: regularUser.AccessToken,
				WantStatus:  http.StatusForbidden,
			})
		})

		t.Run("GetUserActivity", func(t *testing.T) {
			// Admin can get user activity
			// Response shape: {"success": true, "data": {"entries": [...], "pagination": {...}}}
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/users/" + regularUser.ID + "/activity",
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp struct {
				Data struct {
					Entries []interface{} `json:"entries"`
				} `json:"data"`
			}
			ParseJSON(t, body, &resp)
			// entries may be empty for a fresh user, just ensure the key exists
			assert.NotNil(t, resp.Data.Entries)
		})
	}
}

func testAdminCacheManagement(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("CacheStats", func(t *testing.T) {
			cases := []TestCase{
				{
					Name: "admin can view cache stats",
					Request: Request{
						Method:      "GET",
						Path:        "/api/admin/cache/stats",
						AccessToken: adminUser.AccessToken,
					},
					WantStatus: http.StatusOK,
					Validate: func(t *testing.T, body []byte) {
						var resp map[string]interface{}
						ParseJSON(t, body, &resp)
						require.NotNil(t, resp)
					},
				},
				{
					Name: "regular user cannot view cache stats",
					Request: Request{
						Method:      "GET",
						Path:        "/api/admin/cache/stats",
						AccessToken: regularUser.AccessToken,
					},
					WantStatus: http.StatusForbidden,
				},
			}
			RunTestCases(t, env, cases)
		})

		t.Run("InvalidateCache", func(t *testing.T) {
			// Admin can invalidate cache (strategy is required: all|expired|type|user|key)
			MakeRequest(t, env, Request{
				Method:      "DELETE",
				Path:        "/api/admin/cache/invalidate",
				AccessToken: adminUser.AccessToken,
				Body: map[string]interface{}{
					"strategy": "all",
				},
				WantStatus: http.StatusOK,
			})

			// Regular user cannot invalidate cache
			MakeRequest(t, env, Request{
				Method:      "DELETE",
				Path:        "/api/admin/cache/invalidate",
				AccessToken: regularUser.AccessToken,
				Body: map[string]interface{}{
					"strategy": "all",
				},
				WantStatus: http.StatusForbidden,
			})
		})
	}
}

func testAdminAIMetrics(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		endpoints := []struct {
			name string
			path string
		}{
			{"AIMetrics", "/api/admin/ai-metrics"},
			{"AIUsage", "/api/admin/ai-usage"},
			{"AICacheStatus", "/api/admin/ai-cache-status"},
		}

		for _, endpoint := range endpoints {
			t.Run(endpoint.name, func(t *testing.T) {
				// Admin can access
				body := MakeRequest(t, env, Request{
					Method:      "GET",
					Path:        endpoint.path,
					AccessToken: adminUser.AccessToken,
					WantStatus:  http.StatusOK,
				})

				var resp map[string]interface{}
				ParseJSON(t, body, &resp)
				require.NotNil(t, resp)

				// Regular user cannot access
				MakeRequest(t, env, Request{
					Method:      "GET",
					Path:        endpoint.path,
					AccessToken: regularUser.AccessToken,
					WantStatus:  http.StatusForbidden,
				})
			})
		}
	}
}

func testAdminAnalytics(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("GetAnalytics", func(t *testing.T) {
			// Admin can view analytics
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/analytics",
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp map[string]interface{}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp)

			// Regular user cannot view analytics
			MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/analytics",
				AccessToken: regularUser.AccessToken,
				WantStatus:  http.StatusForbidden,
			})
		})

		t.Run("GetAuditLog", func(t *testing.T) {
			// Admin can view audit log
			// Response shape: {"success": true, "data": {"entries": [...], "pagination": {...}}}
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/audit-log",
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp struct {
				Data struct {
					Entries []interface{} `json:"entries"`
				} `json:"data"`
			}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp.Data.Entries)

			// Regular user cannot view audit log
			MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/audit-log",
				AccessToken: regularUser.AccessToken,
				WantStatus:  http.StatusForbidden,
			})
		})
	}
}

func testAdminSubscriptions(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("ListSubscriptions", func(t *testing.T) {
			// Admin can list subscriptions
			// Response shape: {"success": true, "data": {"subscriptions": [...], "summary": {...}, "pagination": {...}}}
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/subscriptions",
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp struct {
				Data struct {
					Subscriptions []interface{} `json:"subscriptions"`
				} `json:"data"`
			}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp.Data.Subscriptions)

			// Regular user cannot list subscriptions
			MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/subscriptions",
				AccessToken: regularUser.AccessToken,
				WantStatus:  http.StatusForbidden,
			})
		})
	}
}

func testAdminVendorManagement(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("ListVendors", func(t *testing.T) {
			// Admin can list vendors
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/vendors",
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp struct {
				Vendors []interface{} `json:"vendors"`
			}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp.Vendors)
		})

		t.Run("GetVendorCosts", func(t *testing.T) {
			// Admin can view vendor costs
			body := MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/vendors/costs",
				AccessToken: adminUser.AccessToken,
				WantStatus:  http.StatusOK,
			})

			var resp map[string]interface{}
			ParseJSON(t, body, &resp)
			require.NotNil(t, resp)

			// Regular user cannot view vendor costs
			MakeRequest(t, env, Request{
				Method:      "GET",
				Path:        "/api/admin/vendors/costs",
				AccessToken: regularUser.AccessToken,
				WantStatus:  http.StatusForbidden,
			})
		})
	}
}

func testAdminRevenueAnalytics(env *TestEnv, adminUser, regularUser *TestUser) func(t *testing.T) {
	return func(t *testing.T) {
		endpoints := []struct {
			name string
			path string
		}{
			{"RevenueSummary", "/api/admin/revenue/summary"},
			{"RevenueTrend", "/api/admin/revenue/trend"},
			{"RevenueByTier", "/api/admin/revenue/by-tier"},
			{"AtRiskCustomers", "/api/admin/revenue/at-risk"},
			{"RevenueLeakage", "/api/admin/revenue/leakage"},
			{"ChargebackRate", "/api/admin/revenue/chargeback-rate"},
		}

		for _, endpoint := range endpoints {
			t.Run(endpoint.name, func(t *testing.T) {
				// Admin can access
				body := MakeRequest(t, env, Request{
					Method:      "GET",
					Path:        endpoint.path,
					AccessToken: adminUser.AccessToken,
					WantStatus:  http.StatusOK,
				})

				var resp map[string]interface{}
				ParseJSON(t, body, &resp)
				require.NotNil(t, resp)

				// Regular user cannot access
				MakeRequest(t, env, Request{
					Method:      "GET",
					Path:        endpoint.path,
					AccessToken: regularUser.AccessToken,
					WantStatus:  http.StatusForbidden,
				})
			})
		}
	}
}
