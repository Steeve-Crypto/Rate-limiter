package limiter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// RedisLimiter implements Limiter backed by Redis.
// Uses EVAL (Lua) for atomic Check operations.
type RedisLimiter struct {
	client *redis.Client
}

func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client}
}

func (r *RedisLimiter) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	start := time.Now()
	defer func() {
		CheckDuration.WithLabelValues(string(req.Algorithm), "redis").Observe(time.Since(start).Seconds())
	}()

	if req.Cost == 0 {
		req.Cost = 1
	}
	if req.MaxTokens == 0 || req.WindowSeconds == 0 {
		return nil, fmt.Errorf("max_tokens and window_seconds must be > 0")
	}

	var resp *CheckResponse
	var err error
	switch req.Algorithm {
	case TokenBucket:
		resp, err = r.checkTokenBucketLua(ctx, req)
	case SlidingWindow:
		resp, err = r.checkSlidingWindowLua(ctx, req)
	case LeakyBucket:
		resp, err = r.checkLeakyBucketLua(ctx, req)
	default:
		return nil, fmt.Errorf("unknown algorithm: %s", req.Algorithm)
	}
	if err != nil {
		return nil, err
	}

	allowedStr := "false"
	if resp.Allowed {
		allowedStr = "true"
	}
	ChecksTotal.WithLabelValues(string(req.Algorithm), allowedStr).Inc()

	return resp, nil
}

// checkTokenBucketLua - atomic token bucket using Lua
func (r *RedisLimiter) checkTokenBucketLua(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	script := redis.NewScript(`
		local key = KEYS[1]
		local max_tokens = tonumber(ARGV[1])
		local window_ms = tonumber(ARGV[2])
		local cost = tonumber(ARGV[3])
		local now = tonumber(ARGV[4])

		local data = redis.call('HMGET', key, 'tokens', 'last')
		local tokens = tonumber(data[1]) or max_tokens
		local last = tonumber(data[2]) or now

		local rate = max_tokens / window_ms
		tokens = math.min(max_tokens, tokens + (now - last) * rate)

		local allowed = tokens >= cost
		if allowed then
			tokens = tokens - cost
		end

		redis.call('HMSET', key, 'tokens', tokens, 'last', now)
		redis.call('PEXPIRE', key, window_ms * 2)

		local remaining = math.floor(tokens)
		local retry = 0
		if not allowed and rate > 0 then
			retry = math.ceil((cost - tokens) / rate)
		elseif not allowed then
			retry = window_ms
		end

		local reset = math.floor((now + window_ms) / 1000)
		return { allowed and 1 or 0, remaining, retry, reset }
	`)

	now := nowUnixMilli()
	windowMs := int64(req.WindowSeconds) * 1000
	key := fmt.Sprintf("rl:tb:%s", req.Key)

	res, err := script.Run(ctx, r.client, []string{key},
		req.MaxTokens, windowMs, req.Cost, now).Slice()
	if err != nil {
		return nil, err
	}

	allowed := res[0].(int64) == 1
	remaining := int64(res[1].(int64))
	retry := int64(res[2].(int64))
	reset := res[3].(int64)

	resp := &CheckResponse{
		Allowed:   allowed,
		Remaining: uint32(remaining),
		Limit:     req.MaxTokens,
		ResetAt:   reset,
		Algorithm: TokenBucket,
	}
	if !allowed && retry > 0 {
		resp.RetryAfterMs = &retry
	}
	return resp, nil
}

