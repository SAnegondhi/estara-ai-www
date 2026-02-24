-- ADR-098 addendum: Market context snapshot at evaluation time
-- Stores aggregated market data (cap rate, mortgage rate, rent, vacancy, etc.)
-- at the moment the evaluation was saved, so playback shows conditions-at-time-of-decision.
ALTER TABLE v2_evaluations
ADD COLUMN IF NOT EXISTS market_context JSONB;

-- Backfill discovery_session_id for pre-ADR-098 evaluations using the same
-- most-recent-match logic that the LATERAL JOIN used. After this runs,
-- the LATERAL JOIN in GetEvaluationsWithDecisionRecords is redundant for all rows.
UPDATE v2_evaluations e
SET discovery_session_id = dse."discoverySessionId"
FROM (
    SELECT DISTINCT ON ("propertyId")
        "discoverySessionId",
        "propertyId"
    FROM discovery_session_evaluations
    ORDER BY "propertyId", "createdAt" DESC
) dse
WHERE dse."propertyId" = e.property_id
  AND e.discovery_session_id IS NULL;
