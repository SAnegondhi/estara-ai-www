package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/db/marketqueries"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	var (
		dryRun      = flag.Bool("dry-run", false, "Show what would be updated without making changes")
		limit       = flag.Int("limit", 0, "Limit number of reports to process (0 = all)")
		reportID    = flag.String("report-id", "", "Backfill single report by ID")
		showStats   = flag.Bool("stats", false, "Show backfill statistics")
	)
	flag.Parse()

	// Load environment variables
	if err := godotenv.Load(".env.local"); err != nil {
		_ = godotenv.Load(".env")
	}

	ctx := context.Background()

	// Connect to main database
	mainPool, closeMain := connectMainDB()
	defer closeMain()
	mainQueries := queries.New(mainPool)

	// Connect to market database (optional)
	var marketQueries *marketqueries.Queries
	marketPool, closeMarket := connectMarketDB()
	if marketPool != nil {
		defer closeMarket()
		marketQueries = marketqueries.New(marketPool)
	} else {
		log.Println("WARNING: Market database not configured - coordinate lookup will fail")
	}

	// Show statistics if requested
	if *showStats {
		showBackfillStats(ctx, mainQueries)
		return
	}

	// Backfill single report if specified
	if *reportID != "" {
		err := backfillSingleReport(ctx, *reportID, mainQueries, marketQueries, *dryRun)
		if err != nil {
			log.Fatalf("Failed to backfill report %s: %v", *reportID, err)
		}
		return
	}

	// Backfill all reports missing coordinates
	err := backfillAllReports(ctx, mainQueries, marketQueries, *limit, *dryRun)
	if err != nil {
		log.Fatalf("Backfill failed: %v", err)
	}
}

func showBackfillStats(ctx context.Context, q *queries.Queries) {
	fmt.Println("=== Coordinate Backfill Statistics ===")

	// Count total reports
	totalCount, err := q.CountReports(ctx)
	if err != nil {
		log.Printf("Failed to count reports: %v", err)
		return
	}

	// Count completed reports
	completedCount, err := q.CountCompletedReports(ctx)
	if err != nil {
		log.Printf("Failed to count completed reports: %v", err)
		return
	}

	// Fetch sample to check coordinates
	reports, err := q.ListAllReports(ctx, queries.ListAllReportsParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		log.Printf("Failed to fetch sample reports: %v", err)
		return
	}

	withCoords := 0
	for _, report := range reports {
		if report.Latitude.Valid && report.Longitude.Valid {
			withCoords++
		}
	}

	fmt.Printf("Total Reports:     %d\n", totalCount)
	fmt.Printf("Completed Reports: %d\n", completedCount)
	fmt.Printf("Sample (first 100):\n")
	fmt.Printf("  - With coords:   %d\n", withCoords)
	fmt.Printf("  - Without coords: %d\n", len(reports)-withCoords)
	fmt.Println()
}

