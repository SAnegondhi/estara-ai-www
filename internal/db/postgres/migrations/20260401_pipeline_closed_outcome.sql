-- ADR-104: Add closed_outcome to pipeline_deals for retrospective analytics.
-- Values: acquired | rejected | other | NULL (deal not yet closed)

ALTER TABLE pipeline_deals
  ADD COLUMN IF NOT EXISTS closed_outcome TEXT;
