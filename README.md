# codesteward-audit-proxy

![Codesteward](assets/codesteward-logo.png)

A zero-config reverse proxy that intercepts LLM API traffic from AI coding agents and writes structured audit logs to ClickHouse.

![Go version](https://img.shields.io/badge/go-1.22+-00ADD8?style=flat&logo=go)
![ClickHouse](https://img.shields.io/badge/ClickHouse-native-FFCC01?style=flat&logo=clickhouse)
![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-OTLP-6750D4?style=flat&logo=opentelemetry)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat)

---

## What it does

`codesteward-audit-proxy` sits between any AI coding agent (Claude Code, OpenAI Codex, Gemini CLI) and the upstream LLM API. It forwards every request and response transparently, and asynchronously extracts structured audit data — thinking blocks, assistant narration, and tool calls — into ClickHouse for later analysis.

**One environment variable is all that is required:**

```bash
ANTHROPIC_BASE_URL=http://localhost:8080 claude
```

The proxy is fully transparent. It never buffers the response before forwarding, never modifies headers or status codes, and never blocks agent operation due to audit backend failures.

---

## Features

- **Stream tap, never buffer** — uses `io.TeeReader` to forward tokens to the agent immediately while capturing a copy for audit asynchronously
- **Anthropic + OpenAI parsing** — extracts thinking blocks, text, and tool calls from both streaming (SSE) and non-streaming responses
- **Batched ClickHouse writes** — accumulates events in memory and flushes on size threshold (default 100) or time interval (default 1s)
- **Multi-tenancy** — `AUDIT_PROJECT` and `AUDIT_BRANCH` tag every row so multiple repos and branches share one ClickHouse instance
- **OpenTelemetry traces** — one span per proxied request with `gen_ai.system`, session/turn IDs, latency, and status; flush spans per ClickHouse batch; W3C trace context propagated in both directions
- **Proxy chaining** — supports `UPSTREAM_PROXY` for corporate firewalls, Portkey, LiteLLM, and other gateway proxies
- **Structured JSON logging** — every request, batch flush, and error logged via `log/slog`
- **Resilient by design** — ClickHouse or OTel unavailability is logged and discarded; the proxy never goes down because of a broken backend

---

## Architecture

```text
                        ┌──────────────────────────────┐
  Claude Code           │codesteward-audit-proxy :8080 │          Anthropic
  Codex        ──HTTP──►│                              │──HTTPS──► OpenAI
  Gemini CLI  ◄─stream──│      io.TeeReader tap        │◄─stream── Gemini
                        └──────────────┬───────────────┘
                                       │ async copy
                          ┌────────────┴────────────┐
                          │                         │
                          ▼                         ▼
                   ┌─────────────┐        ┌──────────────────┐
                   │   Batcher   │        │   OTel span      │
                   └──────┬──────┘        └────────┬─────────┘
                          │                        │
                          ▼                        ▼
                   ┌─────────────┐        ┌──────────────────┐
                   │  ClickHouse │        │  OTLP endpoint   │
                   │ audit_events│        │ (Jaeger / Tempo…)│
                   └─────────────┘        └──────────────────┘
```

---

## Quick start

**Prerequisites:** Go 1.22+, a running ClickHouse instance.

### 1. Apply the schema

```bash
clickhouse-client --multiquery < migrations/001_initial.sql
```

### 2. Start the proxy

```bash
CLICKHOUSE_DSN=clickhouse://localhost:9000/audit \
  go run ./cmd/proxy
```

### 3. Point your agent at the proxy

**Claude Code:**

```bash
ANTHROPIC_BASE_URL=http://localhost:8080 claude
```

**OpenAI SDK / Codex:**

```bash
OPENAI_BASE_URL=http://localhost:8080 codex
```

---

## Configuration

All configuration is via environment variables. No config files required.

| Variable | Default | Description |
| --- | --- | --- |
| `PROXY_ADDR` | `127.0.0.1:8080` | Listen address |
| `CLICKHOUSE_DSN` | *(required)* | ClickHouse connection string |
| `CLICKHOUSE_DB` | `audit` | Database name |
| `BATCH_SIZE` | `100` | Events per flush |
| `BATCH_INTERVAL` | `1s` | Max time between flushes |
| `AUDIT_PROJECT` | `""` | Repository / project name tagged on every row |
| `AUDIT_BRANCH` | auto-detected | Git branch; falls back to `git rev-parse` at startup |
| `UPSTREAM_PROXY` | *(none)* | Upstream proxy URL (overrides `HTTPS_PROXY`) |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | *(none)* | Activates OTel traces when set (e.g. `http://localhost:4318`) |
| `OTEL_SERVICE_NAME` | `codesteward-audit-proxy` | Service name in traces |

Standard `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_TIMEOUT`, and `OTEL_RESOURCE_ATTRIBUTES` are also honoured by the OTel SDK.

---

## OpenTelemetry

OTel is **off by default**. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable it — no other changes required.

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_SERVICE_NAME=codesteward-audit-proxy \
CLICKHOUSE_DSN=clickhouse://localhost:9000/audit \
  go run ./cmd/proxy
```

### What is traced

| Span | Attributes |
| --- | --- |
| `llm.proxy.request` | `gen_ai.system`, `llm.agent`, `audit.session_id`, `audit.turn_id`, `audit.project`, `audit.branch`, `http.request.method`, `url.path`, `http.response.status_code` |
| `audit.batch.flush` | `batch.size`, `db.system=clickhouse` |

**Span duration for `llm.proxy.request` covers the full streaming response** — from when the request is sent upstream to when the last token is delivered to the agent. This is the meaningful latency for LLM workloads.

### Trace context propagation

The proxy extracts W3C `traceparent`/`tracestate` headers from incoming agent requests (making the agent's span the parent when available) and injects them into outbound requests to the LLM API (useful when routing through an observability gateway like Portkey or Helicone).

---

## Multi-tenancy

`AUDIT_PROJECT` and `AUDIT_BRANCH` tag every ClickHouse row, allowing multiple repositories and branches to share one instance.

```bash
AUDIT_PROJECT=myorg/myrepo \
AUDIT_BRANCH=feature/refactor \
CLICKHOUSE_DSN=clickhouse://localhost:9000/audit \
  go run ./cmd/proxy
```

Example query across tenants:

```sql
SELECT project, branch, agent, tool_name, count() AS calls
FROM audit.audit_events
WHERE toDate(ts) = today()
GROUP BY project, branch, agent, tool_name
ORDER BY calls DESC;
```

---

## Upstream routing

The proxy detects the upstream from the `Host` header first, then the request path:

| Host / Path prefix | Upstream |
| --- | --- |
| `api.anthropic.com` or `/v1/messages` | `https://api.anthropic.com` |
| `api.openai.com` or `/v1/chat/` | `https://api.openai.com` |
| `generativelanguage.googleapis.com` or `/v1beta/` | `https://generativelanguage.googleapis.com` |

---

## Proxy chaining

The proxy can forward its outbound connections through an upstream proxy — useful for corporate firewalls, [Portkey](https://portkey.ai), [LiteLLM](https://litellm.ai), or [Helicone](https://helicone.ai).

```text
Agent → [codesteward-audit-proxy :8080] → [Upstream Proxy] → LLM API
```

Resolution order: `UPSTREAM_PROXY` env var → `HTTPS_PROXY`/`HTTP_PROXY` → direct. Supports `http://`, `https://`, and `socks5://` schemes.

```bash
UPSTREAM_PROXY=http://localhost:4000 \
  CLICKHOUSE_DSN=clickhouse://localhost:9000/audit \
  go run ./cmd/proxy
```

---

## ClickHouse schema

One row per tool call. Responses with no tool calls produce a single row with `tool_name = ''`.

```sql
CREATE TABLE audit.audit_events
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
    tool_input      String,   -- JSON-encoded tool input
    model           LowCardinality(String),
    raw             String    -- full original response body
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(ts)
ORDER BY (project, session_id, ts);
```

Existing installations: apply `migrations/002_add_branch.sql` to add the `branch` column.

---

## Repository structure

```text
├── cmd/proxy/main.go                 Entry point, wiring, graceful shutdown
├── internal/
│   ├── config/config.go              Env-var config loading, git branch detection
│   ├── telemetry/otel.go             OTel TracerProvider setup (no-op when unconfigured)
│   ├── audit/
│   │   ├── event.go                  AuditEvent struct + EventAdder interface
│   │   ├── batcher.go                In-memory batcher (size + interval flush)
│   │   └── clickhouse.go             ClickHouse native-protocol writer
│   ├── proxy/
│   │   ├── handler.go                Reverse proxy handler, audit transport, OTel spans
│   │   ├── router.go                 Upstream detection and URL rewriting
│   │   ├── stream.go                 TeeReader stream tap
│   │   └── transport.go              http.Transport with proxy chaining
│   └── parser/
│       ├── anthropic.go              Anthropic message + SSE stream parser
│       ├── openai.go                 OpenAI chat completion + SSE stream parser
│       └── gemini.go                 Gemini stub (TODO)
└── migrations/
    ├── 001_initial.sql               Full schema for new installations
    └── 002_add_branch.sql            Add branch column to existing installations
```

---

## Running tests

```bash
go test ./...
```

Tests cover: Anthropic and OpenAI parsing (full and streaming, including edge cases), batcher (size-threshold flush, ticker flush, drain on stop, non-blocking drop), router (host-based, path-based, header-based routing, URL rewriting), stream tap (byte fidelity, SSE detection, callback timing), and the handler end-to-end (status/body/header passthrough, internal header scrubbing, audit event emission, 502 on dead upstream).

---

## Security notes

- The proxy binds to `127.0.0.1` by default. API keys travel in plaintext on the agent→proxy leg. This is safe on localhost; for multi-host deployments, add mTLS on this leg.
- API keys in request headers (`Authorization`, `x-api-key`) are forwarded to the upstream as-is but are **never stored** in audit records.
