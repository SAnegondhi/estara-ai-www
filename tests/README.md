# Integration Tests

Comprehensive API integration tests for the Estara AI www server.

## Overview

These tests verify **real API endpoints** by making actual HTTP requests to the Go server with all services running (database, Redis, property finder, AI services, etc.).

## Test Coverage

### Authentication (`auth_test.go`)
- ✅ User registration (valid/invalid/duplicate)
- ✅ Login (valid/invalid credentials)
- ✅ Token refresh (valid/invalid tokens)
- ✅ `/api/auth/me` endpoint (authenticated/unauthenticated)
- ✅ Password update
- ✅ Logout
- ✅ OAuth login error cases
- ✅ Passkey endpoints (WebAuthn)

### Portfolio (`portfolio_test.go`)
- ✅ Create properties (valid/invalid/unauthenticated)
- ✅ List properties (pagination, filtering)
- ✅ Get property by ID (valid/nonexistent)
- ✅ Update property (full/partial updates)
- ✅ Delete property
- ✅ Portfolio metrics calculation
- ✅ **Authorization**: Users can only access their own properties
- ✅ **Isolation**: User 1 cannot see User 2's properties

### Discovery (`discover_test.go`)
- ✅ Property search (valid/invalid parameters)
- ✅ Quota endpoints (searches, evaluations, investments)
- ✅ Discovery sessions (create, list, get, archive, restore)
- ✅ Decision memo history
- ✅ Location autocomplete (public endpoint)
- ✅ Location validation (authenticated)

### Market Data (`discover_test.go`)
- ✅ Mortgage rates
- ✅ Investment rates (T-Bills, I-Bonds)
- ✅ Economic indicators (inflation, unemployment)
- ✅ Demographics by city/state
- ✅ Labor market data (national + state)
- ✅ Unified economics endpoint
- ✅ Aggregated market data

### AI Endpoints (`discover_test.go`)
- ✅ Evaluation chat sessions (list with pagination)
- ✅ Market analysis history
- ✅ Investment planning history

### Admin Endpoints (`admin_test.go`)
- ✅ User management (list, get, activity)
- ✅ Cache management (stats, invalidate)
- ✅ AI metrics (metrics, usage, cache status)
- ✅ Analytics (analytics, audit log)
- ✅ Subscriptions (list subscriptions)
- ✅ Vendor management (list, costs)
- ✅ Revenue analytics (summary, trend, by-tier, at-risk, leakage, chargeback-rate)
- ✅ **Authorization**: Only admin users can access admin endpoints
- ✅ **Forbidden**: Regular users get 403 when accessing admin endpoints

### Cron Job Endpoints (`cron_test.go`)
- ✅ Authentication (X-Cron-Secret header validation)
- ✅ General cron jobs (16 endpoints)
- ✅ Market data status
- ⏭️  Market data imports (skipped - require external dependencies)
- ✅ Cron job tracking system

## Setup

### Docker Setup (Recommended)

**Easiest way to run tests with isolated databases:**

```bash
# 1. Start test infrastructure (PostgreSQL + Redis)
docker compose -f docker-compose.test.yml up -d

# 2. Copy environment template
cp .env.test.example .env.test

# 3. Run tests
make test-integration

# 4. Stop infrastructure (optional)
docker compose -f docker-compose.test.yml down
```

**Or use the automated script:**
```bash
./tests/scripts/run-tests-docker.sh
```

**What you get:**
- ✅ PostgreSQL 15 on port 5433 (no conflicts with dev DB)
- ✅ Redis 7 on port 6380 (no conflicts with dev Redis)
- ✅ Automatic database creation (`estara_test`, `estara_market_test`)
- ✅ No manual PostgreSQL installation needed
- ✅ Clean state on each restart
- ✅ Same environment across all developers

**Container management:**
```bash
# View logs
docker compose -f docker-compose.test.yml logs -f

# Check status
docker compose -f docker-compose.test.yml ps

# Clean slate (removes volumes)
docker compose -f docker-compose.test.yml down -v

# Connect to PostgreSQL (debugging)
docker exec -it estara-postgres-test psql -U postgres -d estara_test
```

