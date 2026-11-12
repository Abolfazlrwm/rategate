// Package client
//
// grpc_client.go — gRPC client wrapper for RateGateService.
package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/Abolfazlrwm/rategate/api/proto"
)

// GRPCClient wraps the generated RateGateServiceClient.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client pb.RateGateServiceClient
}

// NewGRPCClient dials target and returns a GRPCClient. The connection uses
// insecure transport — use grpc.WithTransportCredentials for TLS in
// production (a future option will expose this).
func NewGRPCClient(target string) (*GRPCClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	return &GRPCClient{
		conn:   conn,
		client: pb.NewRateGateServiceClient(conn),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *GRPCClient) Close() error { return c.conn.Close() }

// Check performs a rate-limit check over gRPC.
//
// req mirrors the REST CheckRequest; algorithm/limit/refill/window are
// inlined when limitName is empty.
func (c *GRPCClient) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	var windowMs int64
	if req.WindowMs != 0 {
		windowMs = req.WindowMs
	} else if req.Limit != 0 {
		windowMs = 0
	}
	pbReq := &pb.CheckRateLimitRequest{
		Key:             req.Key,
		LimitName:       req.LimitName,
		Algorithm:       req.Algorithm,
		Limit:           req.Limit,
		RefillPerSecond: req.RefillPerSecond,
		WindowMs:        windowMs,
	}
	resp, err := c.client.CheckRateLimit(ctx, pbReq)
	if err != nil {
		return nil, err
	}
	return &CheckResponse{
		Allowed:       resp.GetAllowed(),
		Remaining:     resp.GetRemaining(),
		Limit:         resp.GetLimit(),
		ResetAtUnixMs: resp.GetResetAtUnixMs(),
		RetryAfterMs:  resp.GetRetryAfterMs(),
		Algorithm:     resp.GetAlgorithm(),
	}, nil
}

// Health performs a gRPC HealthCheck.
func (c *GRPCClient) Health(ctx context.Context) (string, error) {
	resp, err := c.client.HealthCheck(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		return "", err
	}
	return resp.GetStatus().String(), nil
}

// ensure time is referenced for future timeout helpers.
var _ = time.Second
