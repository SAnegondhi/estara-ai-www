-- Migration: 20260228_cron_external_job_id
-- Description: Adds external_job_id column to cron_job_configs for cron-job.org integration (ADR-099)
-- Author: Claude
-- Date: 2026-02-28

ALTER TABLE cron_job_configs ADD COLUMN IF NOT EXISTS external_job_id BIGINT;
COMMENT ON COLUMN cron_job_configs.external_job_id IS 'Job ID assigned by cron-job.org; NULL means not yet synced';