// checkSlidingWindowLua - uses ZSET of timestamps
func (r *RedisLimiter) checkSlidingWindowLua(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	script := redis.NewScript(`
		local key = KEYS[1]
		local max_tokens = tonumber(ARGV[1])
		local window_ms = tonumber(ARGV[2])
		local cost = tonumber(ARGV[3])
		local now = tonumber(ARGV[4])
		local min_score = now - window_ms

		redis.call('ZREMRANGEBYSCORE', key, '-inf', min_score)
		local count = redis.call('ZCARD', key)

		local allowed = (count + cost) <= max_tokens
		if allowed then
			for i = 1, cost do
				redis.call('ZADD', key, now + i, tostring(now) .. ':' .. i)
			end
		end
		redis.call('PEXPIRE', key, window_ms * 2)

		local remaining = math.max(0, max_tokens - (count + (allowed and cost or 0)))
		local retry = 0
		if not allowed then
			local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
			if #oldest >= 2 then
				local ts = tonumber(oldest[2])
				retry = math.max(1, ts + window_ms - now)
			else
				retry = window_ms
			end
		end
		local reset = math.floor((now + window_ms) / 1000)
		return {allowed and 1 or 0, remaining, retry, reset}
	`)

	now := nowUnixMilli()
	windowMs := int64(req.WindowSeconds) * 1000
	key := fmt.Sprintf("rl:sw:%s", req.Key)

	res, err := script.Run(ctx, r.client, []string{key},
		req.MaxTokens, windowMs, req.Cost, now).Slice()
	if err != nil {
		return nil, err
	}

	allowed := res[0].(int64) == 1
	remaining := int64(res[1].(int64))
	retry := int64(res[2].(int64))
	reset := res[3].(int64)

	resp := &CheckResponse{
		Allowed:   allowed,
		Remaining: uint32(remaining),
		Limit:     req.MaxTokens,
		ResetAt:   reset,
		Algorithm: SlidingWindow,
	}
	if !allowed && retry > 0 {
		resp.RetryAfterMs = &retry
	}
	return resp, nil
}

// === VISUALIZE FOR REDIS ===

func (r *RedisLimiter) Visualize(ctx context.Context, key string, algo Algorithm, maxTokens, windowSeconds uint32) (*Visualization, error) {
	start := time.Now()
	defer func() {
		VisualizeDuration.WithLabelValues(string(algo), "redis").Observe(time.Since(start).Seconds())
		VisualizeTotal.WithLabelValues(string(algo)).Inc()
	}()

	switch algo {
	case TokenBucket:
		viz, err := r.visualizeTokenBucket(ctx, key, maxTokens, windowSeconds)
		if err == nil {
			r.recordHistory(ctx, key, viz)
		}
		return viz, err
	case SlidingWindow:
		viz, err := r.visualizeSlidingWindow(ctx, key, maxTokens, windowSeconds)
		if err == nil {
			r.recordHistory(ctx, key, viz)
		}
		return viz, err
	default:
		return nil, fmt.Errorf("unknown algorithm: %s", algo)
	}
}

func (r *RedisLimiter) visualizeTokenBucket(ctx context.Context, key string, maxTokens, windowSeconds uint32) (*Visualization, error) {
	fullKey := fmt.Sprintf("rl:tb:%s", key)
	data, err := r.client.HGetAll(ctx, fullKey).Result()
	if err != nil {
		return nil, err
	}

	now := nowUnixMilli()
	windowMs := int64(windowSeconds) * 1000
	ratePerSec := float64(maxTokens) / float64(windowSeconds)

	tokens := float64(maxTokens)
	last := now

	if t, ok := data["tokens"]; ok {
		fmt.Sscanf(t, "%f", &tokens)
	}
	if l, ok := data["last"]; ok {
		fmt.Sscanf(l, "%d", &last)
	}

	elapsed := now - last
	refilled := float64(elapsed) * (float64(maxTokens) / float64(windowMs))
	current := tokens + refilled
	if current > float64(maxTokens) {
		current = float64(maxTokens)
	}

	bar := renderBar(current, float64(maxTokens), 30)

	state := map[string]any{
		"current_tokens": fmt.Sprintf("%.2f", current),
		"max_tokens":     maxTokens,
		"rate_per_sec":   fmt.Sprintf("%.2f", ratePerSec),
		"raw_tokens":     data["tokens"],
		"last_refill":    last,
	}

	diagram := fmt.Sprintf(`Token Bucket (Redis) [%s]
Capacity : %d
Current  : %.1f
Rate     : %.2f /sec
Last fill: %d ms ago

%s`, key, maxTokens, current, ratePerSec, elapsed, bar)

	return &Visualization{
		Algorithm: string(TokenBucket),
		Key:       key,
		State:     state,
		Diagram:   diagram,
	}, nil
}

