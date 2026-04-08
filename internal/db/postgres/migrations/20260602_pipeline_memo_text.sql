-- ADR-109 addendum: persist generated decision memo text on pipeline_deals
-- Allows memo to survive page navigation without regenerating.

ALTER TABLE pipeline_deals
    ADD COLUMN IF NOT EXISTS memo_text TEXT;
