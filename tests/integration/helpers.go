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
	"time"

	"github.com/estara-ai/www/internal/api"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
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

// CreateTestUser creates a user directly in the database and returns auth tokens
func CreateTestUser(t *testing.T, env *TestEnv, email, password string) *TestUser {
	ctx := context.Background()

	// Create a Store from the DB
	store := dbstore.NewStore(env.DB)

	// Generate unique user ID
	userID := fmt.Sprintf("test-user-%d", time.Now().UnixNano())

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err, "failed to hash password")

	// Create user in database using sqlc
	user, err := store.Q().CreateUserWithPassword(ctx, queries.CreateUserWithPasswordParams{
		ID:       userID,
		Email:    email,
		Password: pgtype.Text{String: string(hashedPassword), Valid: true},
		FirstName: pgtype.Text{String: "", Valid: false},
		LastName: pgtype.Text{String: "", Valid: false},
		Role:             "USER",
		SubscriptionTier: pgtype.Text{String: "free", Valid: true},
		StripeCustomerId: pgtype.Text{String: "", Valid: false},
	})
	require.NoError(t, err, "failed to create user")

	// Create V2 quota for the user (required for login)
	quotaID := fmt.Sprintf("quota-%d", time.Now().UnixNano())
	periodStart := time.Now()
	periodEnd := periodStart.AddDate(1, 0, 0)

	_, err = store.Q().UpsertV2EvaluationQuota(ctx, queries.UpsertV2EvaluationQuotaParams{
		ID:              quotaID,
		UserID:          userID,
		Column3:         "V2_ANNUAL_ACCESS",
		AnnualLimit:     500,
		UsedThisPeriod:  0,
		PeriodStartDate: pgtype.Timestamp{Time: periodStart, Valid: true},
		PeriodEndDate:   pgtype.Timestamp{Time: periodEnd, Valid: true},
	})
	require.NoError(t, err, "failed to create V2 quota")

	// Now log in to get tokens
	loginBody := map[string]string{
		"email":    email,
		"password": password,
	}
	respBody := MakeRequest(t, env, Request{
		Method: "POST",
		Path:   "/api/auth/login",
		Body:   loginBody,
	})

	var loginResp struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
	}
	err = json.Unmarshal(respBody, &loginResp)
	require.NoError(t, err, "failed to parse login response")

	return &TestUser{
		ID:           user.ID,
		Email:        user.Email,
		Password:     password,
		AccessToken:  loginResp.Token,
		RefreshToken: loginResp.RefreshToken,
	}
}

// CreateTestAdminUser creates an admin user and returns auth tokens
func CreateTestAdminUser(t *testing.T, env *TestEnv, email, password string) *TestUser {
	ctx := context.Background()

	// Create a Store from the DB
	store := dbstore.NewStore(env.DB)

	// Generate user ID
	userID := fmt.Sprintf("test-admin-%d", time.Now().UnixNano())

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err, "failed to hash password")

	// Create admin user in database using sqlc
	user, err := store.Q().CreateUserWithPassword(ctx, queries.CreateUserWithPasswordParams{
		ID:       userID,
		Email:    email,
		Password: pgtype.Text{String: string(hashedPassword), Valid: true},
		FirstName: pgtype.Text{String: "", Valid: false},
		LastName: pgtype.Text{String: "", Valid: false},
		Role:             "ADMIN",  // Create as admin directly
		SubscriptionTier: pgtype.Text{String: "free", Valid: true},
		StripeCustomerId: pgtype.Text{String: "", Valid: false},
	})
	require.NoError(t, err, "failed to create admin user")

	// Create V2 quota for the admin user (required for login)
	quotaID := fmt.Sprintf("quota-%d", time.Now().UnixNano())
	periodStart := time.Now()
	periodEnd := periodStart.AddDate(1, 0, 0)

	_, err = store.Q().UpsertV2EvaluationQuota(ctx, queries.UpsertV2EvaluationQuotaParams{
		ID:              quotaID,
		UserID:          userID,
		Column3:         "V2_ANNUAL_ACCESS",
		AnnualLimit:     500,
		UsedThisPeriod:  0,
		PeriodStartDate: pgtype.Timestamp{Time: periodStart, Valid: true},
		PeriodEndDate:   pgtype.Timestamp{Time: periodEnd, Valid: true},
	})
	require.NoError(t, err, "failed to create V2 quota")

	// Now log in to get tokens
	loginBody := map[string]string{
		"email":    email,
		"password": password,
	}
	respBody := MakeRequest(t, env, Request{
		Method: "POST",
		Path:   "/api/auth/login",
		Body:   loginBody,
	})

	var loginResp struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
	}
	err = json.Unmarshal(respBody, &loginResp)
	require.NoError(t, err, "failed to parse login response")

	return &TestUser{
		ID:           user.ID,
		Email:        user.Email,
		Password:     password,
		AccessToken:  loginResp.Token,
		RefreshToken: loginResp.RefreshToken,
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

	// Get user ID first
	_, err := env.DB.Main.Exec(ctx, `
		SELECT id FROM users WHERE email = $1
	`, userEmail)
	if err != nil {
		// User doesn't exist, nothing to cleanup
		return
	}

	// Delete audit logs first (they reference users table)
	_, _ = env.DB.Main.Exec(ctx, `
		DELETE FROM audit_logs WHERE "userId" IN (SELECT id FROM users WHERE email = $1)
	`, userEmail)

	// Delete V2 evaluation quota
	_, _ = env.DB.Main.Exec(ctx, `
		DELETE FROM v2_evaluation_quota WHERE "userId" IN (SELECT id FROM users WHERE email = $1)
	`, userEmail)

	// Now delete the user
	_, err = env.DB.Main.Exec(ctx, `
		DELETE FROM users WHERE email = $1
	`, userEmail)
	if err != nil {
		t.Logf("Warning: failed to cleanup test user: %v", err)
	}
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
		WantStatus:  http.StatusCreated,
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
	// Use nanosecond timestamp to ensure unique emails across tests
	return fmt.Sprintf("test-%d@example.com", time.Now().UnixNano())
}

// RandomPassword generates a random test password
func RandomPassword() string {
	return "TestPass123!"
}
