-- Migration: 20260213_seed_cron_job_configs
-- Description: Seeds cron_job_configs with all registered cron jobs
-- Author: Claude
-- Date: 2026-02-13

INSERT INTO cron_job_configs (id, name, description, schedule, endpoint, "isEnabled", "timeoutMs", "maxFailures", "createdAt", "updatedAt")
VALUES
    ('cron_market_alerts',       'market-alerts',         'Process pending market alerts',                '0 */6 * * *',  '/api/cron/market-alerts',         true, 300000, 3, NOW(), NOW()),
    ('cron_cache_cleanup',       'cache-cleanup',         'Clean up expired cache entries',               '0 3 * * *',    '/api/cron/cache-cleanup',         true, 300000, 3, NOW(), NOW()),
    ('cron_vendor_costs',        'vendor-costs',          'Aggregate vendor costs for reporting',         '0 1 * * *',    '/api/cron/vendor-costs',          true, 300000, 3, NOW(), NOW()),
    ('cron_renewal_reminders',   'renewal-reminders',     'Send subscription renewal reminders',          '0 9 * * *',    '/api/cron/renewal-reminders',     true, 300000, 3, NOW(), NOW()),
    ('cron_subscription_sync',   'subscription-sync',     'Sync subscriptions with Stripe',               '0 */4 * * *',  '/api/cron/subscription-sync',     true, 300000, 3, NOW(), NOW()),
    ('cron_usage_reset',         'usage-reset',           'Reset monthly usage counters',                 '0 0 1 * *',    '/api/cron/usage-reset',           true, 300000, 3, NOW(), NOW()),
    ('cron_stale_jobs',          'stale-jobs',            'Clean up stale/orphaned jobs',                 '0 2 * * *',    '/api/cron/stale-jobs',            true, 300000, 3, NOW(), NOW()),
    ('cron_audit_archive',       'audit-archive',         'Archive old audit logs',                       '0 4 * * 0',    '/api/cron/audit-archive',         true, 600000, 3, NOW(), NOW()),
    ('cron_market_data_refresh', 'market-data-refresh',   'Refresh market data timestamps',               '0 5 * * *',    '/api/cron/market-data-refresh',   true, 300000, 3, NOW(), NOW()),
    ('cron_property_cache',      'property-cache-prune',  'Prune old property cache entries',             '0 3 * * *',    '/api/cron/property-cache-prune',  true, 300000, 3, NOW(), NOW()),
    ('cron_expire_trials',       'expire-free-trials',    'Expire free trial subscriptions',              '0 0 * * *',    '/api/cron/expire-free-trials',    true, 300000, 3, NOW(), NOW()),
    ('cron_reports',             'reports',               'Process scheduled report generation',          '*/15 * * * *', '/api/cron/reports',               true, 600000, 3, NOW(), NOW()),
    ('cron_ai_estimates',        'ai-estimate-refresh',   'Refresh AI cost estimates',                    '0 6 * * *',    '/api/cron/ai-estimate-refresh',   true, 300000, 3, NOW(), NOW()),
    ('cron_guest_sessions',      'cleanup-guest-sessions','Remove expired guest sessions',                '0 */2 * * *',  '/api/cron/cleanup-guest-sessions',true, 300000, 3, NOW(), NOW()),
    ('cron_discovery_cleanup',   'discovery-cleanup',     'Archive/delete old discovery sessions',        '0 4 * * *',    '/api/cron/discovery-cleanup',     true, 300000, 3, NOW(), NOW()),
    ('cron_expire_iap',          'expire-iap-subscriptions','Expire IAP subscriptions past their date',  '0 */6 * * *',  '/api/cron/expire-iap-subscriptions', true, 300000, 3, NOW(), NOW()),
    -- Market data import jobs (ADR-075)
    ('cron_zillow_zhvi',         'zillow-zhvi',           'Import Zillow ZHVI data',                     '0 2 1 * *',    '/api/cron/market-data/zillow-zhvi',      true, 1800000, 3, NOW(), NOW()),
    ('cron_zillow_zori',         'zillow-zori',           'Import Zillow ZORI data',                     '0 2 1 * *',    '/api/cron/market-data/zillow-zori',      true, 1800000, 3, NOW(), NOW()),
    ('cron_zillow_forecasts',    'zillow-forecasts',      'Import Zillow forecast data',                 '0 3 1 * *',    '/api/cron/market-data/zillow-forecasts', true, 1800000, 3, NOW(), NOW()),
    ('cron_zillow_metrics',      'zillow-metrics',        'Import Zillow metrics data',                  '0 3 1 * *',    '/api/cron/market-data/zillow-metrics',   true, 1800000, 3, NOW(), NOW()),
    ('cron_redfin',              'redfin',                'Import Redfin data',                          '0 4 1 * *',    '/api/cron/market-data/redfin',           true, 1800000, 3, NOW(), NOW()),
    ('cron_compute_national',    'compute-national',      'Compute national market data aggregates',     '0 6 1 * *',    '/api/cron/market-data/compute-national', true, 600000, 3, NOW(), NOW()),
    ('cron_full_refresh',        'full-refresh',          'Full market data refresh (all sources)',       '0 1 15 * *',   '/api/cron/market-data/full-refresh',     true, 3600000, 3, NOW(), NOW())
ON CONFLICT (name) DO NOTHING;