func backfillSingleReport(
	ctx context.Context,
	reportID string,
	mainQueries *queries.Queries,
	marketQueries *marketqueries.Queries,
	dryRun bool,
) error {
	report, err := mainQueries.GetReportByID(ctx, reportID)
	if err != nil {
		return fmt.Errorf("failed to fetch report: %w", err)
	}

	// Skip if already has coordinates
	if report.Latitude.Valid && report.Longitude.Valid {
		log.Printf("Report %s already has coordinates (%.4f, %.4f)", reportID, report.Latitude.Float64, report.Longitude.Float64)
		return nil
	}

	// Extract city and state from location
	city, state, err := parseLocation(report.Location)
	if err != nil {
		return fmt.Errorf("failed to parse location '%s': %w", report.Location, err)
	}

	// Lookup coordinates in market database
	lat, lon, err := lookupCoordinates(ctx, marketQueries, city, state)
	if err != nil {
		return fmt.Errorf("failed to lookup coordinates for %s, %s: %w", city, state, err)
	}

	if dryRun {
		log.Printf("[DRY RUN] Would update %s (%s) with coords (%.4f, %.4f)", report.CacheKey, report.Location, lat, lon)
		return nil
	}

	// Update report with coordinates
	err = mainQueries.UpdateReportCoordinates(ctx, queries.UpdateReportCoordinatesParams{
		CacheKey:  report.CacheKey,
		Latitude:  pgtype.Float8{Float64: lat, Valid: true},
		Longitude: pgtype.Float8{Float64: lon, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to update coordinates: %w", err)
	}

	log.Printf("Updated %s (%s) with coords (%.4f, %.4f)", report.CacheKey, report.Location, lat, lon)
	return nil
}

func backfillAllReports(
	ctx context.Context,
	mainQueries *queries.Queries,
	marketQueries *marketqueries.Queries,
	limit int,
	dryRun bool,
) error {
	fmt.Println("=== Starting Coordinate Backfill ===")
	if dryRun {
		fmt.Println("DRY RUN MODE - No changes will be made")
	}
	fmt.Println()

	// Fetch all completed reports
	offset := int32(0)
	batchSize := int32(100)
	totalProcessed := 0
	totalUpdated := 0
	totalSkipped := 0
	totalFailed := 0

	for {
		reports, err := mainQueries.ListAllReports(ctx, queries.ListAllReportsParams{
			Limit:  batchSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("failed to fetch reports: %w", err)
		}

		if len(reports) == 0 {
			break
		}

		for _, report := range reports {
			totalProcessed++

			// Check limit
			if limit > 0 && totalProcessed > limit {
				goto done
			}

			// Skip if already has coordinates
			if report.Latitude.Valid && report.Longitude.Valid {
				totalSkipped++
				continue
			}

			// Skip non-completed reports
			if report.Status != "completed" {
				totalSkipped++
				continue
			}

			// Extract city and state
			city, state, err := parseLocation(report.Location)
			if err != nil {
				log.Printf("Failed to parse location '%s': %v", report.Location, err)
				totalFailed++
				continue
			}

			// Lookup coordinates
			lat, lon, err := lookupCoordinates(ctx, marketQueries, city, state)
			if err != nil {
				log.Printf("Failed to lookup coordinates for %s, %s: %v", city, state, err)
				totalFailed++
				continue
			}

			if dryRun {
				log.Printf("[DRY RUN] Would update %s (%s) with coords (%.4f, %.4f)", report.CacheKey, report.Location, lat, lon)
				totalUpdated++
				continue
			}

			// Update report
			err = mainQueries.UpdateReportCoordinates(ctx, queries.UpdateReportCoordinatesParams{
				CacheKey:  report.CacheKey,
				Latitude:  pgtype.Float8{Float64: lat, Valid: true},
				Longitude: pgtype.Float8{Float64: lon, Valid: true},
			})
			if err != nil {
				log.Printf("Failed to update %s: %v", report.CacheKey, err)
				totalFailed++
				continue
			}

			totalUpdated++

			// Progress indicator
			if totalUpdated%10 == 0 {
				fmt.Printf("Progress: %d processed, %d updated, %d skipped, %d failed\n",
					totalProcessed, totalUpdated, totalSkipped, totalFailed)
			}
		}

		offset += batchSize

		// Check limit
		if limit > 0 && totalProcessed >= limit {
			break
		}
	}

done:
	fmt.Println()
	fmt.Println("=== Backfill Complete ===")
	fmt.Printf("Total Processed: %d\n", totalProcessed)
	fmt.Printf("Updated:         %d\n", totalUpdated)
	fmt.Printf("Skipped:         %d\n", totalSkipped)
	fmt.Printf("Failed:          %d\n", totalFailed)

	return nil
}

// parseLocation parses "City, State" format
func parseLocation(location string) (city, state string, err error) {
	parts := strings.Split(location, ",")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid location format (expected 'City, State')")
	}

	city = strings.TrimSpace(parts[0])
	state = strings.TrimSpace(parts[1])

	return city, state, nil
}

// lookupCoordinates looks up lat/lon from city_states table in market database
func lookupCoordinates(ctx context.Context, marketQueries *marketqueries.Queries, city, state string) (lat, lon float64, err error) {
	if marketQueries == nil {
		return 0, 0, fmt.Errorf("market database not configured")
	}

	// Lookup city in city_states table
	cityState, err := marketQueries.GetCityByNameAndState(ctx, marketqueries.GetCityByNameAndStateParams{
		City:    city,
		StateID: state,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("city not found in database: %w", err)
	}

	return cityState.Latitude, cityState.Longitude, nil
}

// connectMainDB connects to the main PostgreSQL database
func connectMainDB() (*pgxpool.Pool, func()) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("Error: DATABASE_URL environment variable not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Fatalf("Error parsing database URL: %v", err)
	}
	poolConfig.MaxConns = 4

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatalf("Failed to connect to main database: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping main database: %v", err)
	}

	log.Println("Connected to main database")

	return pool, func() {
		pool.Close()
	}
}

// connectMarketDB connects to the market PostgreSQL database
func connectMarketDB() (*pgxpool.Pool, func()) {
	dbURL := os.Getenv("MARKET_DATABASE_URL")
	if dbURL == "" {
		log.Println("Market database not configured (MARKET_DATABASE_URL not set)")
		return nil, func() {}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Printf("Error parsing market database URL: %v", err)
		return nil, func() {}
	}
	poolConfig.MaxConns = 4

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Printf("Failed to connect to market database: %v", err)
		return nil, func() {}
	}

	if err := pool.Ping(ctx); err != nil {
		log.Printf("Failed to ping market database: %v", err)
		pool.Close()
		return nil, func() {}
	}

	log.Println("Connected to market database")

	return pool, func() {
		pool.Close()
	}
}
