package limiter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// InMemoryLimiter implements Limiter using local memory (very low latency).
// Suitable for single-instance or as a fast local cache layer.
type InMemoryLimiter struct {
	mu sync.RWMutex

	// token bucket state
	tb map[string]*tbState

	// sliding window: store recent event times (unix milli) per key.
	// We keep them sorted for easy window trimming.
	sw map[string][]int64

	// leaky bucket state (Phase 3)
	lb map[string]*lbState

	// history: lightweight recent visualizations per key (Phase 2)
	// bounded ring to avoid unbounded growth
	history     map[string][]*Visualization
	historySize int

	// decisions log for replay (Phase 4)
	decisions []DecisionEvent
}

type tbState struct {
	tokens     float64
	lastRefill int64 // unix milli
}

func NewInMemoryLimiter() *InMemoryLimiter {
	return &InMemoryLimiter{
		tb:          make(map[string]*tbState),
		sw:          make(map[string][]int64),
		lb:          make(map[string]*lbState),
		history:     make(map[string][]*Visualization),
		historySize: 50, // keep last N visualizations
		decisions:   []DecisionEvent{},
	}
}

func (m *InMemoryLimiter) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	start := time.Now()
	defer func() {
		// Note: allowed is set after, so we record after switch or use a wrapper.
		// For simplicity, we'll update after in each path or record generic here.
		CheckDuration.WithLabelValues(string(req.Algorithm), "inmemory").Observe(time.Since(start).Seconds())
	}()

	if req.Cost == 0 {
		req.Cost = 1
	}
	if req.MaxTokens == 0 || req.WindowSeconds == 0 {
		return nil, fmt.Errorf("max_tokens and window_seconds must be > 0")
	}

	var resp *CheckResponse
	switch req.Algorithm {
	case TokenBucket:
		resp = m.checkTokenBucket(req)
	case SlidingWindow:
		resp = m.checkSlidingWindow(req)
	case LeakyBucket:
		resp = m.checkLeakyBucket(req)
	default:
		return nil, fmt.Errorf("unknown algorithm: %s", req.Algorithm)
	}

	allowedStr := "false"
	if resp.Allowed {
		allowedStr = "true"
	}
	ChecksTotal.WithLabelValues(string(req.Algorithm), allowedStr).Inc()

	return resp, nil
}

func (m *InMemoryLimiter) checkTokenBucket(req CheckRequest) *CheckResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := nowUnixMilli()
	windowMs := int64(req.WindowSeconds) * 1000
	ratePerMs := float64(req.MaxTokens) / float64(windowMs)

	state, ok := m.tb[req.Key]
	if !ok {
		state = &tbState{tokens: float64(req.MaxTokens), lastRefill: now}
		m.tb[req.Key] = state
	}

	// Refill
	elapsed := float64(now - state.lastRefill)
	add := elapsed * ratePerMs
	state.tokens = minFloat(state.tokens+add, float64(req.MaxTokens))
	state.lastRefill = now

	allowed := state.tokens >= float64(req.Cost)
	if allowed {
		state.tokens -= float64(req.Cost)
	}

	remaining := uint32(state.tokens)
	if remaining > req.MaxTokens {
		remaining = req.MaxTokens
	}

	resp := &CheckResponse{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     req.MaxTokens,
		ResetAt:   nowUnix() + int64(req.WindowSeconds),
		Algorithm: TokenBucket,
	}

	if !allowed {
		needed := float64(req.Cost) - state.tokens
		retryMs := int64(0)
		if ratePerMs > 0 {
			retryMs = int64(needed / ratePerMs)
		} else {
			retryMs = windowMs
		}
		resp.RetryAfterMs = &retryMs
	}
	return resp
}

