# www/docs

Documentation for the Go backend (`www/`).

## Key Documents

- **[sqlc-guide.md](sqlc-guide.md)** - MANDATORY: Type-safe database queries with sqlc
- **[CLAUDE.md](../CLAUDE.md)** - Local rules for www/ development

## Directories

- `implementation/` - Implementation plans and execution notes
- `changelogs/` - Monthly changelog entries for all behavior/code changes

## Build Requirements

Before every commit:
```bash
sqlc generate      # Generate type-safe query code
go build ./...     # Compile without errors
```

