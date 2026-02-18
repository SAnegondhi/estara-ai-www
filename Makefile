.PHONY: sqlc build verify lint-no-raw-sql ci clean test test-integration test-unit test-coverage

# Regenerate all sqlc code from schemas + queries
sqlc:
	sqlc generate

# Full build: generate + compile
build: sqlc
	go build ./...

# Verify committed code matches what sqlc would generate
verify:
	@echo "Checking sqlc drift..."
	@sqlc generate
	@if [ -n "$$(git diff --name-only internal/db/queries/ internal/db/marketqueries/)" ]; then \
		echo "ERROR: sqlc drift detected! Generated code differs from committed."; \
		echo ""; \
		git diff --stat internal/db/queries/ internal/db/marketqueries/; \
		echo ""; \
		echo "Run 'make build' and commit the changes."; \
		exit 1; \
	fi
	@echo "OK: No sqlc drift."

# Ensure no raw SQL bypasses sqlc in handler/service code
# ADR-083: All database queries must use sqlc-generated code via Q() or MQ()
lint-no-raw-sql:
	@echo "Checking for raw SQL pool access..."
	@FOUND=$$(grep -rn 'Pool()\.\(Query\|QueryRow\|Exec\)\|MarketPool()\.\(Query\|QueryRow\|Exec\)' \
		internal/api/handlers/ internal/services/ \
		--include="*.go" | grep -v '_test.go' | grep -v 'store.go' || true); \
	if [ -n "$$FOUND" ]; then \
		echo "ERROR: Raw SQL database queries found!"; \
		echo "$$FOUND"; \
		echo ""; \
		echo "All DB queries must use Store.Q() or Store.MQ() (sqlc-generated)."; \
		echo "Pool() may only be used for monitoring: Ping(), Stat()"; \
		exit 1; \
	fi
	@echo "OK: No raw SQL database queries found."
	@echo "Note: Pool().Ping() and Pool().Stat() are allowed for monitoring."

# Full CI pipeline
ci: verify lint-no-raw-sql build
	@echo "All checks passed."

# Clean generated code (for full regeneration)
clean:
	rm -f internal/db/queries/*.sql.go internal/db/queries/models.go internal/db/queries/querier.go
	rm -f internal/db/marketqueries/*.sql.go internal/db/marketqueries/models.go internal/db/marketqueries/querier.go

# Run all tests (unit + integration)
test:
	@echo "Running all tests..."
	go test ./... -v

# Run integration tests (requires test database)
test-integration:
	@echo "Running integration tests..."
	@if [ ! -f .env.test ]; then \
		echo "ERROR: .env.test file not found"; \
		echo "Run: ./tests/scripts/setup-test-db.sh to create it"; \
		exit 1; \
	fi
	@set -a && . ./.env.test && set +a && go test -v ./tests/integration -count=1

# Run unit tests only (no database required)
test-unit:
	@echo "Running unit tests..."
	go test -v -short ./internal/...

# Generate test coverage report
test-coverage:
	@echo "Generating coverage report..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
	@echo "Open with: open coverage.html"

# Generate comprehensive test report with summary
test-report:
	@echo "Generating comprehensive test report..."
	@if [ ! -f .env.test ]; then \
		echo "ERROR: .env.test file not found"; \
		echo "Run: ./tests/scripts/setup-test-db.sh to create it"; \
		exit 1; \
	fi
	@set -a && . ./.env.test && set +a && ./tests/scripts/test-report.sh
