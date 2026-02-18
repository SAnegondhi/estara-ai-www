# Test Reporting Guide

Comprehensive guide to test reporting and analytics for the integration test suite.

## Quick Summary

```bash
# Quick test overview (no test execution)
./tests/scripts/test-summary.sh

# Full test report (runs all tests, generates reports)
make test-report

# Coverage report only
make test-coverage
```

## Available Reports

### 1. Quick Summary (Instant)

**Command**: `./tests/scripts/test-summary.sh`

**What it provides:**
- Test file count and statistics
- Test function and subtest counts
- Lines of code per test file
- Total test coverage overview
- Quick command reference

**Example Output:**
```
📊 Integration Test Suite Summary
==================================

Test Files:
-----------
  ✅ admin_test.go
      Test Functions: 1
      Subtests: 17
      Lines of Code: 372

  ✅ auth_test.go
      Test Functions: 2
      Subtests: 10
      Lines of Code: 459

  ... (more files)

Summary:
--------
  Total Test Files: 5
  Total Test Functions: 11
  Total Subtests: 52
  Total Test Code: 1896 lines
```

**Use cases:**
- Quick overview before making changes
- Documentation updates
- Code review statistics
- No test execution required

### 2. Comprehensive Test Report

**Command**: `make test-report`

**What it provides:**
- Full test execution results
- JSON output for CI/CD integration
- HTML coverage report
- Test summary with pass/fail/skip counts
- Coverage percentage
- Timestamped reports for history

**Generated Files:**
```
tests/reports/
├── test-output-TIMESTAMP.json    # Full JSON test output
├── coverage-TIMESTAMP.out        # Coverage data file
├── coverage-TIMESTAMP.html       # Interactive coverage report
├── summary-TIMESTAMP.txt         # Human-readable summary
├── latest-output.json            # Symlink to latest JSON
├── latest-coverage.html          # Symlink to latest coverage
└── latest-summary.txt            # Symlink to latest summary
```

**Example Summary:**
```
Integration Test Report
=======================
Generated: 2026-02-17 14:30:45

Test Results
------------
✅ Passed:  45
❌ Failed:  0
⏭️  Skipped: 3
📊 Coverage: 68.5%

Test Suites
-----------
- Authentication Tests (auth_test.go)
- Portfolio Tests (portfolio_test.go)
- Discovery Tests (discover_test.go)
- Market Data Tests (discover_test.go)
- AI Endpoint Tests (discover_test.go)
- Admin Endpoint Tests (admin_test.go)
- Cron Job Tests (cron_test.go)
```

**Use cases:**
- Pre-deployment validation
- CI/CD pipeline integration
- Historical tracking
- Coverage analysis
- Detailed debugging

### 3. Coverage Report Only

**Command**: `make test-coverage`

**What it provides:**
- HTML coverage visualization
- Per-file coverage statistics
- Line-by-line coverage highlighting
- Interactive drill-down

**Output:**
- `coverage.out` - Coverage data
- `coverage.html` - Interactive HTML report

**View report:**
```bash
make test-coverage
open coverage.html
```

**Use cases:**
- Finding uncovered code paths
- Improving test coverage
- Code review insights

### 4. Verbose Test Output

**Command**: `go test -v ./tests/integration`

**What it provides:**
- Real-time test execution logs
- Detailed test names and results
- Server logs and HTTP requests
- Error messages and stack traces

**Use cases:**
- Debugging failing tests
- Understanding test flow
- Development and troubleshooting

### 5. JSON Output for CI/CD

**Command**: `go test -json ./tests/integration`

**What it provides:**
- Machine-readable test results
- Structured event stream
- Parseable by CI/CD tools
- Integration with test reporters

**Example Integration:**
```yaml
# GitHub Actions
- name: Run tests
  run: go test -json ./tests/integration > test-results.json

- name: Publish test results
  uses: EnricoMi/publish-unit-test-result-action@v2
  with:
    json_test_results: test-results.json
```

