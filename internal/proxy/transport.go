package proxy

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"llm-audit-proxy/internal/config"
)

// BuildTransport returns an http.RoundTripper configured for upstream
// connections. Proxy resolution order:
//  1. cfg.UpstreamProxy (explicit UPSTREAM_PROXY env var)
//  2. http.ProxyFromEnvironment (HTTPS_PROXY / HTTP_PROXY env vars)
//  3. Direct connection (no proxy)
func BuildTransport(cfg *config.Config) http.RoundTripper {
	proxyFunc := http.ProxyFromEnvironment
	proxyMode := "env"

	if cfg.UpstreamProxy != "" {
		parsed, err := url.Parse(cfg.UpstreamProxy)
		if err != nil {
			slog.Warn("invalid UPSTREAM_PROXY, falling back to env proxy",
				"upstream_proxy", cfg.UpstreamProxy,
				"err", err,
			)
		} else {
			proxyFunc = http.ProxyURL(parsed)
			// Log the proxy URL without any embedded credentials.
			safe := *parsed
			safe.User = nil
			proxyMode = safe.String()
		}
	}

	slog.Info("upstream proxy mode", "mode", proxyMode)

	return &http.Transport{
		Proxy:                 proxyFunc,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
