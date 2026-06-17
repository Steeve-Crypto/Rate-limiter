//go:build ignore
// +build ignore

// NOTE: gRPC pb generated files have protobuf descriptor version mismatch causing init panic.
// Re-enable by removing these lines + regen pb with matching protoc once available.
// The HTTP API + dashboard remain fully functional.

package limiter

import (
	"context"

	"github.com/crypto/rate-limiter-service/limiter/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gRPCServer implements the generated pb.RateLimiterServer using our Limiter.
type gRPCServer struct {
	pb.UnimplementedRateLimiterServer
	lim Limiter
}

func NewGRPCServer(lim Limiter) *gRPCServer {
	return &gRPCServer{lim: lim}
}

func (s *gRPCServer) Check(ctx context.Context, req *pb.CheckRequest) (*pb.CheckResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "nil request")
	}
	// Convert proto to our internal request
	internalReq := CheckRequest{
		Key:           req.Key,
		MaxTokens:     req.MaxTokens,
		WindowSeconds: req.WindowSeconds,
		Algorithm:     Algorithm(req.Algorithm),
		Cost:          req.Cost,
		Labels:        req.Labels,
	}
	resp, err := s.lim.Check(ctx, internalReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check failed: %v", err)
	}
	return &pb.CheckResponse{
		Allowed:      resp.Allowed,
		Remaining:    resp.Remaining,
		Limit:        resp.Limit,
		RetryAfterMs: 0,
		ResetAt:      resp.ResetAt,
		Algorithm:    string(resp.Algorithm),
	}, nil
}

func (s *gRPCServer) Visualize(ctx context.Context, req *pb.VisualizeRequest) (*pb.VisualizeResponse, error) {
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
	// Convert state
	state := map[string]string{}
	for k, v := range viz.State {
		if s, ok := v.(string); ok {
			state[k] = s
		}
	}
	return &pb.VisualizeResponse{
		Algorithm: viz.Algorithm,
		Key:       viz.Key,
		State:     state,
		Diagram:   viz.Diagram,
	}, nil
}

// Note: Simulate, GetPolicies, AddPolicy can be implemented similarly if needed.
// For now, stubbed via Unimplemented.