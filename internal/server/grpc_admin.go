// Package server
//
// grpc_admin.go — gRPC transport for the AdminService (runtime policy CRUD).
//
// This is the gRPC mirror of admin_http.go: both transports call the same
// thread-safe admin.Registry (core.Policies()), so a policy created via gRPC
// is immediately visible to REST and to /v1/check, and vice versa. The
// conversion helpers and error-code mapping here are the gRPC analogues of
// policyToJSON / adminStatusFor in admin_http.go.
package server

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/Abolfazlrwm/rategate/api/proto"
	"github.com/Abolfazlrwm/rategate/internal/admin"
	"github.com/Abolfazlrwm/rategate/internal/config"
)

// GRPCAdminServer adapts a [Server]'s policy Registry to the generated
// AdminServiceServer interface.
type GRPCAdminServer struct {
	pb.AdminServiceServer
	core *Server
}

// NewGRPCAdminServer returns a GRPCAdminServer wrapping the core service's
// policy Registry.
func NewGRPCAdminServer(core *Server) *GRPCAdminServer {
	return &GRPCAdminServer{core: core}
}

// RegisterServer registers AdminService on the gRPC server. Call this
// alongside GRPCServer.RegisterServer (which registers RateGateService and
// health) so one gRPC server serves both the check and admin APIs.
func (a *GRPCAdminServer) RegisterServer(s *grpc.Server) {
	pb.RegisterAdminServiceServer(s, a)
}

// ListPolicies implements pb.AdminServiceServer.
func (a *GRPCAdminServer) ListPolicies(_ context.Context, _ *pb.ListPoliciesRequest) (*pb.ListPoliciesResponse, error) {
	all := a.core.Policies().List()
	out := make([]*pb.Policy, 0, len(all))
	for _, p := range all {
		out = append(out, policyToProto(p))
	}
	a.core.recordAdminOp("list", "success")
	return &pb.ListPoliciesResponse{Policies: out}, nil
}

// CreatePolicy implements pb.AdminServiceServer.
func (a *GRPCAdminServer) CreatePolicy(_ context.Context, req *pb.CreatePolicyRequest) (*pb.Policy, error) {
	if req.GetPolicy() == nil {
		a.core.recordAdminOp("create", "error")
		return nil, status.Error(codes.InvalidArgument, "policy is required")
	}
	p := policyFromProto(req.GetPolicy())
	if err := a.core.Policies().Create(p); err != nil {
		a.core.recordAdminOp("create", "error")
		return nil, status.Error(adminCodeFor(err), err.Error())
	}
	a.core.recordAdminOp("create", "success")
	a.core.refreshPolicyCount()
	return policyToProto(p), nil
}

// GetPolicy implements pb.AdminServiceServer.
func (a *GRPCAdminServer) GetPolicy(_ context.Context, req *pb.GetPolicyRequest) (*pb.Policy, error) {
	p, ok := a.core.Policies().Get(req.GetName())
	if !ok {
		a.core.recordAdminOp("get", "error")
		return nil, status.Error(codes.NotFound, admin.ErrPolicyNotFound{Name: req.GetName()}.Error())
	}
	a.core.recordAdminOp("get", "success")
	return policyToProto(p), nil
}

// UpdatePolicy implements pb.AdminServiceServer. The request name must match
// the policy name (no rename via Update), mirroring the REST PUT rule.
func (a *GRPCAdminServer) UpdatePolicy(_ context.Context, req *pb.UpdatePolicyRequest) (*pb.Policy, error) {
	if req.GetPolicy() == nil {
		a.core.recordAdminOp("update", "error")
		return nil, status.Error(codes.InvalidArgument, "policy is required")
	}
	name := req.GetName()
	p := policyFromProto(req.GetPolicy())
	if name == "" {
		name = p.Name
	}
	if p.Name != name {
		a.core.recordAdminOp("update", "error")
		return nil, status.Error(codes.InvalidArgument, "name in request must match policy.name")
	}
	if err := a.core.Policies().Update(p); err != nil {
		a.core.recordAdminOp("update", "error")
		return nil, status.Error(adminCodeFor(err), err.Error())
	}
	// Update does not change the policy count, so the gauge is not refreshed.
	a.core.recordAdminOp("update", "success")
	return policyToProto(p), nil
}

// DeletePolicy implements pb.AdminServiceServer.
func (a *GRPCAdminServer) DeletePolicy(_ context.Context, req *pb.DeletePolicyRequest) (*pb.DeletePolicyResponse, error) {
	if err := a.core.Policies().Delete(req.GetName()); err != nil {
		a.core.recordAdminOp("delete", "error")
		return nil, status.Error(adminCodeFor(err), err.Error())
	}
	a.core.recordAdminOp("delete", "success")
	a.core.refreshPolicyCount()
	return &pb.DeletePolicyResponse{}, nil
}

// policyToProto converts a config.PolicyConfig to its protobuf representation.
// Window is expressed in milliseconds to mirror CheckRateLimitRequest.window_ms.
func policyToProto(p config.PolicyConfig) *pb.Policy {
	return &pb.Policy{
		Name:            p.Name,
		Algorithm:       p.Algorithm,
		Limit:           p.Limit,
		RefillPerSecond: p.RefillPerSecond,
		WindowMs:        p.Window.Milliseconds(),
	}
}

// policyFromProto converts a protobuf Policy into a config.PolicyConfig.
func policyFromProto(p *pb.Policy) config.PolicyConfig {
	return config.PolicyConfig{
		Name:            p.GetName(),
		Algorithm:       p.GetAlgorithm(),
		Limit:           p.GetLimit(),
		RefillPerSecond: p.GetRefillPerSecond(),
		Window:          time.Duration(p.GetWindowMs()) * time.Millisecond,
	}
}

// adminCodeFor maps an admin.Registry / config.PolicyConfig validation error
// to a gRPC status code: ErrPolicyExists -> AlreadyExists,
// ErrPolicyNotFound -> NotFound, validation errors -> InvalidArgument. This is
// the gRPC analogue of adminStatusFor in admin_http.go.
func adminCodeFor(err error) codes.Code {
	var existsErr admin.ErrPolicyExists
	var notFoundErr admin.ErrPolicyNotFound
	switch {
	case errors.As(err, &existsErr):
		return codes.AlreadyExists
	case errors.As(err, &notFoundErr):
		return codes.NotFound
	default:
		// PolicyConfig.Validate() returns plain fmt.Errorf values for bad
		// input (missing name, non-positive limit, unknown algorithm, missing
		// algorithm-specific field). These are client errors.
		return codes.InvalidArgument
	}
}
