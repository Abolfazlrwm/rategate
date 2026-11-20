// Package server
//
// grpc_auth.go — gRPC unary server interceptor that mirrors the REST
// AuthMiddleware. Without it the gRPC transport (RateGateService.CheckRateLimit
// and the AdminService) would be unauthenticated even when server.api_key /
// server.admin_api_key are set, letting a client bypass the REST credential
// entirely. The interceptor picks the required key per called method:
//
//   - /rategate.v1.AdminService/*         -> server.admin_api_key (fallback server.api_key)
//   - /rategate.v1.RateGateService/CheckRateLimit -> server.api_key
//   - everything else (HealthCheck, standard grpc health, reflection) -> public
//
// "Everything else" is intentionally public so kubelet gRPC health probes and
// grpcurl reflection work without a credential, matching the public /healthz,
// /readyz and /metrics REST routes.
//
// AdminService with neither server.admin_api_key nor server.api_key set is
// deny-by-default (Unauthenticated), mirroring the REST admin group.
package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// adminDisabledSentinel is the credential demanded from AdminService methods
// when no admin key is configured. It cannot appear in valid metadata, so the
// comparison always fails and the method stays closed.
const adminDisabledSentinel = "\x00rategate-admin-disabled\x00"

// AuthUnaryInterceptor returns a gRPC unary server interceptor enforcing the
// same API-key auth as the REST AuthMiddleware. When the required key for a
// method is empty, the method is unauthenticated.
func (s *Server) AuthUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if required := s.requiredAPIKey(info.FullMethod); required != "" {
			if got := apiKeyFromContext(ctx); got != required {
				return nil, status.Error(codes.Unauthenticated, "invalid or missing api key")
			}
		}
		return handler(ctx, req)
	}
}

// requiredAPIKey returns the credential a method demands, or "" when the
// method is public. AdminService methods use the admin key (falling back to
// the client key for backward compatibility); CheckRateLimit uses the client
// key; health and reflection are public.
func (s *Server) requiredAPIKey(fullMethod string) string {
	switch {
	case strings.HasPrefix(fullMethod, "/rategate.v1.AdminService/"):
		if s.cfg.Server.AdminAPIKey != "" {
			return s.cfg.Server.AdminAPIKey
		}
		if s.cfg.Server.APIKey != "" {
			return s.cfg.Server.APIKey
		}
		// Deny by default: with no credential configured, the admin surface
		// must stay closed. No client can present this sentinel key, so the
		// interceptor always answers Unauthenticated.
		return adminDisabledSentinel
	case fullMethod == "/rategate.v1.RateGateService/CheckRateLimit":
		return s.cfg.Server.APIKey
	default:
		return ""
	}
}

// apiKeyFromContext pulls the first `x-api-key` value from the incoming gRPC
// metadata, mirroring the REST x-api-key header.
func apiKeyFromContext(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vs := md.Get("x-api-key"); len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}
