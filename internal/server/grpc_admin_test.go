package server

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/Abolfazlrwm/rategate/api/proto"
)

// newAdminServer builds a GRPCAdminServer backed by the seeded test core. It
// returns the admin server and a check-capable GRPCServer so tests can verify
// the two services share one policy Registry.
func newAdminServer(t *testing.T) (*GRPCAdminServer, *GRPCServer) {
	t.Helper()
	core, _ := newTestServer(t)
	return NewGRPCAdminServer(core), NewGRPCServer(core)
}

func TestGRPCAdmin_ListSeeded(t *testing.T) {
	a, _ := newAdminServer(t)
	resp, err := a.ListPolicies(context.Background(), &pb.ListPoliciesRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.GetPolicies()) != 2 {
		t.Fatalf("len=%d want 2 (strict, sw)", len(resp.GetPolicies()))
	}
	// List is sorted by name; strict < sw.
	if resp.GetPolicies()[0].GetName() != "strict" {
		t.Fatalf("first=%q want strict", resp.GetPolicies()[0].GetName())
	}
}

func TestGRPCAdmin_CreateAndGet(t *testing.T) {
	a, _ := newAdminServer(t)
	p, err := a.CreatePolicy(context.Background(), &pb.CreatePolicyRequest{
		Policy: &pb.Policy{Name: "newpol", Algorithm: "tokenbucket", Limit: 7, RefillPerSecond: 2},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.GetName() != "newpol" || p.GetLimit() != 7 {
		t.Fatalf("created policy = %+v", p)
	}

	got, err := a.GetPolicy(context.Background(), &pb.GetPolicyRequest{Name: "newpol"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.GetLimit() != 7 || got.GetRefillPerSecond() != 2 {
		t.Fatalf("get returned %+v", got)
	}
}

func TestGRPCAdmin_CreateDuplicateAlreadyExists(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.CreatePolicy(context.Background(), &pb.CreatePolicyRequest{
		Policy: &pb.Policy{Name: "strict", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate create code=%v want AlreadyExists (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_CreateInvalidArgument(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.CreatePolicy(context.Background(), &pb.CreatePolicyRequest{
		Policy: &pb.Policy{Name: "bad", Algorithm: "tokenbucket", Limit: 0, RefillPerSecond: 1},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid create code=%v want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_CreateNilPolicy(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.CreatePolicy(context.Background(), &pb.CreatePolicyRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil policy code=%v want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_UpdateExisting(t *testing.T) {
	a, _ := newAdminServer(t)
	p, err := a.UpdatePolicy(context.Background(), &pb.UpdatePolicyRequest{
		Name:   "strict",
		Policy: &pb.Policy{Name: "strict", Algorithm: "tokenbucket", Limit: 50, RefillPerSecond: 10},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p.GetLimit() != 50 {
		t.Fatalf("update returned limit=%d want 50", p.GetLimit())
	}
	got, _ := a.GetPolicy(context.Background(), &pb.GetPolicyRequest{Name: "strict"})
	if got.GetLimit() != 50 {
		t.Fatalf("post-update get limit=%d want 50", got.GetLimit())
	}
}

func TestGRPCAdmin_UpdateMissingNotFound(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.UpdatePolicy(context.Background(), &pb.UpdatePolicyRequest{
		Name:   "ghost",
		Policy: &pb.Policy{Name: "ghost", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("update missing code=%v want NotFound (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_UpdateNameMismatch(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.UpdatePolicy(context.Background(), &pb.UpdatePolicyRequest{
		Name:   "strict",
		Policy: &pb.Policy{Name: "other", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("name mismatch code=%v want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_UpdateNilPolicy(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.UpdatePolicy(context.Background(), &pb.UpdatePolicyRequest{Name: "strict"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil policy code=%v want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_DeleteThenGet(t *testing.T) {
	a, _ := newAdminServer(t)
	if _, err := a.DeletePolicy(context.Background(), &pb.DeletePolicyRequest{Name: "sw"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := a.GetPolicy(context.Background(), &pb.GetPolicyRequest{Name: "sw"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("get after delete code=%v want NotFound (err=%v)", status.Code(err), err)
	}
}

func TestGRPCAdmin_DeleteMissingNotFound(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.DeletePolicy(context.Background(), &pb.DeletePolicyRequest{Name: "ghost"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("delete missing code=%v want NotFound (err=%v)", status.Code(err), err)
	}
}

// TestGRPCAdmin_AdaptivePolicyRoundTrip verifies the proto<->config conversion
// preserves the adaptive algorithm + window (the field that distinguishes
// adaptive from tokenbucket).
func TestGRPCAdmin_AdaptivePolicyRoundTrip(t *testing.T) {
	a, _ := newAdminServer(t)
	_, err := a.CreatePolicy(context.Background(), &pb.CreatePolicyRequest{
		Policy: &pb.Policy{Name: "adap", Algorithm: "adaptive", Limit: 200, WindowMs: 10000},
	})
	if err != nil {
		t.Fatalf("create adaptive: %v", err)
	}
	got, err := a.GetPolicy(context.Background(), &pb.GetPolicyRequest{Name: "adap"})
	if err != nil {
		t.Fatalf("get adaptive: %v", err)
	}
	if got.GetAlgorithm() != "adaptive" || got.GetWindowMs() != 10000 || got.GetLimit() != 200 {
		t.Fatalf("adaptive round-trip = %+v", got)
	}
}

// TestGRPCAdmin_CreatedPolicyUsableByCheck is the key cross-service test: a
// policy created via the AdminService must be immediately resolvable by
// CheckRateLimit on the same core, proving both gRPC services share one
// thread-safe Registry (and by extension the REST admin API too).
func TestGRPCAdmin_CreatedPolicyUsableByCheck(t *testing.T) {
	a, check := newAdminServer(t)
	ctx := context.Background()

	if _, err := a.CreatePolicy(ctx, &pb.CreatePolicyRequest{
		Policy: &pb.Policy{Name: "dynamic", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// First check against the freshly-created policy must succeed.
	resp, err := check.CheckRateLimit(ctx, &pb.CheckRateLimitRequest{Key: "dyn", LimitName: "dynamic"})
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if !resp.GetAllowed() {
		t.Fatal("first check should be allowed (dynamic policy usable)")
	}

	// Second check denied (limit=1).
	resp, err = check.CheckRateLimit(ctx, &pb.CheckRateLimitRequest{Key: "dyn", LimitName: "dynamic"})
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if resp.GetAllowed() {
		t.Fatal("second check should be denied (limit=1)")
	}
}

// TestGRPCAdmin_DeletedPolicyUnknownByCheck verifies that after deletion via
// AdminService, CheckRateLimit returns InvalidArgument for the deleted name.
// TestGRPCAdmin_RegisterServerAndEmptyNameUpdate covers the two remaining
// handler paths: RegisterServer wiring AdminService onto a grpc.Server, and
// the forgiving Update path where the request name is empty and defaults to
// policy.name.
func TestGRPCAdmin_RegisterServerAndEmptyNameUpdate(t *testing.T) {
	a, _ := newAdminServer(t)
	srv := grpc.NewServer()
	a.RegisterServer(srv)
	info := srv.GetServiceInfo()
	if _, ok := info["rategate.v1.AdminService"]; !ok {
		t.Fatal("AdminService not registered on the gRPC server")
	}

	// Empty request name defaults to policy.name, so this update targets
	// "strict" and must succeed.
	p, err := a.UpdatePolicy(context.Background(), &pb.UpdatePolicyRequest{
		Policy: &pb.Policy{Name: "strict", Algorithm: "tokenbucket", Limit: 3, RefillPerSecond: 1},
	})
	if err != nil {
		t.Fatalf("update with empty name: %v", err)
	}
	if p.GetLimit() != 3 {
		t.Fatalf("limit=%d want 3", p.GetLimit())
	}
}

func TestGRPCAdmin_DeletedPolicyUnknownByCheck(t *testing.T) {
	a, check := newAdminServer(t)
	ctx := context.Background()
	if _, err := a.DeletePolicy(ctx, &pb.DeletePolicyRequest{Name: "strict"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := check.CheckRateLimit(ctx, &pb.CheckRateLimitRequest{Key: "k", LimitName: "strict"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("check after delete code=%v want InvalidArgument (err=%v)", status.Code(err), err)
	}
}
