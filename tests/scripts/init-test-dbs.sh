#!/bin/bash
set -e

# This script runs automatically when the PostgreSQL container starts for the first time
# It creates the test databases required for integration tests

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    -- Create test databases
    CREATE DATABASE estara_test;
    CREATE DATABASE estara_market_test;

    -- Grant all privileges to postgres user
    GRANT ALL PRIVILEGES ON DATABASE estara_test TO postgres;
    GRANT ALL PRIVILEGES ON DATABASE estara_market_test TO postgres;

    -- Log database creation
    SELECT 'Test databases created successfully:' AS status;
    SELECT datname FROM pg_database WHERE datname LIKE '%test%';
EOSQL

echo "✅ Test databases initialized: estara_test, estara_market_test"
