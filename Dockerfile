# syntax=docker/dockerfile:1

# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

# git is needed by go mod download for VCS-stamped modules
RUN apk add --no-cache git

WORKDIR /build

# Cache module downloads separately from source so they survive source changes
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a fully static binary.
# -s -w strips the symbol table and DWARF debug info to shrink the binary.
# Build info is embedded via -X for version introspection.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildDate=${BUILD_DATE}" \
    -o proxy \
    ./cmd/proxy

# ── Stage 2: minimal runtime image ──────────────────────────────────────────
# distroless/static includes CA certificates (needed for outbound TLS to LLM
# APIs) and nothing else — no shell, no package manager.
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /build/proxy /proxy

# Internal listen port. The default PROXY_ADDR inside Docker should be
# 0.0.0.0:8080 (set via env); the host-side binding is controlled by
# docker-compose or the orchestrator.
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/proxy"]
