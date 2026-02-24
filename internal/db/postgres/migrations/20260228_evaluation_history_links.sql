-- ADR-098: Add direct FK links for evaluation history completeness
-- Enables: chat session linked to discovery session; evaluations linked to chat + discovery

-- Link evaluation_chat_sessions to the discovery session that supplied the property pool
ALTER TABLE evaluation_chat_sessions
ADD COLUMN IF NOT EXISTS discovery_session_id TEXT;

-- Link v2_evaluations to chat session and discovery session directly
-- (replaces the fragile LATERAL JOIN through discovery_session_evaluations)
ALTER TABLE v2_evaluations
ADD COLUMN IF NOT EXISTS chat_session_id TEXT,
ADD COLUMN IF NOT EXISTS discovery_session_id TEXT;
