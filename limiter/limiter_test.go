package limiter

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"
)

func TestInMemoryTokenBucket(t *testing.T) {
	l := NewInMemoryLimiter()

	req := CheckRequest{
		Key:           "test-tb",
		MaxTokens:     2,
		WindowSeconds: 60,
		Algorithm:     TokenBucket,
		Cost:          1,
	}

	r1, _ := l.Check(context.Background(), req)
	if !r1.Allowed || r1.Remaining != 1 {
		t.Fatalf("expected allowed with 1 remaining, got %+v", r1)
	}

	r2, _ := l.Check(context.Background(), req)
	if !r2.Allowed || r2.Remaining != 0 {
		t.Fatalf("expected allowed with 0 remaining, got %+v", r2)
	}

	r3, _ := l.Check(context.Background(), req)
	if r3.Allowed {
		t.Fatalf("expected rate limited, got allowed")
	}
	if r3.RetryAfterMs == nil {
		t.Error("expected retry_after_ms to be set")
	}
}

func TestInMemorySlidingWindow(t *testing.T) {
	l := NewInMemoryLimiter()

	req := CheckRequest{
		Key:           "test-sw",
		MaxTokens:     2,
		WindowSeconds: 60,
		Algorithm:     SlidingWindow,
		Cost:          1,
	}

	for i := 0; i < 2; i++ {
		r, _ := l.Check(context.Background(), req)
		if !r.Allowed {
			t.Fatalf("expected allowed on request %d", i)
		}
	}

	r, _ := l.Check(context.Background(), req)
	if r.Allowed {
		t.Fatal("expected denied on 3rd request")
	}
}

func TestVisualize(t *testing.T) {
	l := NewInMemoryLimiter()

	req := CheckRequest{
		Key:           "viz-test",
		MaxTokens:     5,
		WindowSeconds: 30,
		Algorithm:     TokenBucket,
		Cost:          1,
	}
	l.Check(context.Background(), req)

	viz, err := l.Visualize(context.Background(), "viz-test", TokenBucket, 5, 30)
	if err != nil {
		t.Fatal(err)
	}
	if viz.Algorithm != "token_bucket" {
		t.Error("wrong algorithm")
	}
	if viz.Diagram == "" {
		t.Error("diagram should not be empty")
	}
	if _, ok := viz.State["current_tokens"]; !ok {
		t.Error("expected current_tokens in state")
	}
}

// Benchmarks for Phase 1

func BenchmarkTokenBucket_InMemory(b *testing.B) {
	l := NewInMemoryLimiter()
	req := CheckRequest{
		Key:           "bench-tb",
		MaxTokens:     1000,
		WindowSeconds: 60,
		Algorithm:     TokenBucket,
		Cost:          1,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Check(context.Background(), req)
	}
}

func BenchmarkSlidingWindow_InMemory(b *testing.B) {
	l := NewInMemoryLimiter()
	req := CheckRequest{
		Key:           "bench-sw",
		MaxTokens:     1000,
		WindowSeconds: 60,
		Algorithm:     SlidingWindow,
		Cost:          1,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Check(context.Background(), req)
	}
}

func BenchmarkVisualize_InMemory(b *testing.B) {
	l := NewInMemoryLimiter()
	req := CheckRequest{Key: "bench-viz", MaxTokens: 100, WindowSeconds: 60, Algorithm: TokenBucket}
	l.Check(context.Background(), req)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Visualize(context.Background(), "bench-viz", TokenBucket, 100, 60)
	}
}

func TestRedisLimiter_WithMiniredis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	l := NewRedisLimiter(client)

	req := CheckRequest{
		Key:           "miniredis-test",
		MaxTokens:     2,
		WindowSeconds: 60,
		Algorithm:     TokenBucket,
		Cost:          1,
	}

	r1, err := l.Check(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Allowed {
		t.Error("expected allowed")
	}

	r2, _ := l.Check(context.Background(), req)
	if !r2.Allowed {
		t.Error("expected second allowed")
	}

	r3, _ := l.Check(context.Background(), req)
	if r3.Allowed {
		t.Error("expected rate limited on third")
	}

	// Test reset
	if err := l.Reset(context.Background(), "miniredis-test"); err != nil {
		t.Error(err)
	}

	r4, _ := l.Check(context.Background(), req)
	if !r4.Allowed {
		t.Error("expected allowed after reset")
	}
}

func TestLeakyBucket(t *testing.T) {
	l := NewInMemoryLimiter()
	req := CheckRequest{
		Key:           "lb-test",
		MaxTokens:     3,
		WindowSeconds: 10,
		Algorithm:     LeakyBucket,
		Cost:          1,
	}
	for i := 0; i < 3; i++ {
		r, _ := l.Check(context.Background(), req)
		if !r.Allowed {
			t.Fatalf("should allow %d", i)
		}
	}
	r, _ := l.Check(context.Background(), req)
	if r.Allowed {
		t.Error("should deny 4th immediately")
	}
}
