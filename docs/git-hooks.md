# Git Hooks for www/

Pre-commit hooks that enforce code quality and catch migration errors before deployment.

## Quick Start

```bash
cd www/
./scripts/install-hooks.sh
```

That's it! The hook will now run automatically before every commit.

## What the Hook Checks

### 1. sqlc generate ✅
- Validates all SQL queries against schema files
- Catches column name typos, type mismatches, missing tables
- Regenerates Go code with latest schema changes

**Why**: ADR-083 mandates sqlc as the only database interface. This ensures all application queries are type-safe at compile time.

### 2. go build ./... ✅
- Ensures all Go code compiles without errors
- Catches type errors, missing imports, syntax issues
- Verifies sqlc-generated code is valid

**Why**: Zero tolerance for build errors. If it doesn't compile, it doesn't get committed.

### 3. Migration Safety Checks ⚠️
- Warns about migrations missing idempotency checks (`IF NOT EXISTS`)
- Warns about unsafe enum casting
- Warns about missing migration headers
- Reminds you to test migrations locally

**Why**: Migrations are runtime operations that can fail in production. These warnings catch common mistakes.

### 4. go fmt 💅
- Ensures consistent code formatting
- Checks all Go files (except sqlc-generated code)

**Why**: Maintain consistent code style across the codebase.

## Migration Workflow (Critical!)

### ❌ Wrong Workflow (What We Were Doing):
```bash
1. Write migration file
2. git commit
3. git push
4. Deploy → Migration fails! 😱
```

### ✅ Correct Workflow (What to Do):

```bash
# 1. Write migration
vim internal/db/postgres/migrations/20260220_my_migration.sql

# 2. TEST LOCALLY FIRST!
go run ./cmd/www
# Check logs for: "failed to run migrations"
# If error → fix migration → restart

# 3. Update schema to match new state
vim internal/db/queries/schema/007_admin.sql

# 4. Let pre-commit hook do its job
git add -A
git commit -m "feat: add my migration"
# Hook runs automatically:
#   ✅ sqlc generate
#   ✅ go build ./...
#   ⚠️  Migration safety checks
#   ✅ go fmt

# 5. Push
git push origin master
```

## Understanding Migration Warnings

### Warning: "adds columns without IF NOT EXISTS check"

❌ Unsafe (fails on re-run):
```sql
ALTER TABLE audit_logs ADD COLUMN "actorType" "AuditActorType";
```

✅ Safe (idempotent):
```sql
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'audit_logs' AND column_name = 'actorType') THEN
        ALTER TABLE audit_logs ADD COLUMN "actorType" "AuditActorType";
    END IF;
END $$;
```

### Warning: "modifies enum without existence check"

❌ Unsafe (fails if value exists):
```sql
ALTER TYPE "AuditEventType" ADD VALUE 'ADMIN_ACTION';
```

✅ Safe (idempotent):
```sql
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum e
        JOIN pg_type t ON e.enumtypid = t.oid
        WHERE t.typname = 'AuditEventType' AND e.enumlabel = 'ADMIN_ACTION'
    ) THEN
        ALTER TYPE "AuditEventType" ADD VALUE 'ADMIN_ACTION';
    END IF;
END $$;
```

### Warning: "casts directly between types"

❌ Unsafe (runtime error if types incompatible):
```sql
SELECT action::"AuditEventType" as event FROM admin_audit_log;
```

✅ Safe (explicit text intermediate):
```sql
SELECT 'ADMIN_ACTION'::"AuditEventType" as event FROM admin_audit_log;
```

## Bypassing the Hook (Not Recommended)

If you absolutely must bypass the hook (e.g., emergency hotfix):

```bash
git commit --no-verify -m "hotfix: emergency fix"
```

**Warning**: Only use this for genuine emergencies. Bypassing the hook means:
- No compile-time validation
- No migration safety checks
- Higher risk of runtime failures

## Uninstalling the Hook

```bash
rm .git/hooks/pre-commit
```

## Troubleshooting

### Hook doesn't run

Check if it's executable:
```bash
ls -l .git/hooks/pre-commit
# Should show: -rwxr-xr-x
```

If not, make it executable:
```bash
chmod +x .git/hooks/pre-commit
```

### "sqlc: command not found"

Install sqlc:
```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

### Hook runs but still get migration errors

The hook can't test migrations against your actual database. It only checks for common issues.

**You must still test migrations locally by running the server before committing.**

## FAQ

### Q: Why can't the hook catch all migration errors?

**A**: Migrations modify the database schema itself. You can't validate SQL against a schema that doesn't exist yet. The hook catches common issues, but you must test against a real database.

### Q: Do I need to run the hook manually?

**A**: No! It runs automatically when you `git commit`. You'll see output like:

```
🔍 Running pre-commit checks for www...
📋 Step 1/4: Running sqlc generate...
✅ sqlc generate passed
🔨 Step 2/4: Running go build...
✅ go build passed
🔍 Step 3/4: Checking migration files...
✅ Migration checks passed
💅 Step 4/4: Checking go fmt...
✅ go fmt passed

✅ All pre-commit checks passed!
```

### Q: What if I commit from a different directory?

**A**: The hook only runs when committing from the `www/` directory. Commits from other repos (client, website, admin) won't trigger this hook.

### Q: Can I customize the checks?

**A**: Yes! Edit `scripts/pre-commit` and re-run `./scripts/install-hooks.sh`.

## Related Documentation

- [ADR-083: Single Auditable Database Interface](../estara-ai-docs/project/decisions/ADR-083-single-auditable-database-interface.md)
- [sqlc Guide](./sqlc-guide.md)
- [Migration Best Practices](./migrations.md)
