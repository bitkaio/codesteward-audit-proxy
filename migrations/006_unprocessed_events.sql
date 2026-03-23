-- 006_unprocessed_events.sql
-- Separate table for requests/responses the proxy could not parse into
-- structured audit events. Captures raw data for debugging parsing issues
-- and discovering new API endpoints used by agents.

CREATE TABLE IF NOT EXISTS audit.unprocessed_events (
    session_id    String,
    turn_id       String,
    ts            DateTime64(3),
    agent         LowCardinality(String),
    project       String,
    branch        LowCardinality(String),
    `user`        LowCardinality(String)  DEFAULT '',
    team          LowCardinality(String)  DEFAULT '',
    direction     LowCardinality(String),
    method        LowCardinality(String),
    path          String,
    status_code   UInt16                  DEFAULT 0,
    content_type  LowCardinality(String)  DEFAULT '',
    raw           String,
    error         String                  DEFAULT ''
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(ts)
ORDER BY (project, session_id, ts);
