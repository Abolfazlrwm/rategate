# RateGate examples

Three runnable examples ship in Phase 1. Each assumes a rategate-server is
running (or, for the standalone example, embeds the core directly).

## gin-middleware

Use RateGate as a Gin middleware in front of an existing HTTP service.

```bash
make build && ./bin/rategate-server --store=memory &
go run examples/gin-middleware/main.go
# then: curl localhost:9091/ping   (5 quick requests OK, 6th gets 429)
```

## standalone-server

Embed the RateGate core directly in your Go process — no sidecar binary.
Useful for single-instance services or for trying the API without standing
up the full server.

```bash
go run examples/standalone-server/main.go
curl -X POST localhost:8090/v1/check \
     -H 'content-type: application/json' \
     -d '{"key":"user:1","algorithm":"tokenbucket","limit":5,"refill_per_second":1}'
```

## client-sdk-usage

Exercise the HTTP and gRPC Go client SDKs against a running server.

```bash
make build && ./bin/rategate-server --store=memory &
go run examples/client-sdk-usage/main.go
```
