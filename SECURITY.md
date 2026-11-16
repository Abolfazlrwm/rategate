# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in RateGate, **please do not open a
public GitHub issue**. Instead, report it privately via one of:

- **GitHub Security Advisories** (preferred): go to the
  [Security tab](https://github.com/Abolfazlrwm/rategate/security/advisories/new)
  of the repository and click "Report a vulnerability". This keeps the report
  private to the maintainers and lets us publish a coordinated advisory with
  credit when the fix ships.
- Email the maintainer directly if you cannot use Security Advisories.

Please include:

- A description of the issue and its impact (what an attacker could do).
- The affected version(s) and a minimal reproduction (config, request, log).
- Any suggested fix or mitigation, if you have one.

## Response Expectations

- **Acknowledgement**: within 3 business days of the report.
- **Status update**: at least every 7 days until resolution.
- **Coordinated disclosure**: we will not publicly disclose the issue until a
  fix is available, or 90 days have elapsed since the report, whichever comes
  first. We will credit you in the advisory unless you ask to remain anonymous.
- A report that turns out not to be a vulnerability will be closed with an
  explanation.

## Supported Versions

Only the **latest minor release** on the `main` branch receives security fixes.
When a new minor is cut, the previous minor stops receiving fixes. Pin to a
specific patch version in production and watch releases for security advisories.

| Version | Supported |
| ------- | --------- |
| latest minor (`0.x`) | ✅ |
| older minors | ❌ |

## Scope

In scope:

- The `rategate-server` binary and its gRPC/REST handlers.
- The `pkg/client` and `pkg/middleware` libraries.
- The store backends (`internal/store`).

Out of scope (report to the upstream project instead):

- Vulnerabilities in dependencies (Redis, the Go toolchain, gRPC, Prometheus
  client) — report to the upstream CVE process; we will bump the dependency on
  our side once a fix is available.
- Issues that require already-privileged access to the host or Redis instance
  RateGate runs against (e.g. an attacker who already has Redis ADMIN).

## Hardening Notes for Operators

- **Set `server.admin_api_key`** to a distinct, strong value so that check
  clients cannot create or delete policies.
- **Leave `store.fail_open` at `false`** (the default) unless you have
  explicitly accepted the availability-over-safety tradeoff. Fail-open admits
  traffic when the store is unreachable, which means an unbounded burst during
  a Redis outage.
- **Run Redis with AUTH** (`redis.password` / `cluster.password`) and on an
  isolated network; RateGate does not encrypt the Redis connection — use a
  TLS sidecar or Redis 6+ TLS if the network is untrusted.
- **Do not expose `/metrics` publicly**; place it behind your scraper's own
  auth or restrict it at the network layer. `/metrics` is intentionally
  unauthenticated at the application layer so a scraper can reach it.
