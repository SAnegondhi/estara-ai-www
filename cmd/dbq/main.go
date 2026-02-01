// dbq - Database Query Tool for debugging
//
// A general-purpose CLI tool for querying main and market databases.
// Used for debugging and testing database queries.
//
// Usage:
//
//	go run cmd/dbq/main.go -db=market -q="SELECT city, state FROM city_market_cache LIMIT 5"
//	go run cmd/dbq/main.go -db=main -q="SELECT id, email FROM users LIMIT 5"
//	go run cmd/dbq/main.go -db=market -table=city_market_cache
//	go run cmd/dbq/main.go -db=market -schema=city_market_cache
//	go run cmd/dbq/main.go -db=market -preset=peoria
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

// Preset queries for common debugging scenarios
var presets = map[string]struct {
	db    string
	query string
	desc  string
}{
	// Market database presets
	"peoria": {
		db:    "market",
		query: `SELECT city, state, median_home_price, median_rent, price_yoy_change, last_updated FROM city_market_cache WHERE city ILIKE 'Peoria' AND state = 'IL'`,
		desc:  "Get Peoria, IL market data",
	},
	"metros": {
		db:    "market",
		query: `SELECT metro_region_id, metro_name, state_name FROM metro_time_series ORDER BY metro_name LIMIT 20`,
		desc:  "List first 20 metros",
	},
	"cities": {
		db:    "market",
		query: `SELECT city, state, median_home_price, median_rent FROM city_market_cache ORDER BY city LIMIT 20`,
		desc:  "List first 20 cached cities",
	},
	"city-count": {
		db:    "market",
		query: `SELECT state, COUNT(*) as count FROM city_market_cache GROUP BY state ORDER BY count DESC`,
		desc:  "Count cities by state",
	},
	"metro-count": {
		db:    "market",
		query: `SELECT COUNT(*) as total FROM metro_time_series`,
		desc:  "Total metro count",
	},
	"expired-cache": {
		db:    "market",
		query: `SELECT city, state, ttl_expires_at FROM city_market_cache WHERE ttl_expires_at < NOW() LIMIT 10`,
		desc:  "Cities with expired cache",
	},
	"ai-estimated": {
		db:    "market",
		query: `SELECT city, state, median_home_price, ai_confidence_score FROM city_market_cache WHERE is_ai_estimated = true LIMIT 10`,
		desc:  "AI-estimated market data",
	},

	// Main database presets
	"users": {
		db:    "main",
		query: `SELECT id, email, "firstName", "lastName", "createdAt" FROM users ORDER BY "createdAt" DESC LIMIT 10`,
		desc:  "Recent users",
	},
	"subscriptions": {
		db:    "main",
		query: `SELECT id, "userId", tier, status, "createdAt" FROM subscriptions ORDER BY "createdAt" DESC LIMIT 10`,
		desc:  "Recent subscriptions",
	},
	"cache": {
		db:    "main",
		query: `SELECT key, "expiresAt", "createdAt" FROM ai_cache ORDER BY "createdAt" DESC LIMIT 10`,
		desc:  "Recent AI cache entries",
	},
}

