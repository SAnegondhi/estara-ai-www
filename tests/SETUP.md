# Integration Tests - Complete Setup Guide

This guide covers everything you need to run integration tests locally or in CI.

## ✅ Quick Start (Docker - Recommended)

**Fastest way to run tests:**

```bash
# 1. Start test infrastructure
docker compose -f docker-compose.test.yml up -d

# 2. Run tests
make test-integration

# 3. Stop infrastructure (optional)
docker compose -f docker-compose.test.yml down
```

**Or use the automated script:**

```bash
./tests/scripts/run-tests-docker.sh
```

## 📋 What's Included

### Test Infrastructure
- ✅ **PostgreSQL 15** on port 5433 (Docker)
- ✅ **Redis 7** on port 6380 (Docker)
- ✅ **Automatic database creation** (estara_test, estara_market_test)
- ✅ **Test helpers** (SetupTestEnv, CreateTestUser, MakeRequest, etc.)

### Test Coverage (29+ endpoints)
- ✅ **Auth** (8 test functions) - Registration, login, refresh, password, OAuth, passkeys
- ✅ **Portfolio** (6 test functions) - CRUD + authorization + isolation
- ✅ **Discovery** (4 test suites) - Search, quota, sessions, decision memos
- ✅ **Market Data** (8 endpoints) - Rates, economics, demographics, labor
- ✅ **AI** (3 endpoints) - Chat sessions, analysis history, investment planning

### Documentation
- ✅ **README.md** - Comprehensive test guide (366 lines)
- ✅ **SETUP.md** - This file
- ✅ **ADR-084** - Architecture decision record
- ✅ **.env.test.example** - Full configuration template (100+ variables)

### CI/CD
- ✅ **GitHub Actions** - Automated tests on push/PR
- ✅ **Makefile targets** - test, test-integration, test-unit, test-coverage

## 🐳 Docker Setup Details

### Files
```
docker-compose.test.yml       # PostgreSQL + Redis services
tests/scripts/
  ├── init-test-dbs.sh        # Automatic database creation
  └── run-tests-docker.sh     # Automated test runner
.env.test.example             # Environment template
.env.test                     # Your local config (gitignored)
```

### Ports
- **PostgreSQL**: 5433 (host) → 5432 (container)
- **Redis**: 6380 (host) → 6379 (container)

Non-conflicting with development databases (5432, 6379).

### Container Commands

```bash
# Start services
docker compose -f docker-compose.test.yml up -d

# View logs
docker compose -f docker-compose.test.yml logs -f

# Check status
docker compose -f docker-compose.test.yml ps

# Stop (preserves data)
docker compose -f docker-compose.test.yml stop

# Clean slate (removes volumes)
docker compose -f docker-compose.test.yml down -v

# Connect to PostgreSQL
docker exec -it estara-postgres-test psql -U postgres -d estara_test

# Connect to Redis
docker exec -it estara-redis-test redis-cli
```

## 🔧 Manual Setup (Alternative)

If you prefer to use your own PostgreSQL installation:

### 1. Create Databases
```bash
createdb estara_test
createdb estara_market_test
```

### 2. Set Environment Variables
```bash
cp .env.test.example .env.test
# Edit .env.test with your database URLs
```

### 3. Run Tests
```bash
make test-integration
```

## 🧪 Running Tests

### All Integration Tests
```bash
make test-integration
```

### Specific Test Suite
```bash
go test -v ./tests/integration -run TestAuthEndpoints
go test -v ./tests/integration -run TestPortfolioEndpoints
go test -v ./tests/integration -run TestDiscoverEndpoints
```

### Specific Test Case
```bash
go test -v ./tests/integration -run TestAuthEndpoints/Register/valid_registration
```

### With Race Detection
```bash
go test -race ./tests/integration
```

### With Coverage Report
```bash
make test-coverage
# Opens coverage.html in browser
```

## 🔒 Environment Variables

### Minimal (Required)
```bash
DATABASE_URL=postgresql://postgres:postgres@localhost:5433/estara_test?sslmode=disable
MARKET_DATABASE_URL=postgresql://postgres:postgres@localhost:5433/estara_market_test?sslmode=disable
REDIS_URL=redis://localhost:6380/1
CLIENT_JWT_SECRET=test-jwt-secret
AUTH_SECRET=test-auth-secret
AUTH_TRUST_HOST=true
```

### Full Configuration
See `.env.test.example` for 100+ available variables including:
- External API keys (HasData, SchoolDigger, FRED, etc.)
- Payment providers (Stripe, Apple IAP)
- OAuth providers (Google, Microsoft, Apple)
- Storage (Cloudflare R2)
- Email (Mailjet)

## 📊 CI/CD Integration

### GitHub Actions
Tests run automatically on:
- Push to master, main, or adr-083-store-migration branches
- Pull requests to master or main

Workflow: `.github/workflows/integration-tests.yml`

### What CI Does
1. Starts PostgreSQL + Redis services
2. Creates test databases
3. Runs sqlc generate
4. Builds all packages
5. Runs integration tests
6. Uploads coverage reports

## 🐛 Troubleshooting

### "database doesn't exist"
```bash
# Docker: Restart containers (auto-creates databases)
docker compose -f docker-compose.test.yml down -v
docker compose -f docker-compose.test.yml up -d

# Manual: Create databases
createdb estara_test
createdb estara_market_test
```

### "connection refused" (PostgreSQL)
```bash
# Check if container is running
docker compose -f docker-compose.test.yml ps

# Check logs
docker compose -f docker-compose.test.yml logs postgres-test

# Verify port
lsof -i :5433
```

### "connection refused" (Redis)
```bash
# Check if container is running
docker compose -f docker-compose.test.yml ps

# Check logs
docker compose -f docker-compose.test.yml logs redis-test

# Verify port
lsof -i :6380
```

### "column does not exist"
```bash
# Regenerate sqlc
sqlc generate
go build ./...
```

### Tests fail randomly
- Check for missing cleanup (defer functions)
- Check for hardcoded IDs
- Use -race flag to detect race conditions
- Use -count=1 to disable test caching

## 📚 Additional Resources

- **ADR-084**: Architecture decision record for integration testing
- **ADR-083**: Store migration (what these tests validate)
- **tests/README.md**: Comprehensive test documentation
- **Makefile**: Build targets and CI checks

## 🎯 Test Patterns

### Table-Driven Tests
See `auth_test.go`, `portfolio_test.go` for examples.

### Authorization Tests
See `portfolio_test.go` - TestPortfolioAuthorization for multi-user isolation tests.

### Error Cases
All test suites include negative test cases (invalid input, missing auth, etc.).

## ✨ Contributing

When adding new tests:
1. Follow table-driven test pattern
2. Include both success and error cases
3. Test authorization boundaries
4. Clean up test data with defer
5. Update README.md if adding new test categories

## 📝 Notes

- Test databases are isolated from development databases
- All tests use real HTTP requests (not mocked)
- Tests can run in parallel safely
- No migrations needed (tests work with sqlc-generated schema)
- `.env.test` is gitignored (never commit credentials)
