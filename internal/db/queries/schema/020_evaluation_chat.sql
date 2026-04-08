-- Evaluation Chat Sessions and Messages (for sqlc validation)
-- Note: These tables are managed by Prisma/legacy migrations, schema is for sqlc only

CREATE TABLE IF NOT EXISTS evaluation_chat_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    property_ids TEXT[] DEFAULT '{}',
    cached_property_ids TEXT[] DEFAULT '{}',
    investor_profile JSONB,
    portfolio_snapshot JSONB,
    discovery_session_id TEXT,
    pipeline_deal_id UUID,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS evaluation_chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    parsed_blocks JSONB,
    token_usage JSONB,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
