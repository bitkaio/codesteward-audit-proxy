package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"llm-audit-proxy/internal/audit"
	"llm-audit-proxy/internal/parser"
	"llm-audit-proxy/internal/telemetry"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// metadataKey is the context key used to pass per-request audit metadata from
// the Director to the transport's RoundTrip.
type metadataKey struct{}

// requestMeta holds per-request audit state threaded through the context.
type requestMeta struct {
	sessionID     string
	turnID        string
	agent         string
	upstreamName  string
	project       string
	branch        string
	user          string
	team          string
	startTime     time.Time
	batcher       audit.EventAdder
	reqPath       string
	resourceGroup string
}

// Handler is the core reverse proxy HTTP handler.
type Handler struct {
	batcher         audit.EventAdder
	project         string
	branch          string
	scrubber        audit.Scrubber
	captureRequests bool
	router          *Router
	rp              *httputil.ReverseProxy
}

// NewHandler creates a Handler that forwards requests to detected upstreams,
// taps the response stream for audit, and queues AuditEvents into batcher.
// project and branch are stored on every audit row for multi-tenancy.
// scrubber is applied to request content before storage; captureRequests
// controls whether request bodies are stored at all.
func NewHandler(batcher audit.EventAdder, transport http.RoundTripper, project, branch string, scrubber audit.Scrubber, captureRequests bool, router *Router) *Handler {
	h := &Handler{
		batcher:         batcher,
		project:         project,
		branch:          branch,
		scrubber:        scrubber,
		captureRequests: captureRequests,
		router:          router,
	}

	h.rp = &httputil.ReverseProxy{
		Director: h.director,
		// Wrap the base transport so we can tap the response body inside
		// RoundTrip, where we still have the request context with metadata.
		Transport: &auditTransport{
			inner:           transport,
			batcher:         batcher,
			scrubber:        scrubber,
			captureRequests: captureRequests,
		},
		ErrorHandler: h.errorHandler,
	}

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.rp.ServeHTTP(w, r)
}

