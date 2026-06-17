package limiter

import (
	"context"
	"time"
)

// Algorithm identifiers used in requests and responses.
type Algorithm string

const (
	TokenBucket   Algorithm = "token_bucket"
	SlidingWindow Algorithm = "sliding_window"
)

// CheckRequest is the input for rate limit evaluation + consumption.
type CheckRequest struct {
	Key           string    `json:"key"`
	MaxTokens     uint32    `json:"max_tokens"`
	WindowSeconds uint32    `json:"window_seconds"`
	Algorithm     Algorithm `json:"algorithm"`
	Cost          uint32    `json:"cost"`
}

// CheckResponse is returned by Check.
type CheckResponse struct {
	Allowed      bool      `json:"allowed"`
	Remaining    uint32    `json:"remaining"`
	Limit        uint32    `json:"limit"`
	RetryAfterMs *int64    `json:"retry_after_ms,omitempty"`
	ResetAt      int64     `json:"reset_at"`
	Algorithm    Algorithm `json:"algorithm"`
}

// Visualization provides rich state for UI/debugging of the algorithm.
type Visualization struct {
	Algorithm string                 `json:"algorithm"`
	Key       string                 `json:"key"`
	State     map[string]any         `json:"state"`
	Diagram   string                 `json:"diagram"`
}

// Limiter is the core interface for both checking limits and visualizing algorithm state.
type Limiter interface {
	Check(ctx context.Context, req CheckRequest) (*CheckResponse, error)

	// Visualize returns internal algorithm state + a textual diagram.
	// windowSeconds and maxTokens are passed so visualization can be done
	// even if the key has never been seen before.
	Visualize(ctx context.Context, key string, algo Algorithm, maxTokens, windowSeconds uint32) (*Visualization, error)

	// Admin operations (Phase 1)
	Reset(ctx context.Context, key string) error
	Inspect(ctx context.Context, key string) (map[string]any, error)
}

// nowUnix returns current unix seconds.
func nowUnix() int64 {
	return time.Now().Unix()
}

// nowUnixMilli returns current unix milliseconds.
func nowUnixMilli() int64 {
	return time.Now().UnixMilli()
}