**Use cases:**
- CI/CD pipeline integration
- Test result aggregation
- Automated reporting
- Failure notifications

## CI/CD Integration

### GitHub Actions

The test suite is integrated with GitHub Actions (`.github/workflows/integration-tests.yml`):

```yaml
- name: Run integration tests
  run: make test-integration
  env:
    TEST_DATABASE_URL: postgresql://postgres:postgres@localhost/estara_test
    TEST_MARKET_DATABASE_URL: postgresql://postgres:postgres@localhost/estara_market_test
    REDIS_URL: redis://localhost:6379/1
```

**Features:**
- Automated test execution on push/PR
- PostgreSQL + Redis services
- Test result publishing
- Coverage reporting
- Failure notifications

### Report Retention

Reports are timestamped and preserved:
```
tests/reports/
├── test-output-2026-02-17_14-30-45.json
├── coverage-2026-02-17_14-30-45.html
├── summary-2026-02-17_14-30-45.txt
├── latest-* (symlinks to most recent)
```

**Benefits:**
- Historical comparison
- Regression tracking
- Trend analysis
- Audit trail

## Test Metrics

### Current Statistics

From latest test run:

| Metric | Value |
|--------|-------|
| Test Files | 5 |
| Test Functions | 11 |
| Subtests | 52 |
| Total Test Code | 1,896 lines |
| Helper Functions | 11 |
| Helper Code | 321 lines |
| **Total** | **2,217 lines** |

### Test Coverage by Category

| Category | Test File | Endpoints | Status |
|----------|-----------|-----------|--------|
| Authentication | `auth_test.go` | 8 | ✅ Complete |
| Portfolio | `portfolio_test.go` | 6 | ✅ Complete |
| Discovery | `discover_test.go` | 4 suites | ✅ Complete |
| Market Data | `discover_test.go` | 8 | ✅ Complete |
| AI Endpoints | `discover_test.go` | 3 | ✅ Complete |
| Admin Endpoints | `admin_test.go` | 19 | ✅ Complete |
| Cron Jobs | `cron_test.go` | 19 | ✅ Complete |
| **Total** | **5 files** | **67+ endpoints** | **✅ Complete** |

## Best Practices

### Before Committing

```bash
# 1. Run quick summary
./tests/scripts/test-summary.sh

# 2. Run full tests
make test-integration

# 3. Check coverage
make test-coverage
```

### Before Deploying

```bash
# Generate comprehensive report
make test-report

# Review summary
cat tests/reports/latest-summary.txt

# Review coverage
open tests/reports/latest-coverage.html

# Ensure all tests pass
# Ensure coverage meets threshold (e.g., >80%)
```

### Continuous Monitoring

```bash
# Automated script
while true; do
    make test-report
    sleep 3600  # Run every hour
done
```

## Troubleshooting

### No Reports Generated

**Problem**: `make test-report` doesn't create files

**Solutions:**
```bash
# Ensure test infrastructure is running
docker compose -f docker-compose.test.yml up -d

# Ensure .env.test exists
cp .env.test.example .env.test

# Run manually
./tests/scripts/test-report.sh
```

### Coverage Percentage is 0%

**Problem**: Coverage shows 0% or "N/A"

**Cause**: Tests not covering production code (only test files)

**Solution**: This is expected for integration tests that primarily test HTTP handlers

### JSON Parse Errors

**Problem**: `jq` errors when parsing JSON output

**Cause**: Test output mixed with server logs

**Solution**: Filter JSON events only:
```bash
cat test-output.json | grep '^{' | jq .
```

## Future Enhancements

**Planned additions:**
- Performance benchmarks in reports
- Trend analysis across test runs
- Automated test health monitoring
- Integration with external reporting tools
- Slack/email notifications for failures

## References

- **Tests**: `tests/integration/`
- **Documentation**: `tests/README.md`
- **Setup Guide**: `tests/SETUP.md`
- **ADR-084**: Integration testing decision
- **CI Workflow**: `.github/workflows/integration-tests.yml`
