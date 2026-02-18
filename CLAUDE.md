# WWW (Go Backend) - Local Rules

These rules are specific to the `www/` Go backend. They supplement the global rules in `~/.claude/CLAUDE.md`.

---

## FUNDAMENTAL ARCHITECTURE RULES

These are non-negotiable constraints on all Go code in `www/`. No exceptions.

### 1. sqlc is the ONLY database interface (ADR-083)

> **"Make sqlc-generated code the only interface between Go and PostgreSQL. Zero raw SQL in handlers or services. Every query validated at build time."**
> — ADR-083: Single Auditable Database Interface

- ❌ **NEVER** write raw SQL strings in Go handlers, services, or helpers
- ❌ **NEVER** call `db.Exec()`, `db.Query()`, or `db.QueryRow()` directly in handlers or services
- ❌ **NEVER** access `Store.Pool()` or `Store.MarketPool()` outside of `internal/db/store.go`
- ✅ **ALWAYS** add new queries to the appropriate `.sql` file, run `sqlc generate`, and use the generated method
- ✅ **ALWAYS** run `sqlc generate && go build ./...` before committing — both must pass

Every SQL query that bypasses sqlc is a defect. The cost of a missing sqlc query is 5 minutes of boilerplate. The cost of a raw SQL bug is a runtime failure in production with a wrong column name or NULL scan that only surfaces under load.

### 2. Store pattern — all DB access via `Store`

All handlers and services receive a `*db.Store` (from `internal/db/store.go`). Database access flows:

```
Handler → Store.Q()  → sqlc Queries (main DB)
Handler → Store.MQ() → sqlc Queries (market DB)
```

Never pass `*pgxpool.Pool` directly to handlers or services.

### 3. Build must be clean before every commit

```bash
sqlc generate   # Must produce no errors
go build ./...  # Must compile with zero errors
```

---

## SQLC - MANDATORY FOR ALL DATABASE QUERIES

**CRITICAL**: All database queries MUST use sqlc-generated code. No raw SQL strings in Go code.

### Databases and Schemas

| Database | Schema Location | Queries Location | Generated Code |
|----------|-----------------|------------------|----------------|
| Main | `internal/db/queries/schema/` | `internal/db/queries/sql/` | `internal/db/queries/` |
| Market | `internal/db/marketqueries/schema/` | `internal/db/marketqueries/sql/` | `internal/db/marketqueries/` |

### MANDATORY WORKFLOW

1. **Before ANY database query change**:
   ```bash
   sqlc generate  # Must pass without errors
   ```

2. **After ANY schema change**:
   ```bash
   # Update schema file to match database
   # Then regenerate
   sqlc generate
   go build ./...  # Must compile without errors
   ```

3. **Before EVERY commit**:
   ```bash
   sqlc generate && go build ./...  # BOTH must pass
   ```

### Adding New Queries

1. Add query to appropriate `.sql` file in `sql/` directory
2. Run `sqlc generate`
3. If error → fix column/table names to match schema
4. Use generated functions in Go code

### Schema Changes

When database schema changes:
1. Update `schema/*.sql` to match actual database
2. Update `sql/*.sql` queries if needed
3. Run `sqlc generate`
4. Update Go code to use new generated types
5. Run `go build ./...`

### Why This Matters

**Without sqlc**: Runtime errors when column names don't match
```go
// BAD - raw SQL with wrong column name
query := "SELECT state_code FROM city_market_cache"  // state_code doesn't exist!
// Error only discovered at runtime
```

**With sqlc**: Compile-time errors catch mistakes
```sql
-- In market.sql
SELECT state_code FROM city_market_cache;
```
```bash
$ sqlc generate
ERROR: column "state_code" does not exist  # Caught immediately!
```

---

## BUILD REQUIREMENTS

**Zero tolerance for errors**:

```bash
# Required before every commit
sqlc generate        # Must pass
go build ./...       # Must pass
go vet ./...         # Must pass (if configured)
```

---

## DATABASE NAMING CONVENTIONS

### Main Database Tables
- Use snake_case for table and column names
- Primary keys: `id` (auto-increment) or `<entity>_id` (UUID)
- Timestamps: `created_at`, `updated_at`