func (m *InMemoryLimiter) checkSlidingWindow(req CheckRequest) *CheckResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := nowUnixMilli()
	windowMs := int64(req.WindowSeconds) * 1000
	cutoff := now - windowMs

	events := m.sw[req.Key]
	// trim old
	idx := sort.Search(len(events), func(i int) bool { return events[i] > cutoff })
	events = events[idx:]

	// count current
	count := len(events)
	allowed := uint32(count)+req.Cost <= req.MaxTokens

	if allowed {
		for i := uint32(0); i < req.Cost; i++ {
			events = append(events, now+int64(i)) // slight spread for same-ms cost>1
		}
		sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
		m.sw[req.Key] = events
	} else {
		m.sw[req.Key] = events // still store trimmed
	}

	remaining := req.MaxTokens
	if uint32(count) < req.MaxTokens {
		remaining = req.MaxTokens - uint32(count)
	}
	if !allowed {
		remaining = 0
	}

	resp := &CheckResponse{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     req.MaxTokens,
		ResetAt:   nowUnix() + int64(req.WindowSeconds),
		Algorithm: SlidingWindow,
	}

	if !allowed && len(events) > 0 {
		oldest := events[0]
		retry := oldest + windowMs - now
		if retry < 1 {
			retry = 1
		}
		resp.RetryAfterMs = &retry
	}
	return resp
}

// === VISUALIZE IMPLEMENTATION ===

func (m *InMemoryLimiter) Visualize(ctx context.Context, key string, algo Algorithm, maxTokens, windowSeconds uint32) (*Visualization, error) {
	start := time.Now()
	defer func() {
		VisualizeDuration.WithLabelValues(string(algo), "inmemory").Observe(time.Since(start).Seconds())
		VisualizeTotal.WithLabelValues(string(algo)).Inc()
	}()

	m.mu.RLock()
	defer m.mu.RUnlock()

	switch algo {
	case TokenBucket:
		viz := m.visualizeTokenBucket(key, maxTokens, windowSeconds)
		m.recordHistory(key, viz)
		return viz, nil
	case SlidingWindow:
		viz := m.visualizeSlidingWindow(key, maxTokens, windowSeconds)
		m.recordHistory(key, viz)
		return viz, nil
	default:
		return nil, fmt.Errorf("unknown algorithm: %s", algo)
	}
}

func (m *InMemoryLimiter) visualizeTokenBucket(key string, maxTokens, windowSeconds uint32) *Visualization {
	state, ok := m.tb[key]
	now := nowUnixMilli()

	var tokens float64 = float64(maxTokens)
	lastMs := now
	if ok {
		tokens = state.tokens
		lastMs = state.lastRefill
	}

	windowMs := int64(windowSeconds) * 1000
	ratePerSec := float64(maxTokens) / float64(windowSeconds)

	elapsed := now - lastMs
	refilled := float64(elapsed) * (float64(maxTokens) / float64(windowMs))
	current := minFloat(tokens+refilled, float64(maxTokens))

	percent := 0.0
	if maxTokens > 0 {
		percent = (current / float64(maxTokens)) * 100
	}

	bar := renderBar(current, float64(maxTokens), 30)

	stateMap := map[string]any{
		"current_tokens": fmt.Sprintf("%.2f", current),
		"max_tokens":     maxTokens,
		"rate_per_sec":   fmt.Sprintf("%.2f", ratePerSec),
		"last_refill_ms": lastMs,
		"elapsed_ms":     elapsed,
	}

	diagram := fmt.Sprintf(`Token Bucket [%s]
Capacity : %d
Current  : %.1f
Rate     : %.2f tokens/sec
Last fill: %d ms ago

%s  %.1f%%`, key, maxTokens, current, ratePerSec, elapsed, bar, percent)

	return &Visualization{
		Algorithm: string(TokenBucket),
		Key:       key,
		State:     stateMap,
		Diagram:   diagram,
	}
}

func (m *InMemoryLimiter) visualizeSlidingWindow(key string, maxTokens, windowSeconds uint32) *Visualization {
	now := nowUnixMilli()
	windowMs := int64(windowSeconds) * 1000
	cutoff := now - windowMs

	events := m.sw[key]
	idx := sort.Search(len(events), func(i int) bool { return events[i] > cutoff })
	events = events[idx:]

	count := len(events)
	percent := 0.0
	if maxTokens > 0 {
		percent = (float64(count) / float64(maxTokens)) * 100
	}

	bar := renderBar(float64(count), float64(maxTokens), 30)

	// Build a simple recent timeline (last 8 events)
	var recent []string
	for i := len(events) - 1; i >= 0 && len(recent) < 8; i-- {
		age := (now - events[i]) / 1000
		recent = append(recent, fmt.Sprintf("-%ds", age))
	}
	sort.Strings(recent) // rough

	stateMap := map[string]any{
		"requests_in_window": count,
		"max_tokens":         maxTokens,
		"window_seconds":     windowSeconds,
		"events_sample":      events,
	}

	diagram := fmt.Sprintf(`Sliding Window [%s]
Window   : %ds
In window: %d / %d
Usage    : %.1f%%

%s

Recent events (age): %s`, key, windowSeconds, count, maxTokens, percent, bar, strings.Join(recent, " "))

	return &Visualization{
		Algorithm: string(SlidingWindow),
		Key:       key,
		State:     stateMap,
		Diagram:   diagram,
	}
}

