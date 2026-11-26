// Command client-sdk-usage shows both the HTTP and gRPC clients against a
// running rategate-server.
//
// Start the server:
//
//	make build && ./bin/rategate-server --store=memory
//
// Then:
//
//	go run examples/client-sdk-usage/main.go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Abolfazlrwm/rategate/pkg/client"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// HTTP client.
	hc := client.NewHTTPClient("http://localhost:8080",
		client.WithHTTPTimeout(time.Second),
	)

	// Health check.
	if err := hc.Health(ctx); err != nil {
		fmt.Printf("health (http): %v\n", err)
	} else {
		fmt.Println("health (http): OK")
	}

	// Burst 5 then watch denials.
	for i := 1; i <= 7; i++ {
		resp, err := hc.Check(ctx, client.CheckRequest{
			Key:             "user:demo",
			Algorithm:       "tokenbucket",
			Limit:           5,
			RefillPerSecond: 1,
		})
		if err != nil {
			if httpErr, ok := err.(*client.HTTPError); ok && httpErr.IsDenied() {
				fmt.Printf("check %d: DENIED (retry_after_ms=%d)\n", i, httpErr.RetryAfterMs)
				continue
			}
			fmt.Printf("check %d: error %v\n", i, err)
			continue
		}
		fmt.Printf("check %d: allowed=%t remaining=%d algorithm=%s\n",
			i, resp.Allowed, resp.Remaining, resp.Algorithm)
	}

	// gRPC client (optional; skip if the server isn't running gRPC).
	gc, err := client.NewGRPCClient("localhost:9090")
	if err != nil {
		fmt.Printf("grpc dial (skipping): %v\n", err)
		return
	}
	defer gc.Close()
	status, err := gc.Health(ctx)
	fmt.Printf("health (grpc): status=%s err=%v\n", status, err)
}
