package server

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestServer_AuthUnaryInterceptor exercises the per-method key selection:
// CheckRateLimit requires the client key, AdminService methods require the
// admin key (and reject the client key), and health/reflection are public.
func TestServer_AuthUnaryInterceptor(t *testing.T) {
	core, _ := newTestServer(t)
	core.cfg.Server.APIKey = "client-secret"
	core.cfg.Server.AdminAPIKey = "admin-secret"
	intercept := core.AuthUnaryInterceptor()

	called := false
	handler := func(_ context.Context, _ any) (any, error) { called = true; return "ok", nil }

	cases := []struct {
		name       string
		fullMethod string
		mdVal      string // "" => no x-api-key metadata sent
		wantCall   bool
		wantCode   codes.Code // ignored when wantCall
	}{
		{"check no key", "/rategate.v1.RateGateService/CheckRateLimit", "", false, codes.Unauthenticated},
		{"check client key", "/rategate.v1.RateGateService/CheckRateLimit", "client-secret", true, codes.OK},
		{"check admin key rejected", "/rategate.v1.RateGateService/CheckRateLimit", "admin-secret", false, codes.Unauthenticated},
		{"admin no key", "/rategate.v1.AdminService/CreatePolicy", "", false, codes.Unauthenticated},
		{"admin admin key", "/rategate.v1.AdminService/CreatePolicy", "admin-secret", true, codes.OK},
		{"admin client key rejected", "/rategate.v1.AdminService/CreatePolicy", "client-secret", false, codes.Unauthenticated},
		{"health public", "/rategate.v1.RateGateService/HealthCheck", "", true, codes.OK},
		{"reflection public", "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", "", true, codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			ctx := context.Background()
			if tc.mdVal != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-api-key", tc.mdVal))
			}
			info := &grpc.UnaryServerInfo{FullMethod: tc.fullMethod}
			_, err := intercept(ctx, nil, info, handler)
			switch {
			case tc.wantCall:
				if !called {
					t.Fatal("handler should be called")
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			default:
				if called {
					t.Fatal("handler should NOT be called")
				}
				if status.Code(err) != tc.wantCode {
					t.Fatalf("code=%v want %v (err=%v)", status.Code(err), tc.wantCode, err)
				}
			}
		})
	}
}

// TestServer_AuthInterceptorDisabled verifies that with no keys configured the
// interceptor admits every method (auth off), matching empty-APIKey REST
// behavior.
func TestServer_AuthInterceptorDisabled(t *testing.T) {
	core, _ := newTestServer(t) // no APIKey / AdminAPIKey set
	intercept := core.AuthUnaryInterceptor()

	called := false
	handler := func(_ context.Context, _ any) (any, error) { called = true; return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/rategate.v1.RateGateService/CheckRateLimit"}
	if _, err := intercept(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("auth disabled should not error: %v", err)
	}
	if !called {
		t.Fatal("auth disabled should call the handler")
	}
}

// TestServer_AuthInterceptor_AdminFallbackToClientKey covers the
// requiredAPIKey branch where AdminAPIKey is empty and an AdminService method
// falls back to the shared client key (the gRPC analogue of the REST fallback).
func TestServer_AuthInterceptor_AdminFallbackToClientKey(t *testing.T) {
	core, _ := newTestServer(t)
	core.cfg.Server.APIKey = "shared-secret"
	// AdminAPIKey intentionally left empty.
	intercept := core.AuthUnaryInterceptor()

	called := false
	handler := func(_ context.Context, _ any) (any, error) { called = true; return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/rategate.v1.AdminService/DeletePolicy"}

	// No key -> Unauthenticated.
	called = false
	if _, err := intercept(context.Background(), nil, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no key: code=%v want Unauthenticated", status.Code(err))
	}
	if called {
		t.Fatal("handler should not be called without a key")
	}

	// Shared client key -> admitted (fallback).
	called = false
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "shared-secret"))
	if _, err := intercept(ctx, nil, info, handler); err != nil {
		t.Fatalf("shared key: unexpected error: %v", err)
	}
	if !called {
		t.Fatal("handler should be called with the shared (fallback) key")
	}
}
