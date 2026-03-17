#!/bin/sh
# Idempotent ClickHouse migration runner.
# Uses the HTTP interface (curl) — no clickhouse-client required.
# Each *.sql file is executed at most once; applied versions are tracked in
# <DB>.schema_migrations so re-running `docker compose up` is always safe.
set -eu

HOST="${CLICKHOUSE_HOST:-clickhouse}"
PORT="${CLICKHOUSE_HTTP_PORT:-8123}"
DB="${CLICKHOUSE_DB:-audit}"
USER="${CLICKHOUSE_USER:-default}"
PASS="${CLICKHOUSE_PASSWORD:-}"

# Credentials as URL parameters — more reliable across ClickHouse versions
# than HTTP Basic Auth, which some configurations reject.
AUTH="user=${USER}&password=${PASS}"
BASE="http://${HOST}:${PORT}/"

# URL used for SQL file statements: includes the target database so that
# migrations written with unqualified table names (e.g. ALTER TABLE foo)
# still resolve correctly.
FILE_URL="${BASE}?${AUTH}&database=${DB}"

# Execute a single SQL string.
# Prints the server response on stdout; exits 1 with the error body on failure.
ch() {
    response=$(curl -s "${BASE}?${AUTH}" --data-binary "$1")
    # ClickHouse returns "Code: N" or "Exception:" in the body for errors.
    case "$response" in
        Code:*|Exception:*)
            printf 'ERROR running query:\n  %s\nServer response:\n  %s\n' \
                "$1" "$response" >&2
            exit 1
            ;;
    esac
    printf '%s' "$response"
}

# Execute every semicolon-delimited statement in a SQL file.
# Writes each statement to a numbered temp file then calls ch() on each.
ch_file() {
    file="$1"
    awk '
        BEGIN { RS = ";"; n = 0 }
        {
            gsub(/--[^\n]*/, "")
            gsub(/^[[:space:]]+|[[:space:]]+$/, "")
            if (length > 0) {
                n++
                print > ("/tmp/_ms_" n ".sql")
            }
        }
    ' "$file"

    i=1
    while [ -f "/tmp/_ms_${i}.sql" ]; do
        stmt=$(cat "/tmp/_ms_${i}.sql")
        rm -f "/tmp/_ms_${i}.sql"
        # Use FILE_URL so unqualified table names resolve to ${DB}.
        response=$(curl -s "${FILE_URL}" --data-binary "$stmt")
        case "$response" in
            Code:*|Exception:*)
                printf 'ERROR running statement from %s:\n  %s\nServer response:\n  %s\n' \
                    "$file" "$stmt" "$response" >&2
                exit 1
                ;;
        esac
        i=$((i + 1))
    done
}

# ---------------------------------------------------------------------------
# Readiness: wait for the HTTP interface to accept connections.
# The Docker healthcheck uses the native protocol; HTTP may lag by a second.
# ---------------------------------------------------------------------------
echo "==> Waiting for ClickHouse HTTP at ${HOST}:${PORT}"
retries=30
while [ "$retries" -gt 0 ]; do
    status=$(curl -so /dev/null -w '%{http_code}' "${BASE}ping" 2>/dev/null || true)
    if [ "$status" = "200" ]; then
        echo "    Ready."
        break
    fi
    retries=$((retries - 1))
    if [ "$retries" -eq 0 ]; then
        echo "ERROR: ClickHouse HTTP interface did not become ready in 30 s." >&2
        exit 1
    fi
    sleep 1
done

echo "==> Ensuring database '${DB}' exists"
ch "CREATE DATABASE IF NOT EXISTS ${DB}"

echo "==> Ensuring schema_migrations table exists"
ch "CREATE TABLE IF NOT EXISTS ${DB}.schema_migrations (
    version     String,
    applied_at  DateTime DEFAULT now()
) ENGINE = ReplacingMergeTree()
ORDER BY version"

echo "==> Scanning /migrations for pending files"
for file in /migrations/*.sql; do
    [ -f "$file" ] || continue
    version=$(basename "$file" .sql)

    applied=$(ch "SELECT count() FROM ${DB}.schema_migrations WHERE version = '${version}' FORMAT TabSeparated")

    if [ "$applied" = "0" ]; then
        printf -- '--> Applying %s ...\n' "$version"
        ch_file "$file"
        ch "INSERT INTO ${DB}.schema_migrations (version) VALUES ('${version}')"
        echo "    OK"
    else
        printf '    Already applied: %s\n' "$version"
    fi
done

echo "==> Migration complete"
