// bootstrap - First-run superadmin setup CLI
//
// Creates the initial SUPER_ADMIN user and sends a password setup email.
// Run this once against a fresh database after the server has started
// (migrations run automatically on server start).
//
// Usage:
//
//	go run cmd/bootstrap/main.go
//	go run cmd/bootstrap/main.go --email=admin@example.com --first-name=Jane --last-name=Doe
//	go run cmd/bootstrap/main.go --check
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/joho/godotenv"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/email"
)

func main() {
	// Load environment
	if err := godotenv.Load(".env.local"); err != nil {
		_ = godotenv.Load(".env")
	}

	email_ := flag.String("email", "sudhindra@estara-ai.com", "Superadmin email address")
	firstName := flag.String("first-name", "Sudhindra", "First name")
	lastName := flag.String("last-name", "", "Last name")
	check := flag.Bool("check", false, "Check if superadmin exists without creating")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fatalf("failed to load config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to main database (migrations already run by server startup)
	mainPool, cleanup, err := connectDB(ctx, cfg)
	if err != nil {
		fatalf("failed to connect to database: %v", err)
	}
	defer cleanup()

	q := queries.New(mainPool)

	if *check {
		runCheck(ctx, q)
		return
	}

	runBootstrap(ctx, cfg, q, *email_, *firstName, *lastName)
}

func runCheck(ctx context.Context, q *queries.Queries) {
	fmt.Println("Checking for existing superadmin...")

	users, err := q.ListUsers(ctx, queries.ListUsersParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		fatalf("failed to query users: %v", err)
	}

	found := false
	for _, u := range users {
		if fmt.Sprint(u.Role) == "SUPER_ADMIN" {
			found = true
			fmt.Printf("  SUPER_ADMIN found: %s (id: %s, created: %s)\n",
				u.Email, u.ID, u.CreatedAt.Time.Format("2006-01-02 15:04:05"))
		}
	}

	if !found {
		fmt.Println("  No SUPER_ADMIN found — run without --check to create one.")
		os.Exit(1)
	}
}

func runBootstrap(ctx context.Context, cfg *config.Config, q *queries.Queries, adminEmail, firstName, lastName string) {
	adminEmail = strings.TrimSpace(adminEmail)
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)

	if adminEmail == "" {
		fatalf("email is required")
	}

	fmt.Printf("Bootstrapping superadmin: %s\n\n", adminEmail)

	// Check if user already exists
	existing, err := q.GetUserByEmail(ctx, adminEmail)
	if err != nil && err != pgx.ErrNoRows {
		fatalf("failed to check existing user: %v", err)
	}

	var userID string

	if err == nil {
		// User exists
		userID = existing.ID
		existingRole := string(existing.Role)
		if existingRole == "SUPER_ADMIN" {
			fmt.Printf("  User already exists with SUPER_ADMIN role (id: %s)\n", userID)
			fmt.Println("  Sending a fresh password setup email...")
		} else {
			fatalf("user %s already exists with role %s — cannot bootstrap over an existing non-superadmin user",
				adminEmail, existingRole)
		}
	} else {
		// Create user (no password yet — set via email link)
		userID = uuid.New().String()

		fn := pgtype.Text{}
		if firstName != "" {
			fn = pgtype.Text{String: firstName, Valid: true}
		}
		ln := pgtype.Text{}
		if lastName != "" {
			ln = pgtype.Text{String: lastName, Valid: true}
		}

		newUser, err := q.CreateUser(ctx, queries.CreateUserParams{
			ID:               userID,
			Email:            adminEmail,
			FirstName:        fn,
			LastName:         ln,
			Role:             "SUPER_ADMIN",
			SubscriptionTier: pgtype.Text{String: "admin", Valid: true},
		})
		if err != nil {
			fatalf("failed to create user: %v", err)
		}
		userID = newUser.ID
		fmt.Printf("  Created SUPER_ADMIN user: %s (id: %s)\n", adminEmail, userID)
	}

	// Generate setup token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		fatalf("failed to generate token: %v", err)
	}
	setupToken := hex.EncodeToString(tokenBytes)
	tokenID := uuid.New().String()

	_, err = q.CreatePasswordSetupToken(ctx, queries.CreatePasswordSetupTokenParams{
		ID:        tokenID,
		UserID:    userID,
		Token:     setupToken,
		ExpiresAt: time.Now().Add(72 * time.Hour),
	})
	if err != nil {
		fatalf("failed to create setup token: %v", err)
	}
	fmt.Println("  Created password setup token (valid 72 hours)")

	// Build setup URL
	clientURL := cfg.Server.ClientURL
	if clientURL == "" {
		clientURL = "http://localhost:3002"
	}
	setupURL := fmt.Sprintf("%s/setup-password?token=%s", clientURL, setupToken)

	// Send email
	emailSvc := email.NewService(cfg)
	result, err := emailSvc.SendEarlyAccessWelcome(adminEmail, firstName, setupURL)
	if err != nil {
		fmt.Printf("\n  WARNING: failed to send email: %v\n", err)
		fmt.Printf("  Setup URL (use this manually):\n  %s\n", setupURL)
		os.Exit(0)
	}
	if !result.Success {
		fmt.Printf("\n  WARNING: email not sent (%s)\n", result.Error)
		fmt.Printf("  Setup URL (use this manually):\n  %s\n", setupURL)
		os.Exit(0)
	}

	fmt.Printf("\n  Email sent to %s\n", adminEmail)
	fmt.Println("\nBootstrap complete.")
	fmt.Println("The superadmin will receive an email to set their password.")
	fmt.Printf("Token expires: %s\n", time.Now().Add(72*time.Hour).Format("2006-01-02 15:04 MST"))
}

func connectDB(ctx context.Context, cfg *config.Config) (*postgres.Pool, func(), error) {
	if cfg.Database.URL == "" {
		return nil, nil, fmt.Errorf("DATABASE_URL not set")
	}

	pool, err := postgres.NewPool(ctx, cfg.Database, "main")
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}

	// Also run migrations so this tool works against a truly empty DB
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		fmt.Printf("  WARNING: migration warning: %v\n", err)
	}

	return pool, func() { pool.Close() }, nil
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
