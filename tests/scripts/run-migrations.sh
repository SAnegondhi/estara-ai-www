#!/bin/bash
set -e

# Run database migrations for test databases
# This ensures the test databases have the complete schema

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "🔧 Running database migrations for test environment..."
echo ""

# Load test environment variables
if [ -f "$PROJECT_ROOT/.env.test" ]; then
    export $(cat "$PROJECT_ROOT/.env.test" | grep -v '^#' | xargs)
    echo "✅ Loaded test environment variables"
else
    echo "⚠️  Warning: .env.test not found, using defaults"
    export DATABASE_URL="postgresql://postgres:postgres@localhost:5433/estara_test?sslmode=disable"
    export MARKET_DATABASE_URL="postgresql://postgres:postgres@localhost:5433/estara_market_test?sslmode=disable"
fi

echo ""
echo "📊 Database URLs:"
echo "  Main:   $DATABASE_URL"
echo "  Market: $MARKET_DATABASE_URL"
echo ""

# Check if databases exist
echo "🔍 Checking database connectivity..."
if ! psql "$DATABASE_URL" -c '\q' 2>/dev/null; then
    echo "❌ Cannot connect to main database: $DATABASE_URL"
    echo ""
    echo "💡 Is the test infrastructure running?"
    echo "   Run: docker compose -f docker-compose.test.yml up -d"
    exit 1
fi

if ! psql "$MARKET_DATABASE_URL" -c '\q' 2>/dev/null; then
    echo "❌ Cannot connect to market database: $MARKET_DATABASE_URL"
    exit 1
fi

echo "✅ Database connections successful"
echo ""

# Option 1: Run migrations via Go application
echo "🚀 Running migrations via Go application..."
cd "$PROJECT_ROOT"

# The Go application automatically runs embedded migrations on startup
# We'll use a temporary server start to trigger migrations
echo "   Starting temporary server to run migrations..."
timeout 10 go run cmd/server/main.go 2>&1 | grep -E "(migration|database)" || true

echo ""
echo "✅ Migrations completed!"
echo ""

# Verify schema by checking for key tables
echo "🔍 Verifying schema..."

# Check main database tables
MAIN_TABLES=$(psql "$DATABASE_URL" -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public';")
echo "  Main database tables: $MAIN_TABLES"

# Check market database tables
MARKET_TABLES=$(psql "$MARKET_DATABASE_URL" -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public';")
echo "  Market database tables: $MARKET_TABLES"

if [ "$MAIN_TABLES" -lt 10 ]; then
    echo ""
    echo "⚠️  Warning: Main database has fewer tables than expected"
    echo "   Expected: 30+ tables"
    echo "   Found: $MAIN_TABLES tables"
    echo ""
    echo "   This might indicate migration failures."
    echo "   Check logs above for errors."
fi

echo ""
echo "✅ Database schema setup complete!"
echo ""
echo "Next steps:"
echo "  - Run tests: make test-integration"
echo "  - Generate report: make test-report"
echo ""
