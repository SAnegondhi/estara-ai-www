.PHONY: sqlc build verify lint-no-raw-sql ci clean

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