### Market Database Tables
| Table | Key Columns | Notes |
|-------|-------------|-------|
| `city_market_cache` | `city`, `state` (NOT state_code) | `median_home_price` (NOT median_home_value) |
| `metro_time_series` | `metro_region_id`, `metro_name` | `zhvf_data` (NOT zhvi_forecast_data) |

---

## COMMON COLUMN NAME PITFALLS

Avoid these common mistakes:

| Wrong | Correct | Table |
|-------|---------|-------|
| `state_code` | `state` | city_market_cache |
| `median_home_value` | `median_home_price` | city_market_cache |
| `yoy_change` | `price_yoy_change` | city_market_cache |
| `updated_at` | `last_updated` | city_market_cache |
| `zhvi_forecast_data` | `zhvf_data` | metro_time_series |
| `zori_forecast_data` | (doesn't exist) | metro_time_series |

---

## DATABASE MIGRATIONS

**Automatic Migrations**: SQL migrations run automatically on server startup.

### Adding New Migrations

1. Create SQL file in `internal/db/postgres/migrations/`
2. Name format: `YYYYMMDD_description.sql` (e.g., `20260207_add_system_cache.sql`)
3. Migrations are embedded at compile time and run in alphabetical order
4. Each migration runs in a transaction with automatic rollback on failure

### Migration Tracking

- Applied migrations are tracked in `schema_migrations` table
- Only pending migrations are executed on startup
- Safe to restart server - already-applied migrations are skipped

### Example Migration

```sql
-- Migration: 20260207_add_system_cache
-- Description: Adds system_cache table for L2 caching
-- Author: Claude
-- Date: 2026-02-07

CREATE TABLE IF NOT EXISTS system_cache (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_system_cache_expires_at ON system_cache(expires_at);
```

### Key Files

- `internal/db/postgres/migrate.go` - Migration runner (uses Go embed)
- `internal/db/postgres/migrations/*.sql` - Migration SQL files

---

## DBQ - DATABASE QUERY TOOL (MANDATORY FOR DEBUGGING)

**CRITICAL**: All debug and test database queries MUST use the `dbq` tool. Do NOT use raw `psql` commands.

### Why dbq?

- Handles connection strings automatically (reads from .env.local)
- Formats output nicely (table format or JSON)
- Has preset queries for common scenarios
- Shows correct column names via `-schema` flag
- Works with both main and market databases

### Basic Usage

```bash
# List available presets
go run cmd/dbq/main.go -list-presets

# Use a preset query
go run cmd/dbq/main.go -preset=peoria
go run cmd/dbq/main.go -preset=users

# Custom query
go run cmd/dbq/main.go -db=market -q="SELECT city, state FROM city_market_cache LIMIT 5"
go run cmd/dbq/main.go -db=main -q="SELECT id, email FROM users LIMIT 5"

# Show table schema (column names and types)
go run cmd/dbq/main.go -db=market -schema=city_market_cache
go run cmd/dbq/main.go -db=main -schema=users

# Sample data from a table
go run cmd/dbq/main.go -db=market -table=city_market_cache

# JSON output
go run cmd/dbq/main.go -preset=peoria -json
```

### Available Presets

| Preset | Database | Description |
|--------|----------|-------------|
| `peoria` | market | Peoria, IL market data |
| `metros` | market | List first 20 metros |
| `cities` | market | List first 20 cached cities |
| `city-count` | market | Count cities by state |
| `metro-count` | market | Total metro count |
| `expired-cache` | market | Cities with expired cache |
| `ai-estimated` | market | AI-estimated market data |
| `users` | main | Recent users |
| `subscriptions` | main | Recent subscriptions |

### Checking Column Names

Before writing queries, verify column names exist:

```bash
# Check market database table schema
go run cmd/dbq/main.go -db=market -schema=city_market_cache

# Check main database table schema
go run cmd/dbq/main.go -db=main -schema=users
```

### DO NOT USE

```bash
# ❌ DON'T use raw psql
psql "$MARKET_DATABASE_URL" -c "SELECT * FROM city_market_cache"

# ✅ DO use dbq
go run cmd/dbq/main.go -db=market -table=city_market_cache
```
