package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/estara-ai/www/internal/api"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/stretchr/testify/require"
)

// TestEnv holds the test environment
type TestEnv struct {
	Router   http.Handler
	DB       *postgres.DB
	Redis    *redisClient.Client
	Config   *config.Config
	Services *api.Services
	Cleanup  func()
}

// SetupTestEnv creates a test environment with real database and services
func SetupTestEnv(t *testing.T) *TestEnv {
	ctx := context.Background()

	// Load test configuration from environment
	cfg, err := config.Load()
	require.NoError(t, err, "failed to load test configuration")

	// Override with test database if TEST_DATABASE_URL is set
	if testDB := os.Getenv("TEST_DATABASE_URL"); testDB != "" {
		cfg.Database.URL = testDB
	}
	if testMarketDB := os.Getenv("TEST_MARKET_DATABASE_URL"); testMarketDB != "" {
		cfg.MarketDB.URL = testMarketDB
	}

	// Connect to databases
	db, err := postgres.NewDB(ctx, cfg)
	require.NoError(t, err, "failed to connect to test database")

	// Connect to Redis (optional for tests)
	var redis *redisClient.Client
	if cfg.Redis.URL != "" {
		redis, _ = redisClient.NewClient(ctx, cfg.Redis)
		// Ignore errors - tests can work without Redis
	}

	// Create services
	services, err := api.NewServices(ctx, api.ServiceConfig{
		DB:     db,
		Redis:  redis,
		Config: cfg,
	})
	require.NoError(t, err, "failed to initialize services")

	// Create router
	router := api.NewRouter(ctx, api.RouterConfig{
		Config:   cfg,
		DB:       db,
		Redis:    redis,
		Services: services,
	})

	// Cleanup function
	cleanup := func() {
		services.Close()
		if redis != nil {
			redis.Close()
		}
		db.Close()
	}

	return &TestEnv{
		Router:   router,
		DB:       db,
		Redis:    redis,
		Config:   cfg,
		Services: services,
		Cleanup:  cleanup,
	}
}

// TestUser represents a test user with auth token
type TestUser struct {
	ID           string
	Email        string
	Password     string
	AccessToken  string
	RefreshToken string
}

// CreateTestUser creates a user and returns auth tokens
func CreateTestUser(t *testing.T, env *TestEnv, email, password string) *TestUser {
	// Register user via API
	reqBody := map[string]string{
		"email":    email,
		"password": password,
	}
	respBody := MakeRequest(t, env, Request{
		Method: "POST",
		Path:   "/api/auth/register",
		Body:   reqBody,
	})

	var registerResp struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	err := json.Unmarshal(respBody, &registerResp)
	require.NoError(t, err, "failed to parse register response")

	return &TestUser{
		ID:           registerResp.User.ID,
		Email:        registerResp.User.Email,
		Password:     password,
		AccessToken:  registerResp.AccessToken,
		RefreshToken: registerResp.RefreshToken,
	}
}

// Request represents an HTTP request to make
type Request struct {
	Method      string
	Path        string
	Body        interface{}
	Headers     map[string]string
	AccessToken string
	WantStatus  int // Expected status code (0 = don't check)
}

// MakeRequest makes an HTTP request and returns the response body
func MakeRequest(t *testing.T, env *TestEnv, req Request) []byte {
	var bodyReader io.Reader
	if req.Body != nil {
		bodyBytes, err := json.Marshal(req.Body)
		require.NoError(t, err, "failed to marshal request body")
		bodyReader = bytes.NewReader(bodyBytes)
	}

	httpReq := httptest.NewRequest(req.Method, req.Path, bodyReader)
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Add access token if provided
	if req.AccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	}

	// Add custom headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Make request
	w := httptest.NewRecorder()
	env.Router.ServeHTTP(w, httpReq)

	// Check status if specified
	if req.WantStatus != 0 {
		require.Equal(t, req.WantStatus, w.Code,
			"unexpected status code for %s %s: got %d, want %d\nResponse: %s",
			req.Method, req.Path, w.Code, req.WantStatus, w.Body.String())
	}

	return w.Body.Bytes()
}

// ParseJSON parses JSON response body into target struct
func ParseJSON(t *testing.T, body []byte, target interface{}) {
	err := json.Unmarshal(body, target)
	require.NoError(t, err, "failed to parse JSON response: %s", string(body))
}

// AssertErrorResponse checks that response contains an error
func AssertErrorResponse(t *testing.T, body []byte, wantErrSubstring string) {
	var errResp struct {
		Error string `json:"error"`
	}
	ParseJSON(t, body, &errResp)
	require.Contains(t, errResp.Error, wantErrSubstring,
		"expected error containing %q, got: %s", wantErrSubstring, errResp.Error)
}

// CleanupTestData removes test data from database
func CleanupTestData(t *testing.T, env *TestEnv, userEmail string) {
	ctx := context.Background()

	// Delete user and all related data (cascades via foreign keys)
	_, err := env.DB.Main.Exec(ctx, `
		DELETE FROM users WHERE email = $1
	`, userEmail)
	require.NoError(t, err, "failed to cleanup test user")
}

// CreateTestPortfolioProperty creates a test property
func CreateTestPortfolioProperty(t *testing.T, env *TestEnv, user *TestUser) map[string]interface{} {
	propertyData := map[string]interface{}{
		"address":       "123 Test St",
		"city":          "Austin",
		"state":         "TX",
		"zipCode":       "78701",
		"purchasePrice": 300000,
		"purchaseDate":  "2020-01-15",
		"bedrooms":      3,
		"bathrooms":     2,
		"squareFeet":    1500,
	}

	body := MakeRequest(t, env, Request{
		Method:      "POST",
		Path:        "/api/v2/portfolio",
		Body:        propertyData,
		AccessToken: user.AccessToken,
		WantStatus:  http.StatusOK,
	})

	var resp struct {
		Property map[string]interface{} `json:"property"`
	}
	ParseJSON(t, body, &resp)

	return resp.Property
}

// TestCases helper for table-driven tests
type TestCase struct {
	Name       string
	Request    Request
	WantStatus int
	WantError  string // If set, checks error message contains this
	Validate   func(t *testing.T, body []byte) // Custom validation
}

// RunTestCases runs table-driven test cases
func RunTestCases(t *testing.T, env *TestEnv, cases []TestCase) {
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			tc.Request.WantStatus = tc.WantStatus
			body := MakeRequest(t, env, tc.Request)

			if tc.WantError != "" {
				AssertErrorResponse(t, body, tc.WantError)
			}

			if tc.Validate != nil {
				tc.Validate(t, body)
			}
		})
	}
}

// RandomEmail generates a random test email
func RandomEmail() string {
	return fmt.Sprintf("test-%d@example.com", os.Getpid())
}

// RandomPassword generates a random test password
func RandomPassword() string {
	return "TestPass123!"
}
