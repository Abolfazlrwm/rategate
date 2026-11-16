package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc"
)

// TestGRPCServer_ReflectionRegistered verifies that GRPCServer.RegisterServer
// (the primary adapter) registers the gRPC reflection service alongside
// RateGateService and health, so the server is discoverable by grpcurl.
func TestGRPCServer_ReflectionRegistered(t *testing.T) {
	core, _ := newTestServer(t)
	srv := grpc.NewServer()
	NewGRPCServer(core).RegisterServer(srv)

	info := srv.GetServiceInfo()

	// reflection.Register installs the reflection service; its exact name
	// (v1 vs v1alpha) varies by grpc-go version, so match on the substring.
	found := false
	for name := range info {
		if strings.Contains(name, "reflection") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(info))
		for n := range info {
			names = append(names, n)
		}
		t.Fatalf("reflection service not registered; services=%v", names)
	}

	// The primary services must still be present.
	if _, ok := info["rategate.v1.RateGateService"]; !ok {
		t.Fatal("RateGateService missing from registered services")
	}
	if _, ok := info["grpc.health.v1.Health"]; !ok {
		t.Fatal("standard grpc health service missing")
	}
}
