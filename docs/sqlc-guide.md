# sqlc Guide for WWW Backend

This guide covers the use of sqlc for type-safe database queries in the Go backend.

## Overview

sqlc generates type-safe Go code from SQL queries. This catches column name mismatches at compile time rather than runtime.

## Configuration

The sqlc configuration is in `www/sqlc.yaml`:

```yaml
version: "2"
sql:
  # Main application database
  - engine: "postgresql"
    queries: "internal/db/queries/sql/"
    schema: "internal/db/queries/schema/"
    gen:
      go:
        package: "queries"
        out: "internal/db/queries"

  # Market database (separate PostgreSQL for time series)
  - engine: "postgresql"
    queries: "internal/db/marketqueries/sql/"
    schema: "internal/db/marketqueries/schema/"
    gen:
      go:
        package: "marketqueries"
        out: "internal/db/marketqueries"
```

## Directory Structure

```
www/
├── sqlc.yaml                           # sqlc configuration
├── internal/db/
│   ├── queries/                        # Main database
│   │   ├── schema/                     # DDL matching main database
│   │   │   └── *.sql
│   │   ├── sql/                        # Query definitions
│   │   │   └── *.sql
│   │   ├── db.go                       # Generated: DB interface
│   │   ├── models.go                   # Generated: Structs
│   │   ├── querier.go                  # Generated: Query interface
│   │   └── *.sql.go                    # Generated: Query implementations
│   │
│   └── marketqueries/                  # Market database
│       ├── schema/
│       │   └── market.sql              # DDL for market tables
│       ├── sql/
│       │   └── market.sql              # Market queries
│       ├── db.go                       # Generated
│       ├── models.go                   # Generated
│       ├── querier.go                  # Generated
│       └── market.sql.go               # Generated
```

## Workflow

### 1. Adding a New Query

```sql
-- In internal/db/marketqueries/sql/market.sql

-- name: GetCityByName :one
SELECT city, state, median_home_price
FROM city_market_cache
WHERE city ILIKE $1;
```

```bash
sqlc generate
```

This generates:
```go
// In market.sql.go
func (q *Queries) GetCityByName(ctx context.Context, city string) (GetCityByNameRow, error)
```

### 2. Updating Schema

When database schema changes:

1. Get current schema from database:
   ```bash
   source .env.local
   psql "$MARKET_DATABASE_URL" -c "
     SELECT column_name, data_type
     FROM information_schema.columns
     WHERE table_name = 'city_market_cache';"
   ```

2. Update schema file to match:
   ```sql
   -- internal/db/marketqueries/schema/market.sql
   CREATE TABLE city_market_cache (
       city VARCHAR(100) NOT NULL,
       state VARCHAR(2) NOT NULL,  -- NOT state_code!
       ...
   );
   ```

3. Regenerate:
   ```bash
   sqlc generate
   go build ./...
   ```

### 3. Before Every Commit

```bash
sqlc generate && go build ./...
```

Both must pass with no errors.

## Error Examples

### Wrong Column Name

```sql
-- BAD: Using wrong column name
SELECT state_code FROM city_market_cache;
```

```bash
$ sqlc generate
internal/db/marketqueries/sql/market.sql:5:8: column "state_code" does not exist
```

### Missing Table

```sql
-- BAD: Table doesn't exist
SELECT * FROM metro_data;
```

```bash
$ sqlc generate
internal/db/marketqueries/sql/market.sql:2:15: relation "metro_data" does not exist
```

## Using Generated Code

```go
import "github.com/estara-ai/www/internal/db/marketqueries"

func (s *Service) GetMarketData(ctx context.Context, city, state string) (*MarketData, error) {
    // Create queries instance with database connection
    q := marketqueries.New(s.db.Market)

    // Use type-safe generated function
    row, err := q.GetCityMarketData(ctx, marketqueries.GetCityMarketDataParams{
        City:  city,
        State: state,
    })
    if err != nil {
        return nil, err
    }

    // row is strongly typed - no manual scanning
    return &MarketData{
        City:            row.City,
        State:           row.State,
        MedianHomePrice: row.MedianHomePrice,
    }, nil
}
```

## Common Pitfalls

### Market Database Column Names

| Wrong | Correct | Table |
|-------|---------|-------|
| `state_code` | `state` | city_market_cache |
| `median_home_value` | `median_home_price` | city_market_cache |
| `yoy_change` | `price_yoy_change` | city_market_cache |
| `updated_at` | `last_updated` | city_market_cache |
| `zhvi_forecast_data` | `zhvf_data` | metro_time_series |

### Nullable Columns

sqlc uses `pgtype` for nullable columns:
```go
// Nullable int
row.MetroRegionID  // pgtype.Int4, not int

// Check if valid
if row.MetroRegionID.Valid {
    id := row.MetroRegionID.Int32
}
```

## References

- [sqlc Documentation](https://docs.sqlc.dev/)
- [sqlc GitHub](https://github.com/sqlc-dev/sqlc)
- [pgx v5 Documentation](https://pkg.go.dev/github.com/jackc/pgx/v5)
