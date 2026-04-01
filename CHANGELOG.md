# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.1] - 2026-04-01

### Security

- Upgraded `google.golang.org/grpc` from v1.79.2 to v1.79.3 — fixes an authorization bypass via missing leading slash in the HTTP/2 `:path` pseudo-header (CVSS 9.1, Critical)
- Upgraded `github.com/ClickHouse/clickhouse-go/v2` from v2.26.0 to v2.44.0 and `github.com/ClickHouse/ch-go` from v0.61.5 to v0.71.0 — fixes a query smuggling vulnerability where large uncompressed malicious external data could be used to inject a query packet into the connection stream (CVE-2025-1386, CVSS 5.9, Moderate)

### Fixed

- License badge in README now correctly shows BSD-3-Clause (was incorrectly showing MIT)

## [1.0.0] - 2026-03-24

### Added

- **Token usage extraction** — `input_tokens`, `output_tokens`, `cache_read_tokens`, and `cache_write_tokens` captured from every LLM response and stored in ClickHouse; supports Anthropic (including cache tokens), OpenAI, and SAP AI Core in both streaming and non-streaming modes
- **OTel metric counters** — `gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` emitted as Int64Counter metrics with `gen_ai.system`, `llm.agent`, `gen_ai.response.model`, and `audit.project` dimensions; no-op when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset
- **OTel MeterProvider** — OTLP/HTTP metric exporter set up alongside existing TracerProvider; both shut down cleanly on graceful stop
- `TokenUsage` shared type in `parser/types.go` — used by all parser result types
- Token usage span attributes — `gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` added to `llm.proxy.request` spans
- Migration `007_add_token_usage.sql` — adds `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens` columns to existing installations
- Dashboard design documentation (`docs/dashboard/`) — comprehensive design spec and implementation guide for a Nuxt 4 + Vue 3 web UI covering session browsing, event inspection, token usage analytics, and proxy health monitoring
- Parser tests for token usage extraction (Anthropic full + streaming, OpenAI full + streaming)
- Handler test for end-to-end token usage flow

### Changed

- Release pipeline now extracts release notes from `CHANGELOG.md` instead of auto-generating from git log; falls back to git log if no entry exists for the tagged version

## [0.4.0] - 2026-03-23

### Added

- **IDE plugin header support** — `X-Audit-User`, `X-Audit-Team`, `X-Audit-Project`, `X-Audit-Branch`, `X-Audit-Session-ID`, and `X-Audit-Agent` headers allow IDE companion plugins to inject per-request identity and context; header values override env-var defaults (`AUDIT_PROJECT`, `AUDIT_BRANCH`) for centrally-hosted deployments
- **Unprocessed events table** — responses the proxy cannot parse into structured audit records (non-chat endpoints like `/v1/models` and `/v1/count_tokens`, unknown providers, parse errors) are routed to `audit.unprocessed_events` instead of being silently dropped; stores raw body, HTTP method, path, status code, content type, and parse error message
- **Health endpoint** — `GET /healthz` returns `{"status":"ok","version":"..."}` for IDE plugin connectivity checks and load balancer probes; version set at build time via `-ldflags "-X main.version=..."`
- `user` and `team` columns on `audit_events` — `LowCardinality(String)`, populated from `X-Audit-User` and `X-Audit-Team` headers
- `UnprocessedAdder` interface and `UnprocessedBatcher` — mirrors `EventAdder`/`Batcher` pattern for the unprocessed events write path
- `ANTHROPIC_UPSTREAM_URL`, `OPENAI_UPSTREAM_URL`, `GEMINI_UPSTREAM_URL` env vars — override the default upstream target for each provider without changing detection logic; useful for routing through LiteLLM, Portkey, a regional endpoint, or any internal gateway
- `NewRouterWithConfig(RouterConfig)` constructor on `Router` — accepts upstream URL overrides alongside SAP AI Core config; `NewRouter` preserved as backwards-compatible wrapper
- Migration `005_add_user_team.sql` — adds `user` and `team` columns to existing installations
- Migration `006_unprocessed_events.sql` — creates the `unprocessed_events` table
- Cline agent detection — `detectAgent` now matches `cline` in the User-Agent header
- Claude Code VSCode extension detection — `detectAgent` now matches `claude-cli` User-Agent sent by the VSCode extension (in addition to `claude-code` from the CLI)
- Session ID priority chain: `X-Audit-Session-ID` > `X-Session-ID` > auto-generated UUID
- IDE plugins design document (`docs/ide-plugins-design.md`) — comprehensive design for VSCode and JetBrains companion plugins covering env var injection, config file schema, and header protocol
- New handler tests: `X-Audit-*` header overrides, session ID fallback, Cline detection, Claude VSCode extension detection, unprocessed event routing

### Changed

- `extractEvents` now returns `([]audit.AuditEvent, error)` instead of `[]audit.AuditEvent` — parse errors are surfaced to the caller for routing to the unprocessed table
- `NewHandler` signature extended with `unprocessed audit.UnprocessedAdder` parameter
- `requestMeta` struct extended with `user`, `team`, `method`, and `unprocessed` fields

## [0.3.0] - 2026-03-18

### Added

- `migrations/migrate.sh` — idempotent HTTP-based migration runner; uses `curl` against the ClickHouse HTTP interface (port 8123) so no `clickhouse-client` binary is required; tracks applied versions in `<db>.schema_migrations` using a `ReplacingMergeTree` table; splits multi-statement files on semicolons via `awk` and executes each statement individually
- `migrate` service in `docker-compose.yml` — runs on every `docker compose up` using `alpine:3.21`; applies only pending migrations; the `proxy` service starts only after the migrate service completes successfully

