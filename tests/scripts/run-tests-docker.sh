#!/bin/bash
set -e

# Integration Test Runner with Docker
# This script starts test databases and runs integration tests

echo "🚀 Starting test infrastructure..."

# Start Docker containers
docker compose -f docker-compose.test.yml up -d

# Wait for services to be healthy
echo "⏳ Waiting for PostgreSQL to be ready..."
timeout 30 bash -c 'until docker exec estara-postgres-test pg_isready -U postgres > /dev/null 2>&1; do sleep 1; done'

echo "⏳ Waiting for Redis to be ready..."
timeout 30 bash -c 'until docker exec estara-redis-test redis-cli ping > /dev/null 2>&1; do sleep 1; done'

echo "✅ Test infrastructure ready"

# Load environment variables
if [ -f .env.test ]; then
    export $(cat .env.test | grep -v '^#' | xargs)
else
    echo "⚠️  .env.test not found, using default values"
    export TEST_DATABASE_URL="postgresql://postgres:postgres@localhost:5433/estara_test?sslmode=disable"
    export TEST_MARKET_DATABASE_URL="postgresql://postgres:postgres@localhost:5433/estara_market_test?sslmode=disable"
    export REDIS_URL="redis://localhost:6380/1"
    export JWT_SECRET="test-secret-key"
fi

# Run migrations (if needed)
echo "📦 Running migrations..."
# Uncomment if you have a migrate command:
# DATABASE_URL=$TEST_DATABASE_URL go run cmd/server/main.go migrate

# Run integration tests
echo "🧪 Running integration tests..."
make test-integration

# Cleanup (optional - uncomment to stop containers after tests)
# echo "🧹 Stopping test infrastructure..."
# docker compose -f docker-compose.test.yml down

echo "✅ Tests complete"