### Manual Setup (Alternative)

If you prefer to use your own PostgreSQL installation:

#### Prerequisites

1. **Test Databases** (separate from development):
   ```bash
   # Create test databases
   createdb estara_test
   createdb estara_market_test
   ```

2. **Environment Variables**:
   ```bash
   # Copy from .env.local and modify for test
   export TEST_DATABASE_URL="postgresql://localhost:5432/estara_test?sslmode=disable"
   export TEST_MARKET_DATABASE_URL="postgresql://localhost:5432/estara_market_test?sslmode=disable"
   export REDIS_URL="redis://localhost:6379/1"  # Use different Redis DB for tests
   export JWT_SECRET="test-secret-key-change-in-production"
   export ANTHROPIC_API_KEY="your-anthropic-key"  # Optional for some tests
   ```

3. **Database Schema**:
   ```bash
   # Run migrations on test database
   DATABASE_URL=$TEST_DATABASE_URL go run cmd/server/main.go migrate
   ```

## Running Tests

### Run All Integration Tests
```bash
make test-integration
```

### Run Specific Test Suite
```bash
# Auth tests only
go test -v ./tests/integration -run TestAuthEndpoints

# Portfolio tests only
go test -v ./tests/integration -run TestPortfolioEndpoints

# Discover tests only
go test -v ./tests/integration -run TestDiscoverEndpoints

# Admin tests only
go test -v ./tests/integration -run TestAdminEndpoints

# Cron tests only
go test -v ./tests/integration -run TestCronEndpoints

# Market data tests only
go test -v ./tests/integration -run TestMarketDataEndpoints
```

### Run Specific Test Case
```bash
# Run only the "valid registration" test
go test -v ./tests/integration -run TestAuthEndpoints/Register/valid_registration

# Run only authorization tests
go test -v ./tests/integration -run TestPortfolioAuthorization
```

### With Verbose Output
```bash
go test -v ./tests/integration -run TestAuthEndpoints -count=1
```

### With Race Detection
```bash
go test -race ./tests/integration
```

## Test Infrastructure

### Test Helpers (`helpers.go`)

**SetupTestEnv(t)**: Creates a full test environment
- Real database connections
- Redis connection (optional)
- All services initialized
- HTTP router configured
- Returns cleanup function

**CreateTestUser(t, env, email, password)**: Creates authenticated test user
- Registers user via API
- Returns user with access/refresh tokens
- Use for authenticated endpoint tests

**MakeRequest(t, env, req)**: Makes HTTP request
- Handles JSON marshaling/unmarshaling
- Adds Authorization header if token provided
- Asserts expected status code
- Returns response body

**RunTestCases(t, env, cases)**: Table-driven test runner
- Runs multiple test cases in subtests
- Validates status codes
- Checks error messages
- Custom validation functions

**CleanupTestData(t, env, userEmail)**: Cleanup after tests
- Deletes user and all related data
- Uses database cascading deletes
- Called via defer in tests

## Test Patterns

### Table-Driven Tests
```go
cases := []TestCase{
    {
        Name: "valid input",
        Request: Request{
            Method:      "POST",
            Path:        "/api/endpoint",
            AccessToken: user.AccessToken,
            Body:        validData,
        },
        WantStatus: http.StatusOK,
        Validate: func(t *testing.T, body []byte) {
            var resp ResponseType
            ParseJSON(t, body, &resp)
            assert.Equal(t, expected, resp.Field)
        },
    },
    {
        Name: "invalid input",
        Request: Request{
            Method: "POST",
            Path:   "/api/endpoint",
            Body:   invalidData,
        },
        WantStatus: http.StatusBadRequest,
        WantError:  "expected error substring",
    },
}
RunTestCases(t, env, cases)
```

