package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"llm-audit-proxy/internal/audit"
	"llm-audit-proxy/internal/config"
	"llm-audit-proxy/internal/proxy"
	"llm-audit-proxy/internal/telemetry"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "err", err)
		os.Exit(1)
	}

	setupLogger(cfg.LogLevel)

	// OpenTelemetry — no-op when OTEL_EXPORTER_OTLP_ENDPOINT is unset.
	otelShutdown, err := telemetry.Setup(context.Background())
	if err != nil {
		slog.Error("otel setup failed", "err", err)
		os.Exit(1)
	}

	slog.Info("llm-audit-proxy starting",
		"addr", cfg.ProxyAddr,
		"clickhouse_db", cfg.ClickHouseDB,
		"batch_size", cfg.BatchSize,
		"batch_interval", cfg.BatchInterval,
		"otel", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "",
		"capture_requests", cfg.CaptureRequests,
		"scrub_patterns_set", cfg.ScrubPatterns != "",
	)

	// ClickHouse writer.
	writer, err := audit.NewWriter(cfg.ClickHouseDSN, cfg.ClickHouseDB)
	if err != nil {
		slog.Error("failed to create ClickHouse writer", "err", err)
		os.Exit(1)
	}
	defer writer.Close()

	// Verify ClickHouse connectivity at startup — log but do not exit.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := writer.Ping(pingCtx); err != nil {
		slog.Error("ClickHouse ping failed — audit writes will fail until it recovers", "err", err)
	} else {
		slog.Info("ClickHouse connection verified")
	}

	// Build the scrubber from configured patterns. Fails fast on invalid regexp.
	scrubber, err := buildScrubber(cfg.ScrubPatterns)
	if err != nil {
		slog.Error("invalid scrub pattern", "err", err)
		os.Exit(1)
	}

	// Batcher and transport.
	batcher := audit.NewBatcher(writer, cfg.BatchSize, cfg.BatchInterval)
	transport := proxy.BuildTransport(cfg)

	if cfg.SAPAICoreBaseURL == "" {
		slog.Warn("SAP AI Core routing disabled: SAP_AICORE_BASE_URL is not set")
	}
	router := proxy.NewRouterWithConfig(proxy.RouterConfig{
		AnthropicUpstreamURL: cfg.AnthropicUpstreamURL,
		OpenAIUpstreamURL:    cfg.OpenAIUpstreamURL,
		GeminiUpstreamURL:    cfg.GeminiUpstreamURL,
		SAPAICoreBaseURL:     cfg.SAPAICoreBaseURL,
		SAPAICoreAuthHost:    cfg.SAPAICoreAuthHost,
	})
	handler := proxy.NewHandler(batcher, transport, cfg.AuditProject, cfg.AuditBranch, scrubber, cfg.CaptureRequests, router)

	// Top-level mux: /healthz is handled directly; everything else goes to
	// the reverse proxy handler.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version,
		})
	})
	mux.Handle("/", handler)

	srv := &http.Server{
		Addr:         cfg.ProxyAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	// Graceful shutdown on SIGINT / SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("proxy listening", "addr", cfg.ProxyAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}

	batcher.Stop()

	if err := otelShutdown(shutdownCtx); err != nil {
		slog.Error("otel shutdown error", "err", err)
	}

	slog.Info("shutdown complete")
}

// buildScrubber constructs a Scrubber from a comma-separated pattern string.
// Returns a NopScrubber when rawPatterns is empty.
// Returns an error if any pattern is an invalid regexp.
func buildScrubber(rawPatterns string) (audit.Scrubber, error) {
	if rawPatterns == "" {
		return audit.NopScrubber{}, nil
	}
	patterns := strings.Split(rawPatterns, ",")
	return audit.NewPatternScrubber(patterns)
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: l,
	})))
}
