// marketdata - CLI tool for market data management
//
// Downloads, parses, and imports market data from Zillow and Redfin.
// Shares the same importer.Service as cron endpoints.
//
// Usage:
//
//	go run cmd/marketdata/main.go status
//	go run cmd/marketdata/main.go import --source=zillow-zhvi --level=metro
//	go run cmd/marketdata/main.go import --source=zillow-zhvi --level=all
//	go run cmd/marketdata/main.go import --source=redfin --level=metro
//	go run cmd/marketdata/main.go import --source=all
//	go run cmd/marketdata/main.go compute-national
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/services/market/importer"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	// Load environment variables
	if err := godotenv.Load(".env.local"); err != nil {
		_ = godotenv.Load(".env")
	}

	switch command {
	case "status":
		runStatus()
	case "import":
		runImport(os.Args[2:])
	case "compute-national":
		runComputeNational()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("marketdata - Market data management CLI (ADR-075)")
	fmt.Println()
	fmt.Println("Usage: go run cmd/marketdata/main.go <command> [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  status            Show row counts and freshness for all tables")
	fmt.Println("  import            Download and import market data")
	fmt.Println("  compute-national  Compute national aggregates from state data")
	fmt.Println("  help              Show this help")
	fmt.Println()
	fmt.Println("Import flags:")
	fmt.Println("  --source=<source>   Data source: zillow-zhvi, zillow-zori, zillow-zhvf,")
	fmt.Println("                      zillow-metrics, redfin, all (default: all)")
	fmt.Println("  --level=<level>     Geographic level: metro, city, zip, state, national,")
	fmt.Println("                      county, all (default: all)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  go run cmd/marketdata/main.go status")
	fmt.Println("  go run cmd/marketdata/main.go import --source=zillow-zhvi --level=metro")
	fmt.Println("  go run cmd/marketdata/main.go import --source=all")
	fmt.Println("  go run cmd/marketdata/main.go compute-national")
}

func connectMarketDB() (*postgres.Pool, func()) {
	dbURL := os.Getenv("MARKET_DATABASE_URL")
	if dbURL == "" {
		fmt.Println("Error: MARKET_DATABASE_URL environment variable not set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		fmt.Printf("Error parsing database URL: %v\n", err)
		os.Exit(1)
	}
	poolConfig.MaxConns = 4

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		fmt.Printf("Error connecting to market database: %v\n", err)
		os.Exit(1)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		fmt.Printf("Error pinging market database: %v\n", err)
		os.Exit(1)
	}

	pgPool := &postgres.Pool{Pool: pool}
	return pgPool, func() { pool.Close() }
}

func runStatus() {
	pool, cleanup := connectMarketDB()
	defer cleanup()

	svc := importer.NewService(pool, nil, nil)  // CLI: No main queries or refresh service

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := svc.Status(ctx)
	if err != nil {
		fmt.Printf("Error getting status: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Market Data Status")
	fmt.Println("==================")
	fmt.Println()
	printTableStatus("Metro", status.Metro)
	printTableStatus("City", status.City)
	printTableStatus("State", status.State)
	printTableStatus("Zip", status.Zip)
	printTableStatus("County", status.County)
}

func printTableStatus(name string, ts importer.TableStatus) {
	freshness := "never"
	if ts.LatestUpdate != nil {
		ago := time.Since(*ts.LatestUpdate)
		if ago < 24*time.Hour {
			freshness = fmt.Sprintf("%s (%.0f hours ago)", ts.LatestUpdate.Format("2006-01-02 15:04"), ago.Hours())
		} else {
			freshness = fmt.Sprintf("%s (%.0f days ago)", ts.LatestUpdate.Format("2006-01-02"), ago.Hours()/24)
		}
	}
	fmt.Printf("  %-8s  %6d rows  last updated: %s\n", name, ts.RowCount, freshness)
}

func runImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	source := fs.String("source", "all", "Data source (zillow-zhvi, zillow-zori, zillow-zhvf, zillow-metrics, redfin, all)")
	level := fs.String("level", "all", "Geographic level (metro, city, zip, state, national, county, all)")
	fs.Parse(args)

	pool, cleanup := connectMarketDB()
	defer cleanup()

	svc := importer.NewService(pool, nil, nil)  // CLI: No main queries or refresh service
	ctx := context.Background()

	if *source == "all" {
		fmt.Println("Running full import (all sources, all levels)...")
		result, err := svc.FullRefresh(ctx)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		printResult(result)
		return
	}

	// Run specific source
	levels := []string{*level}
	if *level == "all" {
		switch *source {
		case "zillow-zhvi":
			levels = []string{"metro", "city", "zip", "state"}
		case "zillow-zori":
			levels = []string{"metro", "city", "zip"}
		case "redfin":
			levels = []string{"national", "state", "metro", "county"}
		default:
			levels = []string{"metro"}
		}
	}

	for _, lvl := range levels {
		fmt.Printf("Importing %s (%s)...\n", *source, lvl)

		var result *importer.ImportResult
		var err error

		switch *source {
		case "zillow-zhvi":
			result, err = svc.ImportZillowZHVI(ctx, lvl)
		case "zillow-zori":
			result, err = svc.ImportZillowZORI(ctx, lvl)
		case "zillow-zhvf":
			result, err = svc.ImportZillowForecasts(ctx)
		case "zillow-metrics":
			result, err = svc.ImportZillowMetrics(ctx)
		case "redfin":
			result, err = svc.ImportRedfinData(ctx, lvl)
		default:
			fmt.Printf("Unknown source: %s\n", *source)
			os.Exit(1)
		}

		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		printResult(result)

		// For sources that only have one level, break after first iteration
		if *source == "zillow-zhvf" || *source == "zillow-metrics" {
			break
		}
	}
}

func runComputeNational() {
	pool, cleanup := connectMarketDB()
	defer cleanup()

	svc := importer.NewService(pool, nil, nil)  // CLI: No main queries or refresh service
	ctx := context.Background()

	fmt.Println("Computing national aggregates...")
	result, err := svc.ComputeNationalAggregates(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	printResult(result)
}

func printResult(result *importer.ImportResult) {
	fmt.Printf("  Source:    %s\n", result.Source)
	fmt.Printf("  Level:     %s\n", result.Level)
	fmt.Printf("  Processed: %d\n", result.RecordsProcessed)
	fmt.Printf("  Upserted:  %d\n", result.RecordsUpserted)
	fmt.Printf("  Skipped:   %d\n", result.RecordsSkipped)
	fmt.Printf("  Duration:  %s\n", result.Duration)
	if len(result.Errors) > 0 {
		fmt.Printf("  Errors:    %d\n", len(result.Errors))
		// Show first 5 errors
		for i, e := range result.Errors {
			if i >= 5 {
				fmt.Printf("    ... and %d more\n", len(result.Errors)-5)
				break
			}
			fmt.Printf("    - %s\n", truncate(e, 100))
		}
	}
	fmt.Println()

	// Also dump as JSON for scripting
	if os.Getenv("JSON_OUTPUT") == "1" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

