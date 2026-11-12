# API Reference

RateGate exposes two transports with identical semantics: gRPC and REST.
Both accept a `Check` call that atomically evaluates whether one unit of
work may proceed for a given `(key, policy)` pair.

## gRPC

Service: `rategate.v1.RateGateService`

### `CheckRateLimit(CheckRateLimitRequest) -> CheckRateLimitResponse`

Evaluates the rate limit for `key` under the named policy (or, if
`limit_name` is empty, the inline fields).

**Request fields**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `key` | string | yes | Rate-limit identity, e.g. `user:42`, `ip:10.0.0.1`, `tenant:acme:api:write`. |
| `limit_name` | string | no | Name of a pre-configured policy. Overrides inline fields. |
| `algorithm` | string | no* | `tokenbucket` or `slidingwindow`. Defaults to the server's configured default. |
| `limit` | int64 | no* | Burst capacity (token bucket) or window count (sliding window). |
| `refill_per_second` | double | no* | Token refill rate. Token bucket only. |
| `window_ms` | int64 | no* | Sliding window duration in ms. Sliding window only. |

\* Inline fields are required only when `limit_name` is empty.

**Response fields**

| Field | Type | Description |
| --- | --- | --- |
| `allowed` | bool | Whether the request is admitted. |
| `remaining` | int64 | Units left after this check (>= 0). |
| `limit` | int64 | The configured limit for surfacing headers. |
| `reset_at_unix_ms` | int64 | When the limit fully resets. |
| `retry_after_ms` | int64 | Suggested wait when denied. 0 when allowed. |
| `algorithm` | string | The algorithm actually applied. |

### `HealthCheck(HealthCheckRequest) -> HealthCheckResponse`

Returns `SERVING` when the server is ready and the store is reachable,
`NOT_SERVING` otherwise.

### Error codes

| Condition | gRPC code |
| --- | --- |
| Missing `key` | `InvalidArgument` |
| Unknown `limit_name` | `InvalidArgument` |
| Invalid inline policy (limit <= 0, etc.) | `InvalidArgument` |
| Store error (Redis down) | `Internal` |

## REST

Base URL: `http://<host>:8080`

### `POST /v1/check`

JSON body mirrors the gRPC request:

```json
{
  "key": "user:42",
  "limit_name": "strict"
}
```

or inline:

```json
{
  "key": "ip:10.0.0.1",
  "algorithm": "tokenbucket",
  "limit": 100,
  "refill_per_second": 10
}
```

**Response** (200 when allowed, 429 when denied):

```json
{
  "allowed": true,
  "remaining": 4,
  "limit": 5,
  "reset_at_unix_ms": 1731400000000,
  "retry_after_ms": 0,
  "algorithm": "tokenbucket"
}
```

**Headers on every response**

| Header | Description |
| --- | --- |
| `X-RateLimit-Limit` | The configured limit. |
| `X-RateLimit-Remaining` | Units remaining. |
| `X-RateLimit-Reset` | Unix seconds when the limit resets. |

**Headers on 429**

| Header | Description |
| --- | --- |
| `Retry-After` | Whole seconds to wait before retrying (ceil). |

### `GET /healthz`

Liveness. Always 200 once the process is up.

```json
{"status":"alive","started_at":"2025-11-12T09:30:00Z"}
```

### `GET /readyz`

Readiness. 200 when the server is ready and the store is reachable; 503
otherwise.

### `GET /metrics`

Prometheus exposition. Metrics:

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `rategate_requests_total` | counter | store, algorithm, decision | Total checks. |
| `rategate_allowed_total` | counter | store, algorithm | Admitted checks. |
| `rategate_denied_total` | counter | store, algorithm | Denied checks. |
| `rategate_request_duration_seconds` | histogram | store, algorithm | End-to-end Check latency. |
| `rategate_store_latency_seconds` | histogram | store, operation | Store backend latency. |

## Authentication

When `server.api_key` is set (env `RATEGATE_SERVER_API_KEY`), both transports
require the key:

- REST: `x-api-key: <key>` header.
- gRPC: `x-api-key` metadata key.

Requests without a valid key get `401 Unauthorized` (REST) / `Unauthenticated`
(gRPC). An empty `api_key` disables auth — suitable only for trusted sidecar
deployments where the network is isolated.

## Go client SDK

```go
import "github.com/Abolfazlrwm/rategate/pkg/client"

// REST
c := client.NewHTTPClient("http://localhost:8080",
    client.WithHTTPAPIKey("secret"),
    client.WithHTTPTimeout(2 * time.Second),
)
resp, err := c.Check(ctx, client.CheckRequest{
    Key:       "user:42",
    Algorithm: "tokenbucket",
    Limit:     100,
    RefillPerSecond: 10,
})

// gRPC
gc, _ := client.NewGRPCClient("localhost:9090")
defer gc.Close()
resp, err = gc.Check(ctx, client.CheckRequest{Key: "user:42", LimitName: "strict"})
```

## Configuration

See `internal/config/config.go` for the full schema. Key environment
variables:

| Env | Default | Description |
| --- | --- | --- |
| `RATEGATE_SERVER_GRPC_PORT` | 9090 | gRPC port. |
| `RATEGATE_SERVER_REST_PORT` | 8080 | REST port. |
| `RATEGATE_SERVER_API_KEY` | "" | API key (empty disables auth). |
| `RATEGATE_STORE_BACKEND` | memory | `memory` or `redis`. |
| `RATEGATE_STORE_DEFAULT_ALGORITHM` | tokenbucket | Default algorithm. |
| `RATEGATE_REDIS_ADDR` | localhost:6379 | Redis host:port. |
| `RATEGATE_REDIS_PASSWORD` | "" | Redis AUTH password. |
| `RATEGATE_REDIS_PREFIX` | rategate | Key namespace. |
| `RATEGATE_LOG_LEVEL` | info | debug/info/warn/error. |
| `RATEGATE_LOG_FORMAT` | json | json or text. |
| `RATEGATE_OBSERVABILITY_METRICS_ENABLED` | true | Enable /metrics. |
| `RATEGATE_OBSERVABILITY_TRACING_ENABLED` | false | Enable OTel tracing. |
| `RATEGATE_OBSERVABILITY_OTLP_ENDPOINT` | "" | OTLP/gRPC collector endpoint. |
