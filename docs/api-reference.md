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
| `algorithm` | string | no* | `tokenbucket`, `slidingwindow`, or `adaptive`. Defaults to the server's configured default. |
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

## Admin gRPC API

Service `rategate.v1.AdminService` mirrors the REST admin API and shares the
same thread-safe Registry, so a policy created via gRPC is immediately visible
to REST, to `CheckRateLimit`, and vice versa.

| RPC | Request | Response | Error codes |
| --- | --- | --- | --- |
| `ListPolicies` | `ListPoliciesRequest` | `ListPoliciesResponse{repeated Policy}` | — |
| `CreatePolicy` | `CreatePolicyRequest{policy}` | `Policy` | `AlreadyExists`, `InvalidArgument` |
| `GetPolicy` | `GetPolicyRequest{name}` | `Policy` | `NotFound` |
| `UpdatePolicy` | `UpdatePolicyRequest{name, policy}` | `Policy` | `NotFound`, `InvalidArgument` (incl. name mismatch) |
| `DeletePolicy` | `DeletePolicyRequest{name}` | `DeletePolicyResponse` | `NotFound` |

`Policy` fields: `name`, `algorithm` (`tokenbucket` | `slidingwindow` |
`adaptive`), `limit`, `refill_per_second`, `window_ms`. On `UpdatePolicy`,
`name` must equal `policy.name` (no rename via Update), mirroring the REST PUT
rule.

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

## Admin REST API

Policies can be managed at runtime (no restart) under `/v1/admin/policies`.
These endpoints are gated by `server.admin_api_key` when set (falling back to
`server.api_key`); see [Authentication](#authentication). Mutations are applied
to a thread-safe registry and
are visible to subsequent `/v1/check` calls immediately.

### `GET /v1/admin/policies`

List all policies, sorted by name.

```json
{"policies":[{"name":"strict","algorithm":"tokenbucket","limit":5,"refill_per_second":1,"window_ms":0}]}
```

### `POST /v1/admin/policies`

Create a policy. `201 Created` on success; `409 Conflict` if the name exists;
`400 Bad Request` if the policy fails validation (missing name, non-positive
limit, unknown algorithm, or missing the algorithm-specific field).

```json
{"name":"burst","algorithm":"adaptive","limit":200,"window_ms":10000}
```

### `GET /v1/admin/policies/:name`

Fetch one policy. `404` if missing.

### `PUT /v1/admin/policies/:name`

Update an existing policy. The `name` in the body must match the path (no
rename via PUT). `200` on success; `404` if missing; `400` on validation
failure or name mismatch.

### `DELETE /v1/admin/policies/:name`

Delete a policy. `204 No Content` on success; `404` if missing. A deleted
policy is immediately unknown to `/v1/check` (references return `400`).

**Policy JSON shape**

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `name` | string | yes | Unique policy name. |
| `algorithm` | string | yes | `tokenbucket`, `slidingwindow`, or `adaptive`. |
| `limit` | int64 | yes | `> 0`. Floor (adaptive) / burst (tokenbucket) / window count (slidingwindow). |
| `refill_per_second` | double | tokenbucket | `> 0` for tokenbucket. |
| `window_ms` | int64 | slidingwindow / adaptive | `> 0`. Window duration in ms. |

Runtime changes are not persisted back to the config file; a process restart
re-seeds from file/env.

## Authentication

RateGate has two independent credentials, configured via `server.api_key`
(env `RATEGATE_SERVER_API_KEY`) and `server.admin_api_key`
(env `RATEGATE_SERVER_ADMIN_API_KEY`).

- **`server.api_key`** gates the data plane: `POST /v1/check` (REST) and
  `rategate.v1.RateGateService/CheckRateLimit` (gRPC).
- **`server.admin_api_key`** gates the admin plane: `/v1/admin/*` (REST) and
  `rategate.v1.AdminService` (gRPC). When empty, admin routes fall back to
  `server.api_key` (Phase 2 behavior). Setting a distinct admin key is strongly
  recommended so check clients cannot create or delete policies.

Both transports read the credential from the same place:

- REST: `x-api-key: <key>` header.
- gRPC: `x-api-key` metadata key (enforced by a unary server interceptor with
  per-method key selection).

`/healthz`, `/readyz`, and `/metrics` are intentionally **public** so kubelet
probes and a metrics scraper can reach them without a credential; restrict
`/metrics` at the network layer if your environment requires it. gRPC health
and reflection are likewise public.

Requests without a valid key get `401 Unauthorized` (REST) /
`Unauthenticated` (gRPC). Leaving both keys empty disables auth — suitable
only for trusted sidecar deployments where the network is isolated.

### Store failure mode

`store.fail_open` (default `false`) controls what happens when the backing
store errors mid-Check. Fail-closed (default) surfaces the error and denies the
request (`500` REST / `Internal` gRPC) — the limiter is treated as broken, not
as silently denying. `fail_open=true` admits the request instead, so a Redis
outage does not take down the protected service, at the cost of an unbounded
burst while the store is unavailable. Choose deliberately.

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
| `RATEGATE_STORE_BACKEND` | memory | `memory`, `redis`, or `redis-cluster`. |
| `RATEGATE_STORE_DEFAULT_ALGORITHM` | tokenbucket | `tokenbucket`, `slidingwindow`, or `adaptive`. |
| `RATEGATE_REDIS_ADDR` | localhost:6379 | Redis host:port (single-node `redis` backend). |
| `RATEGATE_REDIS_PASSWORD` | "" | Redis AUTH password. |
| `RATEGATE_REDIS_PREFIX` | rategate | Key namespace. |
| `RATEGATE_CLUSTER_*` | — | Redis Cluster settings (`addrs`, `route_randomly`, `max_redirects`, `prefix`); see `config.example.yaml`. `addrs` is configured via file or env list. |
| `RATEGATE_STORE_MEMORY_ADAPTIVE_MAX_FACTOR` | 4.0 | Cap for the adaptive limiter: effective limit in `[floor, floor*max_factor]`. |
| `RATEGATE_LOG_LEVEL` | info | debug/info/warn/error. |
| `RATEGATE_LOG_FORMAT` | json | json or text. |
| `RATEGATE_OBSERVABILITY_METRICS_ENABLED` | true | Enable /metrics. |
| `RATEGATE_OBSERVABILITY_TRACING_ENABLED` | false | Enable OTel tracing. |
| `RATEGATE_OBSERVABILITY_OTLP_ENDPOINT` | "" | OTLP/gRPC collector endpoint. |
