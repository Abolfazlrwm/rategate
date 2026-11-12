// Command gin-middleware demonstrates using RateGate as a Gin middleware.
//
// Run a rategate-server first (in another terminal):
//
//	make build && ./bin/rategate-server --store=memory
//
// Then run this example and point a load generator at it:
//
//	go run examples/gin-middleware/main.go
//	curl localhost:9091/ping      # allowed until the limit is hit, then 429
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	rategateclient "github.com/Abolfazlrwm/rategate/pkg/client"
	rategatemw "github.com/Abolfazlrwm/rategate/pkg/middleware"
)

func main() {
	// Point at a running rategate-server.
	limiter := rategateclient.NewHTTPClient("http://localhost:8080",
		rategateclient.WithHTTPTimeout(500*time.Millisecond),
	)

	r := gin.Default()
	r.Use(rategatemw.GinMiddleware(limiter, rategatemw.Options{
		// Rate-limit by client IP. In production use a tenant/user ID.
		KeyFunc: func(r *http.Request) string {
			return "ip:" + r.RemoteAddr
		},
		Algorithm: "tokenbucket",
		Limit:     5,
		Refill:    1, // 1 token/sec, burst 5
		// FailClosed: false  -> if rategate-server is down, allow traffic.
	}))

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true})
	})

	log.Println("gin-middleware example listening on :9091")
	if err := r.Run(":9091"); err != nil {
		log.Fatal(err)
	}
}
