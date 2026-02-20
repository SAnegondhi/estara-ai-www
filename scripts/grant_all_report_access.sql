-- Grant access to all market analysis reports for a specific user
-- Usage: Replace 'YOUR_USER_ID' with the actual user ID from users table

INSERT INTO user_market_analysis_access (id, user_id, report_id, accessed_at)
SELECT
    gen_random_uuid()::text,
    'YOUR_USER_ID', -- CHANGE THIS
    mar.id,
    NOW()
FROM market_analysis_reports mar
WHERE NOT EXISTS (
    SELECT 1 FROM user_market_analysis_access
    WHERE user_id = 'YOUR_USER_ID' AND report_id = mar.id
);
