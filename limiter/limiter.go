package limiter

import (
	"context"
	"strings"
	"time"
)

// Algorithm identifiers used in requests and responses.
type Algorithm string

const (
	TokenBucket   Algorithm = "token_bucket"
	SlidingWindow Algorithm = "sliding_window"
	LeakyBucket   Algorithm = "leaky_bucket"
)

// CheckRequest is the input for rate limit evaluation + consumption.
type CheckRequest struct {
	Key           string            `json:"key"`
	MaxTokens     uint32            `json:"max_tokens"`
	WindowSeconds uint32            `json:"window_seconds"`
	Algorithm     Algorithm         `json:"algorithm"`
	Cost          uint32            `json:"cost"`
	Labels        map[string]string `json:"labels,omitempty"`
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

	// History (Phase 2) - recent visualization snapshots for a key.
	// limit=0 means default (e.g. 20).
	History(ctx context.Context, key string, algo Algorithm, maxTokens, windowSeconds uint32, limit int) ([]*Visualization, error)
}

// nowUnix returns current unix seconds.
func nowUnix() int64 {
	return time.Now().Unix()
}

// nowUnixMilli returns current unix milliseconds.
func nowUnixMilli() int64 {
	return time.Now().UnixMilli()
}

// LimitConfig holds resolved rate limit parameters from policy.
type LimitConfig struct {
	Algorithm     Algorithm `json:"algorithm"`
	MaxTokens     uint32    `json:"max_tokens"`
	WindowSeconds uint32    `json:"window_seconds"`
	Burst         uint32    `json:"burst,omitempty"` // for leaky bucket etc.
}

// Policy defines a matching rule and its limit config.
type Policy struct {
	Name    string            `json:"name"`
	Pattern string            `json:"pattern"` // e.g. "user:*" or exact "ip:1.2.3.4"
	Labels  map[string]string `json:"labels,omitempty"`
	Config  LimitConfig       `json:"config"`
	Priority int              `json:"priority,omitempty"`
}

// PolicyEngine resolves policies for keys + labels.
type PolicyEngine interface {
	Resolve(key string, labels map[string]string) (LimitConfig, bool)
	AddPolicy(p Policy)
	RemovePolicy(name string)
	ListPolicies() []Policy
}

// DefaultPolicyEngine simple in-memory implementation with prefix matching.
type DefaultPolicyEngine struct {
	policies []Policy // sorted by priority desc
}

func NewPolicyEngine() *DefaultPolicyEngine {
	return &DefaultPolicyEngine{}
}

func (e *DefaultPolicyEngine) Resolve(key string, labels map[string]string) (LimitConfig, bool) {
	var best *Policy
	for i := range e.policies {
		p := &e.policies[i]
		if matchesPolicy(key, labels, p) {
			if best == nil || p.Priority > best.Priority {
				best = p
			}
		}
	}
	if best != nil {
		return best.Config, true
	}
	return LimitConfig{}, false
}

func (e *DefaultPolicyEngine) AddPolicy(p Policy) {
	e.policies = append(e.policies, p)
	// simple sort by priority desc
	for i := 0; i < len(e.policies); i++ {
		for j := i + 1; j < len(e.policies); j++ {
			if e.policies[j].Priority > e.policies[i].Priority {
				e.policies[i], e.policies[j] = e.policies[j], e.policies[i]
			}
		}
	}
}

func (e *DefaultPolicyEngine) RemovePolicy(name string) {
	for i := range e.policies {
		if e.policies[i].Name == name {
			e.policies = append(e.policies[:i], e.policies[i+1:]...)
			return
		}
	}
}

func (e *DefaultPolicyEngine) ListPolicies() []Policy {
	return append([]Policy{}, e.policies...)
}

func matchesPolicy(key string, labels map[string]string, p *Policy) bool {
	// simple prefix/wildcard
	if p.Pattern != "" {
		if p.Pattern == key || (strings.HasSuffix(p.Pattern, "*") && strings.HasPrefix(key, strings.TrimSuffix(p.Pattern, "*"))) {
			// labels must match if specified
			if len(p.Labels) > 0 {
				for k, v := range p.Labels {
					if labels[k] != v {
						return false
					}
				}
			}
			return true
		}
	}
	// exact label match fallback if no pattern
	if len(p.Labels) > 0 {
		match := true
		for k, v := range p.Labels {
			if labels[k] != v {
				match = false
				break
			}
		}
		return match
	}
	return false
}
