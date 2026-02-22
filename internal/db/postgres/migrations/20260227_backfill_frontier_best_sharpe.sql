-- ADR-089: Backfill best_sharpe from stored frontier_points JSONB.
-- Runs saved before the strategy/best_sharpe columns were added have best_sharpe = 0.
-- This migration computes the correct value from the stored JSONB for those rows.
UPDATE frontier_runs
SET best_sharpe = subq.max_sharpe
FROM (
    SELECT
        fr.id,
        MAX((fp->>'sharpeScore')::double precision) AS max_sharpe
    FROM frontier_runs fr,
         jsonb_array_elements(fr.frontier_points) AS fp
    WHERE fr.best_sharpe = 0
      AND jsonb_array_length(fr.frontier_points) > 0
    GROUP BY fr.id
) AS subq
WHERE frontier_runs.id = subq.id
  AND subq.max_sharpe > 0;
