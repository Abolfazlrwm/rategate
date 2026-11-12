// Command standalone-server embeds the RateGate core directly in a Go
// process — no separate rategate-server binary. Use this when you want
// in-process rate limiting without a sidecar.
//
//	go run examples/standalone-server/main.go
//	curl -X POST localhost:8090/v1/check \
//	     -H 'content-type: application/json' \
//	     -d '{"key":"user:1","algorithm":"tokenbucket","limit":5,"refill_per_second":1}'
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Abolfazlrwm/rategate/internal/config"
	"github.com/Abolfazlrwm/rategate/internal/observability"
	"github.com/Abolfazlrwm/rategate/internal/server"
	"github.com/Abolfazlrwm/rategate/internal/store"
)

func main() {
	cfg := &config.Config{}
	cfg.Store.Backend = "memory"
	cfg.Store.DefaultAlgorithm = "tokenbucket"
	cfg.Policies = []config.PolicyConfig{
		{Name: "default", Algorithm: "tokenbucket", Limit: 100, RefillPerSecond: 10},
	}

	metrics, reg := observability.NewMetrics()
	logger := observability.NewLogger("info", "text")
	s := store.NewMemoryStore(store.MemoryOptions{TBShards: 64})
	defer s.Close()

	core := server.New(cfg, s, metrics, logger)
	core.MarkReady()

	rest := server.NewHTTPServer(core, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	srv := &http.Server{
		Addr:         ":8090",
		Handler:      rest.Engine(),
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}

	fmt.Println("standalone rategate listening on :8090 (POST /v1/check, GET /metrics)")
	log.Fatal(srv.ListenAndServe())
}