// director rewrites the outbound request URL and injects per-request audit
// metadata into the request context so the transport can access it.
func (h *Handler) director(req *http.Request) {
	upstream := h.router.DetectUpstream(req)
	RewriteRequest(req, upstream)

	// Session ID: X-Audit-Session-ID takes precedence, then X-Session-ID,
	// then auto-generated UUID.
	sessionID := req.Header.Get("X-Audit-Session-ID")
	if sessionID == "" {
		sessionID = req.Header.Get("X-Session-ID")
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	turnID := uuid.New().String()

	agent := detectAgent(req.Header)
	if override := req.Header.Get("X-Audit-Agent"); override != "" {
		agent = override
	}

	// Header-based overrides take precedence over env-var defaults.
	// This enables centrally-hosted proxies where per-request identity
	// is injected by the IDE plugin.
	project := h.project
	if v := req.Header.Get("X-Audit-Project"); v != "" {
		project = v
	}
	branch := h.branch
	if v := req.Header.Get("X-Audit-Branch"); v != "" {
		branch = v
	}

	meta := &requestMeta{
		sessionID:     sessionID,
		turnID:        turnID,
		agent:         agent,
		upstreamName:  upstream.Name,
		project:       project,
		branch:        branch,
		user:          req.Header.Get("X-Audit-User"),
		team:          req.Header.Get("X-Audit-Team"),
		startTime:     time.Now(),
		batcher:       h.batcher,
		reqPath:       req.URL.Path,
		resourceGroup: req.Header.Get("AI-Resource-Group"),
	}

	// Inject metadata into context. We replace the request in-place because
	// Director's signature is func(*http.Request) — no return value.
	*req = *req.WithContext(context.WithValue(req.Context(), metadataKey{}, meta))
}

func (h *Handler) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("proxy error",
		"method", r.Method,
		"path", r.URL.Path,
		"err", err,
	)
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

// auditTransport wraps an inner RoundTripper. It strips internal audit headers
// before forwarding the request, taps the request body for audit, then taps
// the response body for async audit extraction.
type auditTransport struct {
	inner           http.RoundTripper
	batcher         audit.EventAdder
	scrubber        audit.Scrubber
	captureRequests bool
}

// internalHeaders are headers that must not be forwarded to upstream APIs.
// Accept-Encoding is included so that http.Transport adds its own encoding
// negotiation header — this ensures it transparently decompresses the
// response body before it reaches our TeeReader. Without this, the agent's
// explicit "gzip, br" value causes Transport to leave the body compressed and
// the audit copy ends up as binary garbage.
var internalHeaders = []string{
	"X-Session-ID",
	"X-Audit-Agent",
	"X-Audit-User",
	"X-Audit-Project",
	"X-Audit-Branch",
	"X-Audit-Session-ID",
	"X-Audit-Team",
	"Accept-Encoding",
}

func (t *auditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Read the request body into a buffer so we can:
	//   1. Restore it byte-for-byte for the upstream.
	//   2. Process a copy asynchronously for audit.
	// This must happen before inner.RoundTrip consumes the body.
	var reqBodyBytes []byte
	if req.Body != nil && req.Body != http.NoBody {
		var readErr error
		reqBodyBytes, readErr = io.ReadAll(req.Body)
		req.Body.Close()
		if readErr != nil {
			// Body unreadable — restore an empty body so the upstream request
			// still goes through, and log the anomaly.
			slog.Warn("could not read request body for audit", "err", readErr)
			reqBodyBytes = nil
		}
		// Restore the body byte-for-byte so the upstream receives the original.
		req.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
	}

	// Extract incoming W3C trace context — lets an agent that propagates
	// traces become the parent of this span automatically.
	ctx := otel.GetTextMapPropagator().Extract(req.Context(), propagation.HeaderCarrier(req.Header))

	tracer := otel.Tracer(telemetry.InstrumentationName)
	ctx, span := tracer.Start(ctx, "llm.proxy.request",
		trace.WithSpanKind(trace.SpanKindClient),
	)

	// Strip internal headers before forwarding.
	for _, h := range internalHeaders {
		req.Header.Del(h)
	}

	// Inject outbound trace context so gateway proxies downstream can correlate.
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	meta, _ := req.Context().Value(metadataKey{}).(*requestMeta)
	if meta != nil {
		span.SetAttributes(
			attribute.String("gen_ai.system", meta.upstreamName),
			attribute.String("llm.agent", meta.agent),
			attribute.String("audit.session_id", meta.sessionID),
			attribute.String("audit.turn_id", meta.turnID),
			attribute.String("audit.project", meta.project),
			attribute.String("audit.branch", meta.branch),
		)
	}
	span.SetAttributes(
		attribute.String("http.request.method", req.Method),
		attribute.String("url.path", req.URL.Path),
	)

	// Emit the request-direction audit record asynchronously.
	if meta != nil {
		bodySnap := reqBodyBytes // capture for goroutine
		scrubber := t.scrubber
		captureRequests := t.captureRequests
		go func() {
			result := parser.ParseRequest(bodySnap, scrubber, captureRequests)
			e := audit.AuditEvent{
				SessionID:       meta.sessionID,
				TurnID:          meta.turnID,
				TS:              meta.startTime,
				Agent:           meta.agent,
				Project:         meta.project,
				Branch:          meta.branch,
				User:            meta.user,
				Team:            meta.team,
				Direction:       "request",
				Raw:             result.Raw,
				UserMessages:    result.UserMessages,
				RequestCaptured: result.RequestCaptured,
			}
			meta.batcher.Add(e)
		}()
	}

	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if meta == nil {
		// Defensive: no metadata, skip audit tap.
		span.End()
		return resp, nil
	}

	ts := time.Now()
	status := resp.StatusCode

	TapBody(resp, func(body []byte, isStream bool) {
		// End the span here — duration covers the full streaming response,
		// which is the meaningful latency for LLM requests.
		span.End()

		latency := time.Since(meta.startTime)
		slog.Info("request proxied",
			"session_id", meta.sessionID,
			"turn_id", meta.turnID,
			"agent", meta.agent,
			"upstream", meta.upstreamName,
			"method", req.Method,
			"path", req.URL.Path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"stream", isStream,
		)

		events := extractEvents(body, isStream, meta.upstreamName,
			meta.sessionID, meta.turnID, meta.agent, meta.project, meta.branch,
			meta.user, meta.team, ts, meta.reqPath, meta.resourceGroup)
		for _, e := range events {
			meta.batcher.Add(e)
		}
	})

	return resp, nil
}

// detectAgent infers the calling agent from the User-Agent header.
// Comparison is case-insensitive to handle capitalisation differences between
// the CLI ("claude-code") and the VSCode extension ("Claude-Code").
func detectAgent(headers http.Header) string {
	ua := strings.ToLower(headers.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "claude-code"):
		return "claude-code"
	case strings.Contains(ua, "openai-codex"):
		return "codex"
	case strings.Contains(ua, "gemini-cli"):
		return "gemini-cli"
	case strings.Contains(ua, "cline"):
		return "cline"
	default:
		return "unknown"
	}
}

