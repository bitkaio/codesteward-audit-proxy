package config

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// ProxyAddr is the address the proxy listens on.
	ProxyAddr string

	// UpstreamProxy is an optional upstream proxy URL. When set it takes
	// priority over HTTPS_PROXY / HTTP_PROXY.
	UpstreamProxy string

	// ClickHouseDSN is required. Example: "clickhouse://localhost:9000/audit"
	ClickHouseDSN string

	// ClickHouseDB is the database name used for the audit table.
	ClickHouseDB string

	// BatchSize is the number of events that trigger an immediate flush.
	BatchSize int

	// BatchInterval is how often the batcher flushes even when below BatchSize.
	BatchInterval time.Duration

	// LogLevel controls the slog minimum level.
	LogLevel string

	// AuditProject is the repository / project name stored on every audit row.
	// Set via AUDIT_PROJECT. Enables multi-tenancy in a shared ClickHouse instance.
	AuditProject string

	// AuditBranch is the git branch stored on every audit row.
	// Set via AUDIT_BRANCH. Falls back to auto-detection from `git rev-parse`
	// when the variable is absent.
	AuditBranch string

	// CaptureRequests controls whether request bodies are stored in audit rows.
	// When false, request records are still written (for session metadata) but
	// raw and messages content are omitted. Set via AUDIT_CAPTURE_REQUESTS.
	CaptureRequests bool

	// ScrubPatterns is a comma-separated list of Go regexp patterns.
	// Matches in request message content are replaced with [REDACTED] before
	// storage. Set via AUDIT_SCRUB_PATTERNS.
	ScrubPatterns string
}

// Load reads configuration from environment variables and applies defaults.
// It returns an error if a required variable is missing.
func Load() (*Config, error) {
	captureRequests, err := getEnvBool("AUDIT_CAPTURE_REQUESTS", true)
	if err != nil {
		return nil, fmt.Errorf("invalid AUDIT_CAPTURE_REQUESTS: %w", err)
	}

	cfg := &Config{
		ProxyAddr:       getEnv("PROXY_ADDR", "127.0.0.1:8080"),
		UpstreamProxy:   getEnv("UPSTREAM_PROXY", ""),
		ClickHouseDSN:   getEnv("CLICKHOUSE_DSN", ""),
		ClickHouseDB:    getEnv("CLICKHOUSE_DB", "audit"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		AuditProject:    getEnv("AUDIT_PROJECT", ""),
		AuditBranch:     getEnv("AUDIT_BRANCH", ""),
		CaptureRequests: captureRequests,
		ScrubPatterns:   getEnv("AUDIT_SCRUB_PATTERNS", ""),
	}

	if cfg.AuditBranch == "" {
		cfg.AuditBranch = detectGitBranch()
	}

	var batchErr error

	cfg.BatchSize, batchErr = getEnvInt("BATCH_SIZE", 100)
	if batchErr != nil {
		return nil, fmt.Errorf("invalid BATCH_SIZE: %w", batchErr)
	}

	cfg.BatchInterval, batchErr = getEnvDuration("BATCH_INTERVAL", time.Second)
	if batchErr != nil {
		return nil, fmt.Errorf("invalid BATCH_INTERVAL: %w", batchErr)
	}

	if cfg.ClickHouseDSN == "" {
		return nil, fmt.Errorf("CLICKHOUSE_DSN is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func getEnvBool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, err
	}
	return b, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	return d, nil
}

// detectGitBranch attempts to read the current branch name from git.
// Returns an empty string if git is unavailable or the working directory is
// not a repository — this is a best-effort helper, never a hard requirement.
func detectGitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
