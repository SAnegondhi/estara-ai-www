#!/bin/bash

# Quick Test Suite Summary (without running tests)
# Analyzes test files to provide coverage overview

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TEST_DIR="$PROJECT_ROOT/tests/integration"

echo "📊 Integration Test Suite Summary"
echo "=================================="
echo ""

# Function to count test functions in a file
count_tests() {
    local file=$1
    grep -c "^func Test" "$file" 2>/dev/null || echo "0"
}

# Function to count subtests in a file
count_subtests() {
    local file=$1
    grep -c "t.Run(" "$file" 2>/dev/null || echo "0"
}

# Analyze each test file
echo "Test Files:"
echo "-----------"

total_test_funcs=0
total_subtests=0

for test_file in "$TEST_DIR"/*_test.go; do
    if [ -f "$test_file" ]; then
        filename=$(basename "$test_file")
        test_funcs=$(count_tests "$test_file")
        subtests=$(count_subtests "$test_file")
        lines=$(wc -l < "$test_file" | tr -d ' ')

        total_test_funcs=$((total_test_funcs + test_funcs))
        total_subtests=$((total_subtests + subtests))

        echo "  ✅ $filename"
        echo "      Test Functions: $test_funcs"
        echo "      Subtests: $subtests"
        echo "      Lines of Code: $lines"
        echo ""
    fi
done

echo "Summary:"
echo "--------"
echo "  Total Test Files: $(ls -1 "$TEST_DIR"/*_test.go 2>/dev/null | wc -l | tr -d ' ')"
echo "  Total Test Functions: $total_test_funcs"
echo "  Total Subtests: $total_subtests"
echo "  Total Test Code: $(cat "$TEST_DIR"/*_test.go 2>/dev/null | wc -l | tr -d ' ') lines"
echo ""

echo "Test Coverage by Category:"
echo "--------------------------"
echo "  🔐 Authentication (auth_test.go)"
echo "  📁 Portfolio Management (portfolio_test.go)"
echo "  🔍 Property Discovery (discover_test.go)"
echo "  📊 Market Data (discover_test.go)"
echo "  🤖 AI Endpoints (discover_test.go)"
echo "  👑 Admin Endpoints (admin_test.go)"
echo "  ⏰ Cron Jobs (cron_test.go)"
echo ""

echo "Helper Files:"
echo "-------------"
if [ -f "$TEST_DIR/helpers.go" ]; then
    helper_lines=$(wc -l < "$TEST_DIR/helpers.go" | tr -d ' ')
    helper_funcs=$(grep -c "^func " "$TEST_DIR/helpers.go" 2>/dev/null || echo "0")
    echo "  ✅ helpers.go"
    echo "      Functions: $helper_funcs"
    echo "      Lines: $helper_lines"
else
    echo "  ⚠️  No helpers.go found"
fi
echo ""

echo "Quick Commands:"
echo "---------------"
echo "  Run all tests:       make test-integration"
echo "  Generate report:     make test-report"
echo "  Coverage report:     make test-coverage"
echo "  Specific suite:      go test -v ./tests/integration -run TestSuiteName"
echo ""

echo "Documentation:"
echo "--------------"
echo "  📖 tests/README.md - Comprehensive test guide"
echo "  📖 tests/SETUP.md - Setup instructions"
echo "  📖 ADR-084 - Integration testing decision record"
echo ""