### Authorization Tests
```go
func TestAuthorization(t *testing.T) {
    env := SetupTestEnv(t)
    defer env.Cleanup()

    // Create two users
    user1 := CreateTestUser(t, env, "user1@example.com", "pass123")
    user2 := CreateTestUser(t, env, "user2@example.com", "pass123")

    // User1 creates resource
    resource := CreateResource(t, env, user1)

    // User2 cannot access User1's resource
    MakeRequest(t, env, Request{
        Method:      "GET",
        Path:        "/api/resource/" + resource.ID,
        AccessToken: user2.AccessToken,
        WantStatus:  http.StatusNotFound, // 404, not 403 for security
    })
}
```

## Extending Tests

### Adding New Test File

1. Create `tests/integration/newfeature_test.go`
2. Follow existing patterns
3. Import test helpers
4. Use table-driven tests
5. Clean up test data

### Adding New Test Case

```go
func TestNewFeature(t *testing.T) {
    env := SetupTestEnv(t)
    defer env.Cleanup()

    email := RandomEmail()
    password := RandomPassword()
    defer CleanupTestData(t, env, email)

    user := CreateTestUser(t, env, email, password)

    // Your test logic here
    body := MakeRequest(t, env, Request{
        Method:      "POST",
        Path:        "/api/new-feature",
        AccessToken: user.AccessToken,
        Body:        testData,
        WantStatus:  http.StatusOK,
    })

    var resp YourResponseType
    ParseJSON(t, body, &resp)
    assert.Equal(t, expected, resp.Field)
}
```

## CI Integration

### GitHub Actions
```yaml
- name: Run integration tests
  env:
    TEST_DATABASE_URL: postgresql://postgres:postgres@localhost/estara_test
    TEST_MARKET_DATABASE_URL: postgresql://postgres:postgres@localhost/estara_market_test
    REDIS_URL: redis://localhost:6379/1
  run: make test-integration
```

### Pre-commit Hook
```bash
#!/bin/bash
# .git/hooks/pre-commit
make test-integration || exit 1
```

## Debugging Tests

### View Full HTTP Request/Response
```go
// Add to test temporarily
fmt.Printf("Request: %+v\n", req)
fmt.Printf("Response: %s\n", string(body))
```

### Run Single Test with Verbose Logging
```bash
LOG_LEVEL=debug go test -v ./tests/integration -run TestSpecificTest -count=1
```

### Check Test Database State
```bash
psql $TEST_DATABASE_URL -c "SELECT * FROM users;"
```

## Best Practices

✅ **DO**:
- Use separate test databases
- Clean up test data with defer
- Use table-driven tests for similar cases
- Test both success and error cases
- Test authorization boundaries
- Use meaningful test names
- Validate response structure

❌ **DON'T**:
- Use production database for tests
- Skip cleanup (causes test pollution)
- Hardcode user IDs or test data
- Test only happy paths
- Share test data between tests
- Use time.Sleep() - use proper sync

## Troubleshooting

### "database doesn't exist"
```bash
createdb estara_test
DATABASE_URL=$TEST_DATABASE_URL go run cmd/server/main.go migrate
```

### "connection refused" (Redis)
```bash
# Install and start Redis
brew install redis
brew services start redis
```

### "column does not exist"
```bash
# Regenerate sqlc and rebuild
sqlc generate
go build ./...
```

### Tests fail randomly
- Check for missing cleanup
- Check for hardcoded IDs
- Use -race flag to detect race conditions
- Use -count=1 to disable test caching

## Reporting

### Quick Summary (No Test Execution)
```bash
./tests/scripts/test-summary.sh
```

### Comprehensive Test Report
```bash
make test-report
```
Generates:
- JSON test output
- HTML coverage report
- Test summary with statistics
- Timestamped reports for history

### Coverage Report Only
```bash
make test-coverage
open coverage.html
```

**📖 See [REPORTING.md](REPORTING.md) for detailed reporting guide**

## Maintenance

### Update Test Data
- Update `CreateTestUser()` in helpers.go
- Update `CreateTestPortfolioProperty()` in helpers.go
- Keep test data realistic but minimal

### Update After Schema Changes
1. Run migrations on test database
2. Regenerate sqlc: `sqlc generate`
3. Update affected tests
4. Run full test suite

### Performance
- Tests should complete in < 10 seconds
- Use connection pooling
- Reuse test environment when possible
- Clean up only necessary data
