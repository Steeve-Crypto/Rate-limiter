package limiter

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RateLimiterServer is the gRPC server interface (Phase 7).
type RateLimiterServer interface {
	Check(ctx context.Context, req *CheckRequest) (*CheckResponse, error)
	Visualize(ctx context.Context, req *VisualizeReq) (*Visualization, error)
}

// VisualizeReq for gRPC.
type VisualizeReq struct {
	Key           string
	Algorithm     string
	MaxTokens     uint32
	WindowSeconds uint32
}

// gRPCServer implements the service.
type gRPCServer struct {
	UnimplementedRateLimiterServer
	lim Limiter
}

func NewGRPCServer(lim Limiter) *gRPCServer {
	return &gRPCServer{lim: lim}
}

// Check implements gRPC Check.
func (s *gRPCServer) Check(ctx context.Context, req *CheckRequest) (*CheckResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "nil request")
	}
	resp, err := s.lim.Check(ctx, *req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check failed: %v", err)
	}
	return resp, nil
}

// Visualize implements gRPC Visualize (simplified).
func (s *gRPCServer) Visualize(ctx context.Context, req *VisualizeReq) (*Visualization, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "nil request")
	}
	algo := Algorithm(req.Algorithm)
	if algo == "" {
		algo = TokenBucket
	}
	viz, err := s.lim.Visualize(ctx, req.Key, algo, req.MaxTokens, req.WindowSeconds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "visualize failed: %v", err)
	}
	return viz, nil
}

// Unimplemented for forward compat.
type UnimplementedRateLimiterServer struct{}

func (UnimplementedRateLimiterServer) Check(context.Context, *CheckRequest) (*CheckResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Check not implemented")
}
func (UnimplementedRateLimiterServer) Visualize(context.Context, *VisualizeReq) (*Visualization, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Visualize not implemented")
}

// RegisterRateLimiterServer registers the gRPC service.
func RegisterRateLimiterServer(s grpc.ServiceRegistrar, srv RateLimiterServer) {
	// In real use, use generated code. Here we stub for demo.
	// For actual gRPC, define .proto and generate.
	// This registers a basic handler.
	_ = s
	_ = srv
	// Placeholder - full impl would use pb.Register
}