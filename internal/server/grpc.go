// Package server
//
// grpc.go — gRPC transport for RateGateService.
package server

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	pb "github.com/Abolfazlrwm/rategate/api/proto"
	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// GRPCServer adapts a [Server] to the generated RateGateServiceServer
// interface. It also hosts a standard gRPC health server.
type GRPCServer struct {
	pb.RateGateServiceServer
	healthSrv *health.Server
	core      *Server
}

// NewGRPCServer returns a GRPCServer wrapping the core service.
func NewGRPCServer(core *Server) *GRPCServer {
	return &GRPCServer{
		healthSrv: health.NewServer(),
		core:      core,
	}
}

// HealthServer returns the embedded standard gRPC health server so the
// caller can register it alongside the RateGateService.
func (g *GRPCServer) HealthServer() *health.Server { return g.healthSrv }

// RegisterServer registers RateGateService, the health service, and the gRPC
// reflection service on the gRPC server. Reflection is registered here (on the
// primary adapter) so every RateGate gRPC server is discoverable by grpcurl and
// friends without extra wiring in main.
func (g *GRPCServer) RegisterServer(s *grpc.Server) {
	pb.RegisterRateGateServiceServer(s, g)
	healthpb.RegisterHealthServer(s, g.healthSrv)
	reflection.Register(s)
}

// CheckRateLimit implements pb.RateGateServiceServer.
func (g *GRPCServer) CheckRateLimit(ctx context.Context, req *pb.CheckRateLimitRequest) (*pb.CheckRateLimitResponse, error) {
	if req.GetKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}

	policy := algorithms.Policy{
		Limit:           req.GetLimit(),
		RefillPerSecond: req.GetRefillPerSecond(),
		Window:          time.Duration(req.GetWindowMs()) * time.Millisecond,
	}
	cr := CheckRequest{
		Key:        req.GetKey(),
		PolicyName: req.GetLimitName(),
		Algorithm:  req.GetAlgorithm(),
		Policy:     policy,
	}

	result, err := g.core.Check(ctx, cr, time.Now())
	if err != nil {
		// Distinguish "unknown policy" (InvalidArgument) from store errors.
		if isUnknownPolicyErr(err) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &pb.CheckRateLimitResponse{
		Allowed:       result.Allowed,
		Remaining:     result.Remaining,
		Limit:         result.Limit,
		ResetAtUnixMs: result.ResetAt.UnixMilli(),
		RetryAfterMs:  int64(result.RetryAfter / time.Millisecond),
		Algorithm:     result.Algorithm,
	}, nil
}

// HealthCheck implements pb.RateGateServiceServer.
func (g *GRPCServer) HealthCheck(ctx context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	h := g.core.Health(ctx)
	var st pb.HealthCheckResponse_ServingStatus
	switch h {
	case HealthServing:
		st = pb.HealthCheckResponse_SERVING
		g.healthSrv.SetServingStatus("rategate", healthpb.HealthCheckResponse_SERVING)
	case HealthNotServing:
		st = pb.HealthCheckResponse_NOT_SERVING
		g.healthSrv.SetServingStatus("rategate", healthpb.HealthCheckResponse_NOT_SERVING)
	default:
		st = pb.HealthCheckResponse_SERVING_STATUS_UNSPECIFIED
	}
	return &pb.HealthCheckResponse{Status: st}, nil
}

// isUnknownPolicyErr returns true if err is the "unknown policy" error from
// the core Check. We string-match via HasPrefix to avoid exporting an error
// type from the core package; the message prefix is stable.
func isUnknownPolicyErr(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unknown policy")
}