func (r *RedisLimiter) visualizeSlidingWindow(ctx context.Context, key string, maxTokens, windowSeconds uint32) (*Visualization, error) {
	fullKey := fmt.Sprintf("rl:sw:%s", key)
	now := nowUnixMilli()
	windowMs := int64(windowSeconds) * 1000
	cutoff := float64(now - windowMs)

	// Fetch members in window
	members, err := r.client.ZRangeByScoreWithScores(ctx, fullKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("%f", cutoff),
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}

	count := len(members)

	// Extract timestamps for diagram
	var tsList []int64
	for _, z := range members {
		tsList = append(tsList, int64(z.Score))
	}
	sort.Slice(tsList, func(i, j int) bool { return tsList[i] < tsList[j] })

	percent := 0.0
	if maxTokens > 0 {
		percent = float64(count) / float64(maxTokens) * 100
	}
	bar := renderBar(float64(count), float64(maxTokens), 30)

	var recent []string
	for i := len(tsList) - 1; i >= 0 && len(recent) < 6; i-- {
		ageSec := (now - tsList[i]) / 1000
		recent = append(recent, fmt.Sprintf("%ds", ageSec))
	}

	state := map[string]any{
		"requests_in_window": count,
		"max_tokens":         maxTokens,
		"window_seconds":     windowSeconds,
	}

	diagram := fmt.Sprintf(`Sliding Window (Redis) [%s]
Window   : %ds
In window: %d / %d  (%.1f%%)

%s

Recent (seconds ago): %s`, key, windowSeconds, count, maxTokens, percent, bar, strings.Join(recent, ", "))

	return &Visualization{
		Algorithm: string(SlidingWindow),
		Key:       key,
		State:     state,
		Diagram:   diagram,
	}, nil
}

// Reset removes all rate limit state for the key in Redis.
func (r *RedisLimiter) Reset(ctx context.Context, key string) error {
	tbKey := fmt.Sprintf("rl:tb:%s", key)
	swKey := fmt.Sprintf("rl:sw:%s", key)
	_, err := r.client.Del(ctx, tbKey, swKey).Result()
	return err
}

// Inspect returns raw Redis state for the key.
func (r *RedisLimiter) Inspect(ctx context.Context, key string) (map[string]any, error) {
	result := map[string]any{
		"key": key,
	}

	tbKey := fmt.Sprintf("rl:tb:%s", key)
	tbData, err := r.client.HGetAll(ctx, tbKey).Result()
	if err == nil && len(tbData) > 0 {
		result["token_bucket"] = tbData
	}

	swKey := fmt.Sprintf("rl:sw:%s", key)
	// Get recent members for inspection
	members, err := r.client.ZRangeWithScores(ctx, swKey, 0, 20).Result()
	if err == nil && len(members) > 0 {
		result["sliding_window_sample"] = members
		result["sliding_window_count"] = r.client.ZCard(ctx, swKey).Val()
	}

	return result, nil
}

// recordHistory pushes a viz snapshot to Redis history ZSET for the key.
func (r *RedisLimiter) recordHistory(ctx context.Context, key string, viz *Visualization) {
	if viz == nil {
		return
	}
	histKey := fmt.Sprintf("rl:hist:%s", key)
	// store as JSON score by ts
	data, _ := json.Marshal(viz) // ignore error for simplicity
	score := float64(nowUnixMilli())
	r.client.ZAdd(ctx, histKey, &redis.Z{Score: score, Member: string(data)}).Result()
	// trim to last 50
	r.client.ZRemRangeByRank(ctx, histKey, 0, -51)
}

// History fetches recent history from Redis ZSET.
func (r *RedisLimiter) History(ctx context.Context, key string, algo Algorithm, maxTokens, windowSeconds uint32, limit int) ([]*Visualization, error) {
	if limit <= 0 {
		limit = 20
	}
	histKey := fmt.Sprintf("rl:hist:%s", key)
	// get latest
	members, err := r.client.ZRevRange(ctx, histKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*Visualization, 0, len(members))
	for _, m := range members {
		var v Visualization
		if json.Unmarshal([]byte(m), &v) == nil {
			out = append(out, &v)
		}
	}
	// reverse to chronological
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

