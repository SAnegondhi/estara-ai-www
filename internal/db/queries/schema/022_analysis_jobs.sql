-- Analysis Jobs (for sqlc validation)
-- Note: This table is managed by Prisma/legacy migrations, schema is for sqlc only

-- Enum types (must match database)
DO $$ BEGIN
    CREATE TYPE "AnalysisJobType" AS ENUM (
        'MARKET_ANALYSIS',
        'INVESTMENT_PLANNING',
        'INVESTOR_REPORT',
        'EVALUATION_CHAT'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE "AnalysisStatus" AS ENUM (
        'QUEUED',
        'AGENT1_RUNNING',
        'AGENT1_COMPLETE',
        'AGENT2_RUNNING',
        'AGENT2_COMPLETE',
        'STORING',
        'COMPLETED',
        'FAILED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS analysis_jobs (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL,
    "jobType" "AnalysisJobType" NOT NULL DEFAULT 'MARKET_ANALYSIS',
    status "AnalysisStatus" NOT NULL DEFAULT 'QUEUED',
    progress INTEGER NOT NULL DEFAULT 0,
    location TEXT NOT NULL,
    criteria JSONB,
    "metricsData" JSONB,
    "metricsError" TEXT,
    "narrativeData" JSONB,
    "narrativeError" TEXT,
    "reportId" TEXT,
    "queuedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "startedAt" TIMESTAMP(3),
    "completedAt" TIMESTAMP(3),
    "dismissedAt" TIMESTAMP(3),
    error TEXT,
    "retryCount" INTEGER NOT NULL DEFAULT 0
);