### Fixed

- Compressed response body in `raw` field — the Claude Code VSCode extension (and TypeScript SDK) explicitly sets `Accept-Encoding: gzip, deflate, br`; when forwarded verbatim, Go's `http.Transport` treats compression as application-managed and does not decompress the response, causing the audit `raw` field to contain binary data and `thinking`/`assistant_text` to be empty; fixed by adding `Accept-Encoding` to the stripped headers list so `http.Transport` negotiates encoding itself and transparently decompresses before the `TeeReader` sees the body
- Agent detection for VSCode extension — `detectAgent` used a case-sensitive `strings.Contains` check; the Claude Code VSCode extension sends `Claude-Code` (capitalised) which fell through to `"unknown"`; fixed by applying `strings.ToLower` before comparison
- `003_request_capture.sql` — both `ALTER TABLE` statements used unqualified table name `audit_events`; the old `clickhouse-client --database=audit` flag set an implicit database context that the HTTP-based migration runner does not; fixed by using fully qualified `audit.audit_events`

## [0.2.0] - 2026-03-17

### Added

- SAP AI Core upstream support — routes requests to a configurable SAP AI Core base URL; resource group forwarded via `AI-Resource-Group` header
- `Router` struct encapsulating upstream detection logic; `NewRouter(sapBaseURL, sapAuthHost)` constructor replaces the previous package-level `DetectUpstream` function
- Request body capture with configurable scrubbing pipeline (`AUDIT_CAPTURE_REQUESTS`, `AUDIT_SCRUB_PATTERNS`)
- `PatternScrubber` — compiles Go regexp patterns at startup, replaces matches with `[REDACTED]` before storage
- `user_messages` field: structured, queryable extraction of user-role message content from request bodies
- `request_captured` field: boolean flag per audit row indicating whether request body was stored
- Migration `003_request_capture.sql` adding the two new columns to existing installations
- Dockerfile — multi-stage build (`golang:1.25-alpine` → `gcr.io/distroless/static:nonroot`), fully static binary, non-root runtime
- `docker-compose.yml` — local development stack with ClickHouse; migrations auto-applied on first start
- GitHub Actions CI workflow — `go vet`, `go test -race`, build verification, multi-arch Docker build check on every push and PR
- GitHub Actions release workflow — multi-arch Docker image pushed to GHCR, static binaries for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, auto-generated changelog, GitHub Release with SHA-256 checksums

### Fixed

- `handler_test.go` — updated two `NewHandler` call sites to pass the required `*Router` argument introduced by the SAP AI Core feature
- `router_test.go` — updated all `DetectUpstream` calls to use `defaultRouter.DetectUpstream()` following the promotion of `DetectUpstream` from a package-level function to a `*Router` method

## [0.1.0] - 2026-03-17

Initial release.

### Added

- Transparent reverse proxy supporting Anthropic, OpenAI, and Gemini upstream APIs
- Automatic upstream routing by `Host` header, URL path prefix, and `anthropic-version` header hint
- `io.TeeReader` stream tap — tokens forwarded to the agent immediately; audit copy consumed asynchronously
- Anthropic SSE/streaming parser — reassembles `content_block_start` / `content_block_delta` / `content_block_stop` events including thinking blocks and tool-input JSON fragments
- OpenAI SSE/streaming parser — accumulates tool call argument fragments by index across `data:` chunks
- Structured audit extraction: `thinking`, `assistant_text`, `tool_name`, `tool_input`, `model` per response
- One audit row per tool call; responses with no tool calls produce a single row
- ClickHouse writer using the native `clickhouse-go/v2` client with batched inserts (explicit column names)
- In-memory batcher with dual flush trigger: configurable size threshold and ticker interval
- Multi-tenancy via `AUDIT_PROJECT` and `AUDIT_BRANCH` tags on every row; branch auto-detected from `git rev-parse` when not set explicitly
- OpenTelemetry tracing — zero-cost when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset; `llm.proxy.request` span covers full streaming duration; W3C `traceparent` propagated inbound and outbound
- Proxy chaining via `UPSTREAM_PROXY` (overrides `HTTPS_PROXY` / `HTTP_PROXY`); supports `http`, `https`, `socks5`
- Agent detection from `User-Agent` header (`claude-code`, `codex`, `gemini-cli`); overridable via `X-Audit-Agent`
- Session ID propagation via `X-Session-ID` header; auto-generated UUID when absent
- Internal headers (`X-Session-ID`, `X-Audit-Agent`) stripped before forwarding to upstream APIs
- `EventAdder` interface decoupling handler from concrete ClickHouse writer (enables test isolation)
- ClickHouse migrations: `001_initial.sql` (full schema), `002_add_branch.sql`
- Test suite: parser unit tests, batcher tests, router tests, stream tap tests, handler integration tests

[Unreleased]: https://github.com/bitkaio/codesteward-audit-proxy/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/bitkaio/codesteward-audit-proxy/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/bitkaio/codesteward-audit-proxy/compare/v0.4.0...v1.0.0
[0.4.0]: https://github.com/bitkaio/codesteward-audit-proxy/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/bitkaio/codesteward-audit-proxy/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/bitkaio/codesteward-audit-proxy/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/bitkaio/codesteward-audit-proxy/releases/tag/v0.1.0
