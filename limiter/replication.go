package limiter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// ReplicationEvent is the unified event for rate limits and general state replication (Phase 5).
type ReplicationEvent struct {
	EventID string      `json:"event_id"`
	Op      string      `json:"op"` // "upsert", "delete", "inc", "rate_decision" etc.
	Key     string      `json:"key"`
	Value   interface{} `json:"value"`
	Ts      int64       `json:"ts"`
	Node    string      `json:"node"`
	Version int         `json:"version"`
}

// ReplicatedStore manages replicated key-value state with conflict resolution (LWW + tiebreak by node+ver).
type ReplicatedStore struct {
	mu        sync.RWMutex
	NodeID    string
	Data      map[string]interface{}
	Versions  map[string]struct {
		Ts      int64
		Node    string
		Version int
	}
	Applied map[string]bool // event_id dedup
}

// NewReplicatedStore creates a new store for a node.
func NewReplicatedStore(nodeID string) *ReplicatedStore {
	return &ReplicatedStore{
		NodeID:   nodeID,
		Data:     make(map[string]interface{}),
		Versions: make(map[string]struct{ Ts int64; Node string; Version int }),
		Applied:  make(map[string]bool),
	}
}

// Apply applies an event with LWW conflict resolution. Returns true if applied.
func (s *ReplicatedStore) Apply(ev ReplicationEvent, resolver func(local, incoming interface{}, winner string) interface{}) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Applied[ev.EventID] {
		return false
	}
	s.Applied[ev.EventID] = true

	current, has := s.Versions[ev.Key]
	incoming := struct {
		Ts      int64
		Node    string
		Version int
	}{ev.Ts, ev.Node, ev.Version}

	if !has {
		if ev.Op == "upsert" || ev.Op == "inc" {
			s.Data[ev.Key] = ev.Value
		} else if ev.Op == "delete" {
			delete(s.Data, ev.Key)
		}
		s.Versions[ev.Key] = incoming
		return true
	}

	// LWW: higher ts, or same ts + higher node, or same + higher version
	if (incoming.Ts > current.Ts) ||
		(incoming.Ts == current.Ts && incoming.Node > current.Node) ||
		(incoming.Ts == current.Ts && incoming.Node == current.Node && incoming.Version > current.Version) {
		winner := "incoming"
		resolved := ev.Value
		if resolver != nil && (ev.Op == "upsert" || ev.Op == "inc") {
			resolved = resolver(s.Data[ev.Key], ev.Value, winner)
		}
		if ev.Op == "upsert" || ev.Op == "inc" {
			s.Data[ev.Key] = resolved
		} else if ev.Op == "delete" {
			delete(s.Data, ev.Key)
		}
		s.Versions[ev.Key] = incoming
		return true
	}
	// current wins
	return false
}

// Get returns value and version info.
func (s *ReplicatedStore) Get(key string) (interface{}, bool, struct{ Ts int64; Node string; Version int }) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.Data[key]
	ver := s.Versions[key]
	return v, ok, ver
}

// Replicator coordinates replication using Redis Streams (unifies with Phase 4 event log).
type Replicator struct {
	Store    *ReplicatedStore
	Client   *redis.Client
	Stream   string // e.g. "rl:replication" or reuse "rl:decisions"
	NodeID   string
	stopCh   chan struct{}
}

// NewReplicator creates a replicator.
func NewReplicator(nodeID string, client *redis.Client, stream string) *Replicator {
	if stream == "" {
		stream = "rl:replication"
	}
	return &Replicator{
		Store:  NewReplicatedStore(nodeID),
		Client: client,
		Stream: stream,
		NodeID: nodeID,
		stopCh: make(chan struct{}),
	}
}

// Emit publishes an event to the stream.
func (r *Replicator) Emit(ctx context.Context, op, key string, value interface{}, version int) error {
	ev := ReplicationEvent{
		EventID: fmt.Sprintf("%s-%d-%s", r.NodeID, time.Now().UnixNano(), key),
		Op:      op,
		Key:     key,
		Value:   value,
		Ts:      time.Now().UnixMilli(),
		Node:    r.NodeID,
		Version: version,
	}
	data := map[string]interface{}{
		"event_id": ev.EventID,
		"op":       ev.Op,
		"key":      ev.Key,
		"value":    ev.Value,
		"ts":       ev.Ts,
		"node":     ev.Node,
		"version":  ev.Version,
	}
	_, err := r.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: r.Stream,
		Values: data,
	}).Result()
	return err
}

// StartConsumer starts consuming the stream and applying events (simple single consumer for demo).
func (r *Replicator) StartConsumer(ctx context.Context) {
	go func() {
		lastID := "0-0"
		for {
			select {
			case <-r.stopCh:
				return
			default:
				streams, err := r.Client.XRead(ctx, &redis.XReadArgs{
					Streams: []string{r.Stream, lastID},
					Count:   10,
					Block:   1 * time.Second,
				}).Result()
				if err != nil {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				for _, stream := range streams {
					for _, msg := range stream.Messages {
						ev := ReplicationEvent{
							EventID: msg.Values["event_id"].(string),
							Op:      msg.Values["op"].(string),
							Key:     msg.Values["key"].(string),
							Value:   msg.Values["value"],
							Ts:      int64(msg.Values["ts"].(int64)), // adjust type
							Node:    msg.Values["node"].(string),
							Version: int(msg.Values["version"].(int64)),
						}
						r.Store.Apply(ev, nil) // simple LWW, no custom resolver for now
						lastID = msg.ID
					}
				}
			}
		}
	}()
}

func (r *Replicator) Stop() {
	close(r.stopCh)
}

// For rate limit state replication example: replicate a decision or bucket update.
func (r *Replicator) ReplicateRateDecision(ctx context.Context, key string, allowed bool, remaining uint32) error {
	return r.Emit(ctx, "rate_decision", key, map[string]interface{}{
		"allowed":   allowed,
		"remaining": remaining,
	}, 1)
}

// ReplicatedCounter example (uses inc op).
type ReplicatedCounter struct {
	*ReplicatedStore
}

func NewReplicatedCounter(nodeID string) *ReplicatedCounter {
	return &ReplicatedCounter{NewReplicatedStore(nodeID)}
}

func (c *ReplicatedCounter) Inc(key string, delta int) {
	c.ReplicatedStore.Apply(ReplicationEvent{
		Op:      "inc",
		Key:     key,
		Value:   delta,
		Ts:      time.Now().UnixMilli(),
		Node:    c.NodeID,
		Version: 1,
	}, nil)
}

func (c *ReplicatedCounter) Value(key string) int {
	v, ok, _ := c.Get(key)
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}