package limiter

import (
	"context"
	"fmt"
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

	// Phase 4: Persistent + Replayable State
	Snapshot(ctx context.Context, dest string) error // dest can be file path or "redis"
	Restore(ctx context.Context, src string) error
	LogDecision(ctx context.Context, ev DecisionEvent) error
	Replay(ctx context.Context, fromTs, toTs int64) ([]DecisionEvent, error)

	// Phase 5: Replication support
	EmitReplicationEvent(ctx context.Context, ev ReplicationEvent) error
	ApplyReplicationEvent(ev ReplicationEvent) bool
	GetReplicatedState(key string) (interface{}, bool)
}

// nowUnix returns current unix seconds.
func nowUnix() int64 {
	return time.Now().Unix()
}

// nowUnixMilli returns current unix milliseconds.
func nowUnixMilli() int64 {
	return time.Now().UnixMilli()
}

// DecisionEvent for event log and replay (Phase 4)
type DecisionEvent struct {
	Timestamp int64     `json:"ts"`
	Key       string    `json:"key"`
	Algorithm Algorithm `json:"algo"`
	Allowed   bool      `json:"allowed"`
	Remaining uint32    `json:"remaining"`
	Limit     uint32    `json:"limit"`
	Cost      uint32    `json:"cost"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// StateStore abstracts persistence for snapshots and event logs (Phase 4).
type StateStore interface {
	SaveSnapshot(ctx context.Context, data []byte) error
	LoadSnapshot(ctx context.Context) ([]byte, error)
	AppendEvent(ctx context.Context, ev DecisionEvent) error
	QueryEvents(ctx context.Context, from, to int64) ([]DecisionEvent, error)
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

// NamespaceKey is a helper for richer multi-tenant / namespace security.
// It prefixes or validates keys using labels (e.g. tenant, user, env).
// Usage in middleware or before Check:
//   safeKey := limiter.NamespaceKey("tenant:acme", req.Labels["user"], req.Key)
// This prevents cross-tenant key collisions when using shared limiter instances.
func NamespaceKey(namespace string, labels map[string]string, key string) string {
	if namespace == "" {
		namespace = labels["tenant"]
	}
	if namespace == "" {
		namespace = labels["namespace"]
	}
	if namespace == "" {
		return key
	}
	// Avoid double prefix
	if strings.HasPrefix(key, namespace+":") || strings.HasPrefix(key, namespace+"/") {
		return key
	}
	return namespace + ":" + key
}

// ValidateKeyNamespace enforces that a key belongs to an allowed namespace.
// Returns error if violation. Useful for admin or policy enforcement.
func ValidateKeyNamespace(allowedNamespace string, key string) error {
	if allowedNamespace == "" {
		return nil
	}
	if !strings.HasPrefix(key, allowedNamespace+":") && !strings.HasPrefix(key, allowedNamespace+"/") {
		return fmt.Errorf("key %q violates namespace %q", key, allowedNamespace)
	}
	return nil
}