// extractEvents parses the captured response body and returns one AuditEvent
// per tool call, or a single event if there are no tool calls.
// All response events have RequestCaptured=true (responses are always stored).
func extractEvents(
	body []byte,
	isStream bool,
	upstreamName, sessionID, turnID, agent, project, branch string,
	user, team string,
	ts time.Time,
	reqPath, resourceGroup string,
) []audit.AuditEvent {
	base := audit.AuditEvent{
		SessionID:       sessionID,
		TurnID:          turnID,
		TS:              ts,
		Agent:           agent,
		Project:         project,
		Branch:          branch,
		User:            user,
		Team:            team,
		Direction:       "response",
		Raw:             string(body),
		RequestCaptured: true,
	}

	switch upstreamName {
	case "anthropic":
		result, err := parser.ParseAnthropic(body, isStream)
		if err != nil {
			slog.Warn("anthropic parse error", "err", err, "turn_id", turnID)
			return []audit.AuditEvent{base}
		}
		base.Thinking = result.Thinking
		base.AssistantText = result.AssistantText
		base.Model = result.Model

		if len(result.ToolCalls) == 0 {
			return []audit.AuditEvent{base}
		}
		events := make([]audit.AuditEvent, len(result.ToolCalls))
		for i, tc := range result.ToolCalls {
			e := base
			e.ToolName = tc.Name
			e.ToolInput = tc.Input
			events[i] = e
		}
		return events

	case "openai":
		result, err := parser.ParseOpenAI(body, isStream)
		if err != nil {
			slog.Warn("openai parse error", "err", err, "turn_id", turnID)
			return []audit.AuditEvent{base}
		}
		base.AssistantText = result.AssistantText
		base.Model = result.Model

		if len(result.ToolCalls) == 0 {
			return []audit.AuditEvent{base}
		}
		events := make([]audit.AuditEvent, len(result.ToolCalls))
		for i, tc := range result.ToolCalls {
			e := base
			e.ToolName = tc.Name
			e.ToolInput = tc.Input
			events[i] = e
		}
		return events

	case "sap-ai-core":
		result, err := parser.ParseSAPAICore(body, isStream, reqPath, resourceGroup)
		if err != nil {
			slog.Warn("sap-ai-core parse error", "err", err, "turn_id", turnID)
			return []audit.AuditEvent{base}
		}
		base.AssistantText = result.AssistantText
		base.Model = result.Model
		base.ResourceGroup = result.ResourceGroup

		if len(result.ToolCalls) == 0 {
			return []audit.AuditEvent{base}
		}
		events := make([]audit.AuditEvent, len(result.ToolCalls))
		for i, tc := range result.ToolCalls {
			e := base
			e.ToolName = tc.Name
			e.ToolInput = tc.Input
			events[i] = e
		}
		return events

	default:
		// Gemini and unknown: emit a single raw event.
		return []audit.AuditEvent{base}
	}
}
