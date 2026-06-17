package limiter

import (
	"context"
	"fmt"
	"math"

	redis "github.com/go-redis/redis/v8"
)

// Leaky bucket state for in-memory
type lbState struct {
	volume    float64
	lastLeak  int64 // unix milli
}

func (m *InMemoryLimiter) checkLeakyBucket(req CheckRequest) *CheckResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := nowUnixMilli()
	windowMs := int64(req.WindowSeconds) * 1000
	ratePerMs := float64(req.MaxTokens) / float64(windowMs) // leak rate

	state, ok := m.lb[req.Key]
	if !ok {
		state = &lbState{volume: 0, lastLeak: now}
		m.lb[req.Key] = state
	}

	// Leak
	elapsed := float64(now - state.lastLeak)
	leak := elapsed * ratePerMs
	state.volume = math.Max(0, state.volume-leak)
	state.lastLeak = now

	capacity := float64(req.MaxTokens)
	allowed := state.volume+float64(req.Cost) <= capacity
	if allowed {
		state.volume += float64(req.Cost)
	}

	remaining := uint32(math.Max(0, capacity-state.volume))

	resp := &CheckResponse{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     req.MaxTokens,
		ResetAt:   nowUnix() + int64(req.WindowSeconds),
		Algorithm: LeakyBucket,
	}

	if !allowed {
		// estimate time to leak enough space
		needed := state.volume + float64(req.Cost) - capacity
		if ratePerMs > 0 {
			retryMs := int64(needed / ratePerMs)
			resp.RetryAfterMs = &retryMs
		}
	}
	return resp
}

// For Redis Leaky
func (r *RedisLimiter) checkLeakyBucketLua(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	script := redis.NewScript(`
		local key = KEYS[1]
		local max_tokens = tonumber(ARGV[1])
		local window_ms = tonumber(ARGV[2])
		local cost = tonumber(ARGV[3])
		local now = tonumber(ARGV[4])

		local data = redis.call('HMGET', key, 'volume', 'last')
		local volume = tonumber(data[1]) or 0
		local last = tonumber(data[2]) or now

		local rate = max_tokens / window_ms
		local elapsed = now - last
		volume = math.max(0, volume - elapsed * rate)
		last = now

		local allowed = (volume + cost) <= max_tokens
		if allowed then
			volume = volume + cost
		end

		redis.call('HMSET', key, 'volume', volume, 'last', last)
		redis.call('PEXPIRE', key, window_ms * 2)

		local remaining = math.max(0, math.floor(max_tokens - volume))
		local retry = 0
		if not allowed and rate > 0 then
			local needed = volume + cost - max_tokens
			retry = math.ceil(needed / rate)
		end
		local reset = math.floor((now + window_ms) / 1000)
		return {allowed and 1 or 0, remaining, retry, reset}
	`)

	now := nowUnixMilli()
	windowMs := int64(req.WindowSeconds) * 1000
	key := fmt.Sprintf("rl:lb:%s", req.Key)

	res, err := script.Run(ctx, r.client, []string{key},
		req.MaxTokens, windowMs, req.Cost, now).Slice()
	if err != nil {
		return nil, err
	}

	allowed := res[0].(int64) == 1
	rem := int64(res[1].(int64))
	retry := int64(res[2].(int64))
	reset := res[3].(int64)

	resp := &CheckResponse{
		Allowed:   allowed,
		Remaining: uint32(rem),
		Limit:     req.MaxTokens,
		ResetAt:   reset,
		Algorithm: LeakyBucket,
	}
	if !allowed && retry > 0 {
		resp.RetryAfterMs = &retry
	}
	return resp, nil
}