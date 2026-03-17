-- Migration 003: add request capture columns
-- Apply to existing installations that already have the schema from 001 + 002.

ALTER TABLE audit.audit_events
    ADD COLUMN IF NOT EXISTS request_captured  UInt8         DEFAULT 1;

ALTER TABLE audit.audit_events
    ADD COLUMN IF NOT EXISTS user_messages     Array(String) DEFAULT [];
