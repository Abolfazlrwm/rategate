# syntax=docker/dockerfile:1.7

# RateGate multi-stage Dockerfile.
#
# Stage 1: build on a full Go image with build-args for version stamping.
# Stage 2: runtime on a distroless static image (~2MB, no shell, no libc).

ARG GO_VERSION=1.26
ARG BASE_DISTRO=gcr.io/distroless/static-debian12:nonroot

FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src

# Cache deps: copy go.mod/go.sum first and download.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the rest of the source.
COPY . .

# Build args for version stamping (injected via -ldflags).
ARG VERSION=v0.2.1
ARG COMMIT=unknown
ARG BUILD_DATE

ENV CGO_ENABLED=0 GOOS=linux

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath \
      -ldflags "-s -w -X github.com/Abolfazlrwm/rategate/internal/version.Version=${VERSION} -X github.com/Abolfazlrwm/rategate/internal/version.Commit=${COMMIT} -X github.com/Abolfazlrwm/rategate/internal/version.BuildDate=${BUILD_DATE}" \
      -o /out/rategate-server ./cmd/rategate-server \
 && go build -trimpath -ldflags "-s -w" -o /out/rategate-bench ./cmd/rategate-bench

FROM ${BASE_DISTRO}

LABEL org.opencontainers.image.title="rategate" \
      org.opencontainers.image.description="Distributed rate-limiter service (token bucket + sliding window, gRPC + REST)" \
      org.opencontainers.image.source="https://github.com/Abolfazlrwm/rategate" \
      org.opencontainers.image.licenses="Apache-2.0"

# Copy the static binary.
COPY --from=builder /out/rategate-server /usr/local/bin/rategate-server
COPY --from=builder /out/rategate-bench /usr/local/bin/rategate-bench

# Non-root user (distroless nonroot image provides UID 65532).
USER nonroot:nonroot

EXPOSE 8080 9090

ENTRYPOINT ["/usr/local/bin/rategate-server"]
