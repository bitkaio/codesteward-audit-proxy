-- Migration 002: add branch column for git branch tracking.
-- Apply to existing installations with:
--   clickhouse-client --multiquery < migrations/002_add_branch.sql

ALTER TABLE audit.audit_events
    ADD COLUMN IF NOT EXISTS branch LowCardinality(String) DEFAULT '' AFTER project;