func main() {
	// Parse flags
	dbName := flag.String("db", "", "Database to query: 'main' or 'market' (required)")
	query := flag.String("q", "", "SQL query to execute")
	tableName := flag.String("table", "", "Show sample data from table (SELECT * LIMIT 5)")
	schemaName := flag.String("schema", "", "Show table schema (column names and types)")
	preset := flag.String("preset", "", "Use a preset query (run with -list-presets to see available)")
	listPresets := flag.Bool("list-presets", false, "List available preset queries")
	jsonOutput := flag.Bool("json", false, "Output results as JSON")
	limit := flag.Int("limit", 0, "Override LIMIT in query (0 = no override)")

	flag.Parse()

	// List presets
	if *listPresets {
		fmt.Println("Available presets:")
		fmt.Println()
		for name, p := range presets {
			fmt.Printf("  %-15s [%s] %s\n", name, p.db, p.desc)
		}
		fmt.Println()
		fmt.Println("Usage: go run cmd/dbq/main.go -preset=<name>")
		return
	}

	// Load environment variables
	if err := godotenv.Load(".env.local"); err != nil {
		if err := godotenv.Load(".env"); err != nil {
			fmt.Println("Warning: No .env file found, using environment variables")
		}
	}

	// Handle preset
	if *preset != "" {
		p, ok := presets[*preset]
		if !ok {
			fmt.Printf("Error: Unknown preset '%s'. Run with -list-presets to see available.\n", *preset)
			os.Exit(1)
		}
		*dbName = p.db
		*query = p.query
		fmt.Printf("Preset: %s\n", p.desc)
		fmt.Printf("Database: %s\n", p.db)
		fmt.Println()
	}

	// Handle table shortcut
	if *tableName != "" {
		if *dbName == "" {
			fmt.Println("Error: -db flag is required with -table")
			os.Exit(1)
		}
		*query = fmt.Sprintf("SELECT * FROM %s LIMIT 5", *tableName)
	}

	// Handle schema shortcut
	if *schemaName != "" {
		if *dbName == "" {
			fmt.Println("Error: -db flag is required with -schema")
			os.Exit(1)
		}
		*query = fmt.Sprintf(`
			SELECT column_name, data_type, is_nullable, column_default
			FROM information_schema.columns
			WHERE table_name = '%s'
			ORDER BY ordinal_position`, *schemaName)
	}

	// Validate required flags
	if *dbName == "" || *query == "" {
		fmt.Println("Usage: go run cmd/dbq/main.go -db=<main|market> -q=\"<SQL query>\"")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  go run cmd/dbq/main.go -db=market -q=\"SELECT * FROM city_market_cache LIMIT 5\"")
		fmt.Println("  go run cmd/dbq/main.go -db=market -table=city_market_cache")
		fmt.Println("  go run cmd/dbq/main.go -db=market -schema=city_market_cache")
		fmt.Println("  go run cmd/dbq/main.go -preset=peoria")
		fmt.Println("  go run cmd/dbq/main.go -list-presets")
		os.Exit(1)
	}

	// Get database URL
	var dbURL string
	switch *dbName {
	case "main":
		dbURL = os.Getenv("DATABASE_URL")
		if dbURL == "" {
			fmt.Println("Error: DATABASE_URL environment variable not set")
			os.Exit(1)
		}
	case "market":
		dbURL = os.Getenv("MARKET_DATABASE_URL")
		if dbURL == "" {
			fmt.Println("Error: MARKET_DATABASE_URL environment variable not set")
			os.Exit(1)
		}
	default:
		fmt.Printf("Error: Invalid database '%s'. Use 'main' or 'market'.\n", *dbName)
		os.Exit(1)
	}

	// Apply limit override
	if *limit > 0 {
		// Simple replacement - won't work for all queries but good enough for debugging
		q := strings.ToUpper(*query)
		if strings.Contains(q, "LIMIT") {
			// Replace existing limit
			// This is a simple approach - for complex queries might not work
		} else {
			*query = fmt.Sprintf("%s LIMIT %d", *query, *limit)
		}
	}

	// Execute query
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Printf("Error connecting to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, *query)
	if err != nil {
		fmt.Printf("Error executing query: %v\n", err)
		fmt.Println()
		fmt.Println("Query:")
		fmt.Println(*query)
		os.Exit(1)
	}
	defer rows.Close()

	// Get column names
	fieldDescs := rows.FieldDescriptions()
	columns := make([]string, len(fieldDescs))
	for i, fd := range fieldDescs {
		columns[i] = string(fd.Name)
	}

	// Collect results
	var results []map[string]interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			fmt.Printf("Error reading row: %v\n", err)
			continue
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		fmt.Printf("Error iterating rows: %v\n", err)
		os.Exit(1)
	}

	// Output results
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Printf("Error encoding JSON: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Pretty print as table
		if len(results) == 0 {
			fmt.Println("No results")
			return
		}

		// Calculate column widths
		widths := make(map[string]int)
		for _, col := range columns {
			widths[col] = len(col)
		}
		for _, row := range results {
			for col, val := range row {
				str := formatValue(val)
				if len(str) > widths[col] {
					widths[col] = len(str)
				}
				// Cap width at 50 chars
				if widths[col] > 50 {
					widths[col] = 50
				}
			}
		}

		// Print header
		var headerParts []string
		var separatorParts []string
		for _, col := range columns {
			headerParts = append(headerParts, fmt.Sprintf("%-*s", widths[col], col))
			separatorParts = append(separatorParts, strings.Repeat("-", widths[col]))
		}
		fmt.Println(strings.Join(headerParts, " | "))
		fmt.Println(strings.Join(separatorParts, "-+-"))

		// Print rows
		for _, row := range results {
			var rowParts []string
			for _, col := range columns {
				str := formatValue(row[col])
				if len(str) > 50 {
					str = str[:47] + "..."
				}
				rowParts = append(rowParts, fmt.Sprintf("%-*s", widths[col], str))
			}
			fmt.Println(strings.Join(rowParts, " | "))
		}

		fmt.Printf("\n(%d rows)\n", len(results))
	}
}

func formatValue(v interface{}) string {
	if v == nil {
		return "NULL"
	}

	switch val := v.(type) {
	case time.Time:
		return val.Format("2006-01-02 15:04:05")
	case []byte:
		// Truncate long byte arrays (like JSONB)
		s := string(val)
		if len(s) > 50 {
			return s[:47] + "..."
		}
		return s
	case map[string]interface{}:
		// JSON object
		b, _ := json.Marshal(val)
		s := string(b)
		if len(s) > 50 {
			return s[:47] + "..."
		}
		return s
	case float64:
		// Format floats nicely
		if val == float64(int64(val)) {
			return fmt.Sprintf("%.0f", val)
		}
		return fmt.Sprintf("%.2f", val)
	case float32:
		if val == float32(int32(val)) {
			return fmt.Sprintf("%.0f", val)
		}
		return fmt.Sprintf("%.2f", val)
	case string:
		return val
	case int, int32, int64, int16, int8:
		return fmt.Sprintf("%d", val)
	case uint, uint32, uint64, uint16, uint8:
		return fmt.Sprintf("%d", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		// For complex types like pgtype.Numeric, use JSON marshaling
		// which handles them correctly
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		s := string(b)
		// Remove quotes from strings
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		if len(s) > 50 {
			return s[:47] + "..."
		}
		return s
	}
}
