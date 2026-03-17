-- ClickHouse schema for llm-audit-proxy audit events.
-- Apply with: clickhouse-client --multiquery < migrations/001_initial.sql

CREATE DATABASE IF NOT EXISTS audit;

CREATE TABLE IF NOT EXISTS audit.audit_events
(
    session_id      String,
    turn_id         String,
    ts              DateTime64(3),
    agent           LowCardinality(String),
    project         String,
    branch          LowCardinality(String),
    direction       LowCardinality(String),
    thinking        Array(String),
    assistant_text  Array(String),
    tool_name       String,
    tool_input      String,
    model           LowCardinality(String),
    raw             String
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(ts)
ORDER BY (project, session_id, ts);