// Reset clears all state for a key (both token bucket and sliding window).
func (m *InMemoryLimiter) Reset(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tb, key)
	delete(m.sw, key)
	delete(m.history, key)
	return nil
}

func (m *InMemoryLimiter) recordHistory(key string, viz *Visualization) {
	if viz == nil {
		return
	}
	h := m.history[key]
	h = append(h, viz)
	if len(h) > m.historySize {
		h = h[len(h)-m.historySize:]
	}
	m.history[key] = h
}

// Inspect returns raw internal state for debugging.
func (m *InMemoryLimiter) Inspect(ctx context.Context, key string) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := map[string]any{
		"key": key,
	}

	if tb, ok := m.tb[key]; ok {
		result["token_bucket"] = map[string]any{
			"tokens":       tb.tokens,
			"last_refill":  tb.lastRefill,
		}
	}

	if events, ok := m.sw[key]; ok {
		result["sliding_window"] = map[string]any{
			"events_count": len(events),
			"events":       events,
		}
	}

	return result, nil
}

// History returns recent visualization snapshots.
func (m *InMemoryLimiter) History(ctx context.Context, key string, algo Algorithm, maxTokens, windowSeconds uint32, limit int) ([]*Visualization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	h := m.history[key]
	if len(h) == 0 {
		return []*Visualization{}, nil
	}
	start := 0
	if len(h) > limit {
		start = len(h) - limit
	}
	// return copies to avoid mutation
	out := make([]*Visualization, len(h)-start)
	copy(out, h[start:])
	return out, nil
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func renderBar(current, max float64, width int) string {
	if max <= 0 {
		return "[" + strings.Repeat(" ", width) + "]"
	}
	filled := int((current / max) * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// Phase 4: Snapshot to file (JSON of internal states)
func (m *InMemoryLimiter) Snapshot(ctx context.Context, dest string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Serialize simple state for demo
	snap := map[string]any{
		"ts": time.Now().Unix(),
		"tb": make(map[string]map[string]any),
		"sw_count": make(map[string]int),
	}
	for k, s := range m.tb {
		snap["tb"].(map[string]map[string]any)[k] = map[string]any{"tokens": s.tokens, "last": s.lastRefill}
	}
	for k, evs := range m.sw {
		snap["sw_count"].(map[string]int)[k] = len(evs)
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0644)
}

func (m *InMemoryLimiter) Restore(ctx context.Context, src string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var snap map[string]any
	if err := json.Unmarshal(b, &snap); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Demo restore - reset and note
	m.tb = make(map[string]*tbState)
	// In full impl deserialize properly
	return nil
}

func (m *InMemoryLimiter) LogDecision(ctx context.Context, ev DecisionEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decisions = append(m.decisions, ev)
	// keep bounded
	if len(m.decisions) > 1000 {
		m.decisions = m.decisions[len(m.decisions)-1000:]
	}
	return nil
}

func (m *InMemoryLimiter) Replay(ctx context.Context, fromTs, toTs int64) ([]DecisionEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []DecisionEvent{}
	for _, ev := range m.decisions {
		if (fromTs == 0 || ev.Timestamp >= fromTs) && (toTs == 0 || ev.Timestamp <= toTs) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Phase 5 replication (simple in-memory replicated store)
var (
	globalReplicated = NewReplicatedStore("inmemory-node")
)

func (m *InMemoryLimiter) EmitReplicationEvent(ctx context.Context, ev ReplicationEvent) error {
	// For in-mem demo, just apply locally
	m.ApplyReplicationEvent(ev)
	return nil
}

func (m *InMemoryLimiter) ApplyReplicationEvent(ev ReplicationEvent) bool {
	return globalReplicated.Apply(ev, nil)
}

func (m *InMemoryLimiter) GetReplicatedState(key string) (interface{}, bool) {
	v, ok, _ := globalReplicated.Get(key)
	return v, ok
}
