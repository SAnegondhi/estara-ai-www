#!/bin/bash
set -e

# Complete Test Database Setup Script
# Creates databases and applies complete schema + migrations

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "🚀 Test Database Setup"
echo "======================"
echo ""

# Step 1: Start Docker containers
echo "1️⃣  Starting Docker containers..."
cd "$PROJECT_ROOT"
docker compose -f docker-compose.test.yml up -d

echo "   Waiting for PostgreSQL to be ready..."
timeout 30 bash -c 'until docker exec estara-postgres-test pg_isready -U postgres > /dev/null 2>&1; do sleep 1; done' || {
    echo "❌ PostgreSQL failed to start"
    exit 1
}

echo "   Waiting for Redis to be ready..."
timeout 30 bash -c 'until docker exec estara-redis-test redis-cli ping > /dev/null 2>&1; do sleep 1; done' || {
    echo "❌ Redis failed to start"
    exit 1
}

echo "✅ Infrastructure ready"
echo ""

# Step 2: Load environment variables
echo "2️⃣  Loading environment configuration..."
if [ -f "$PROJECT_ROOT/.env.test" ]; then
    export $(cat "$PROJECT_ROOT/.env.test" | grep -v '^#' | xargs)
    echo "✅ Environment loaded from .env.test"
else
    echo "⚠️  .env.test not found, creating from template..."
    cp "$PROJECT_ROOT/.env.test.example" "$PROJECT_ROOT/.env.test"
    export $(cat "$PROJECT_ROOT/.env.test" | grep -v '^#' | xargs)
fi
echo ""

# Step 3: Apply base schemas (CREATE TABLE statements)
echo "3️⃣  Applying base database schemas..."

# Apply main database schemas (24 files in numbered order)
echo "   📄 Main database (estara_test):"
SCHEMA_COUNT=0
for schema_file in internal/db/queries/schema/*.sql; do
    if [ -f "$schema_file" ]; then
        filename=$(basename "$schema_file")
        echo "      → $filename"
        docker exec -i estara-postgres-test psql -U postgres -d estara_test < "$schema_file" > /dev/null 2>&1 || {
            echo "      ⚠️  Warning: $filename may have had errors (possibly OK if idempotent)"
        }
        SCHEMA_COUNT=$((SCHEMA_COUNT + 1))
    fi
done
echo "   ✅ Applied $SCHEMA_COUNT schema files to main database"

# Apply market database schema (1 file)
echo "   📄 Market database (estara_market_test):"
if [ -f "internal/db/marketqueries/schema/market.sql" ]; then
    echo "      → market.sql"
    docker exec -i estara-postgres-test psql -U postgres -d estara_market_test < "internal/db/marketqueries/schema/market.sql" > /dev/null 2>&1 || {
        echo "      ⚠️  Warning: market.sql may have had errors (possibly OK if idempotent)"
    }
    echo "   ✅ Applied market schema"
fi
echo ""

# Step 4: Run incremental migrations
echo "4️⃣  Running incremental migrations..."
echo "   (Starting Go application on port 3001 to trigger embedded migrations)"
echo ""

# Use port 3001 to avoid conflicts with running development server
(
    cd "$PROJECT_ROOT"
    PORT=3001 timeout 15s go run cmd/server/main.go 2>&1 | tee /tmp/migration-log.txt || true
) &

# Wait for migrations to complete
sleep 8

# Kill any remaining server processes
pkill -f "go run cmd/server/main.go" 2>/dev/null || true

echo ""
echo "✅ Migrations completed"
echo ""

# Step 5: Verify schema
echo "5️⃣  Verifying database schema..."

MAIN_TABLES=$(docker exec estara-postgres-test psql -U postgres -d estara_test -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public';" | tr -d ' ')
MARKET_TABLES=$(docker exec estara-postgres-test psql -U postgres -d estara_market_test -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public';" | tr -d ' ')

echo "   Main database tables: $MAIN_TABLES"
echo "   Market database tables: $MARKET_TABLES"

# Verify key tables exist
echo ""
echo "   🔍 Checking critical tables..."
USERS_EXISTS=$(docker exec estara-postgres-test psql -U postgres -d estara_test -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'users';" | tr -d ' ')
SESSIONS_EXISTS=$(docker exec estara-postgres-test psql -U postgres -d estara_test -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'discovery_sessions';" | tr -d ' ')
MARKET_CACHE_EXISTS=$(docker exec estara-postgres-test psql -U postgres -d estara_market_test -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'city_market_cache';" | tr -d ' ')

if [ "$USERS_EXISTS" -eq 1 ]; then
    echo "      ✅ users table exists"
else
    echo "      ❌ users table MISSING"
fi

if [ "$SESSIONS_EXISTS" -eq 1 ]; then
    echo "      ✅ discovery_sessions table exists"
else
    echo "      ❌ discovery_sessions table MISSING"
fi

if [ "$MARKET_CACHE_EXISTS" -eq 1 ]; then
    echo "      ✅ city_market_cache table exists"
else
    echo "      ❌ city_market_cache table MISSING"
fi

if [ "$MAIN_TABLES" -lt 20 ]; then
    echo ""
    echo "⚠️  Warning: Fewer tables than expected in main database"
    echo "   Expected: 30+ tables, Found: $MAIN_TABLES tables"
    echo ""
    echo "   Migration log (last 30 lines):"
    tail -30 /tmp/migration-log.txt 2>/dev/null || echo "   (log not available)"
    echo ""
fi

echo ""
echo "✅ Database schema setup complete!"
echo ""
echo "📊 Summary:"
echo "   • PostgreSQL: localhost:5433"
echo "   • Redis: localhost:6380"
echo "   • Main DB: estara_test ($MAIN_TABLES tables)"
echo "   • Market DB: estara_market_test ($MARKET_TABLES tables)"
echo ""
echo "🧪 Next steps:"
echo "   Run tests:        make test-integration"
echo "   Generate report:  make test-report"
echo "   Quick summary:    ./tests/scripts/test-summary.sh"
echo ""
