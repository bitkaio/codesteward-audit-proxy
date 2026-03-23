-- 005_add_user_team.sql
-- Adds user and team columns for IDE plugin identity injection.
-- user: developer identity (git email, username) from X-Audit-User header.
-- team: team/org identifier from X-Audit-Team header.

ALTER TABLE audit.audit_events ADD COLUMN IF NOT EXISTS `user` LowCardinality(String) DEFAULT '';
ALTER TABLE audit.audit_events ADD COLUMN IF NOT EXISTS team LowCardinality(String) DEFAULT '';
