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
lint-no-raw-sql:
	@echo "Checking for raw SQL pool access..."
	@FOUND=$$(grep -rn '\.Main\.\(Query\|QueryRow\|Exec\)\|\.Market\.\(Query\|QueryRow\|Exec\)' \
		internal/api/handlers/ internal/services/ \
		--include="*.go" | grep -v '_test.go' | grep -v 'store.go' || true); \
	if [ -n "$$FOUND" ]; then \
		echo "ERROR: Raw SQL found in handlers/services!"; \
		echo "$$FOUND"; \
		echo ""; \
		echo "All DB access must use Store.Q() or Store.MQ() (sqlc-generated queries)."; \
		exit 1; \
	fi
	@echo "OK: No raw SQL in handlers/services."

# Full CI pipeline
ci: verify lint-no-raw-sql build
	@echo "All checks passed."

# Clean generated code (for full regeneration)
clean:
	rm -f internal/db/queries/*.sql.go internal/db/queries/models.go internal/db/queries/querier.go
	rm -f internal/db/marketqueries/*.sql.go internal/db/marketqueries/models.go internal/db/marketqueries/querier.go
