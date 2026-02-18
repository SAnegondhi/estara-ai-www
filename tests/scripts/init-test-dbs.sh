#!/bin/bash
set -e

# Initialize test databases with full schema
# This script runs automatically when the Docker container starts for the first time

echo "🔧 Initializing test databases..."

# Create databases
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
    CREATE DATABASE estara_test;
    CREATE DATABASE estara_market_test;
EOSQL

echo "✅ Test databases created"

# Note: Schema migrations are run by the Go application on startup
# The migrate.go file automatically runs all SQL migrations from internal/db/postgres/migrations/
# when the application connects to the database.
#
# This ensures the schema is always up-to-date with the embedded migration files.
#
# If you need to manually run migrations:
# 1. Ensure the www/ application has been built
# 2. Run: DATABASE_URL="postgresql://postgres:postgres@localhost:5433/estara_test?sslmode=disable" \
#         go run cmd/server/main.go migrate
