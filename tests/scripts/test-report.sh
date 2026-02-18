#!/bin/bash
set -e

# Integration Test Report Generator
# Runs integration tests and generates detailed reports

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
REPORT_DIR="$PROJECT_ROOT/tests/reports"
TIMESTAMP=$(date +"%Y-%m-%d_%H-%M-%S")

echo "🧪 Integration Test Report Generator"
echo "===================================="
echo ""

# Create reports directory
mkdir -p "$REPORT_DIR"

# Load environment variables
if [ -f "$PROJECT_ROOT/.env.test" ]; then
    export $(cat "$PROJECT_ROOT/.env.test" | grep -v '^#' | xargs)
else
    echo "⚠️  .env.test not found, using default values"
fi

# Run tests with JSON output
echo "📊 Running tests with detailed output..."
cd "$PROJECT_ROOT"

# Generate JSON report
go test -v -json ./tests/integration -count=1 > "$REPORT_DIR/test-output-$TIMESTAMP.json" 2>&1

# Generate coverage report
echo ""
echo "📈 Generating coverage report..."
go test -coverprofile="$REPORT_DIR/coverage-$TIMESTAMP.out" ./tests/integration -count=1 > /dev/null 2>&1 || true
go tool cover -html="$REPORT_DIR/coverage-$TIMESTAMP.out" -o "$REPORT_DIR/coverage-$TIMESTAMP.html" 2>/dev/null || true

# Generate summary report
echo ""
echo "📝 Generating test summary..."

# Parse JSON output for summary
TOTAL_TESTS=$(grep -c '"Action":"pass"' "$REPORT_DIR/test-output-$TIMESTAMP.json" 2>/dev/null || echo "0")
FAILED_TESTS=$(grep -c '"Action":"fail"' "$REPORT_DIR/test-output-$TIMESTAMP.json" 2>/dev/null || echo "0")
SKIPPED_TESTS=$(grep -c '"Action":"skip"' "$REPORT_DIR/test-output-$TIMESTAMP.json" 2>/dev/null || echo "0")

# Calculate coverage percentage
COVERAGE_PCT="0"
if [ -f "$REPORT_DIR/coverage-$TIMESTAMP.out" ]; then
    COVERAGE_PCT=$(go tool cover -func="$REPORT_DIR/coverage-$TIMESTAMP.out" | grep total | awk '{print $3}' | sed 's/%//')
fi

# Generate summary file
cat > "$REPORT_DIR/summary-$TIMESTAMP.txt" << EOF
Integration Test Report
=======================
Generated: $(date)

Test Results
------------
✅ Passed:  $TOTAL_TESTS
❌ Failed:  $FAILED_TESTS
⏭️  Skipped: $SKIPPED_TESTS
📊 Coverage: ${COVERAGE_PCT}%

Test Suites
-----------
- Authentication Tests (auth_test.go)
- Portfolio Tests (portfolio_test.go)
- Discovery Tests (discover_test.go)
- Market Data Tests (discover_test.go)
- AI Endpoint Tests (discover_test.go)
- Admin Endpoint Tests (admin_test.go)
- Cron Job Tests (cron_test.go)

Files Generated
---------------
- JSON Output:    test-output-$TIMESTAMP.json
- Coverage Data:  coverage-$TIMESTAMP.out
- Coverage HTML:  coverage-$TIMESTAMP.html
- Summary Report: summary-$TIMESTAMP.txt

View Coverage Report
--------------------
open tests/reports/coverage-$TIMESTAMP.html

View Full Output
----------------
cat tests/reports/test-output-$TIMESTAMP.json | jq .

EOF

# Display summary
cat "$REPORT_DIR/summary-$TIMESTAMP.txt"

echo ""
echo "✅ Reports generated in: $REPORT_DIR"
echo ""
echo "Quick Links:"
echo "  Summary:  cat tests/reports/summary-$TIMESTAMP.txt"
echo "  Coverage: open tests/reports/coverage-$TIMESTAMP.html"
echo "  JSON:     cat tests/reports/test-output-$TIMESTAMP.json | jq ."
echo ""

# Create symlink to latest reports
ln -sf "summary-$TIMESTAMP.txt" "$REPORT_DIR/latest-summary.txt"
ln -sf "coverage-$TIMESTAMP.html" "$REPORT_DIR/latest-coverage.html"
ln -sf "test-output-$TIMESTAMP.json" "$REPORT_DIR/latest-output.json"

echo "📎 Latest reports also available at:"
echo "  tests/reports/latest-summary.txt"
echo "  tests/reports/latest-coverage.html"
echo "  tests/reports/latest-output.json"

# Exit with test status
if [ "$FAILED_TESTS" -gt 0 ]; then
    exit 1
fi

exit 0
