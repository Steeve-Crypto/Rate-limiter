# Real-Time Rate Limiter Service (Go)

High-performance, low-latency rate limiting microservice written in **Go**.

Implements:
- **Token Bucket** (great for bursts)
- **Sliding Window** (fairer window boundaries)

## Features

- Extremely fast in-memory mode (sync primitives)
- Redis-backed mode with Lua scripts for **atomic** operations across instances
- Full **Visualizer interface** for both algorithms (see below)
- Simple but powerful HTTP API + CLI
- Docker ready

## Quick Start

```bash
# Build
go build -o rate-limiter .

# Run (in-memory)
./rate-limiter -port 8080

# With Redis
./rate-limiter -port 8080 -redis localhost:6379
# or
REDIS_ADDR=localhost:6379 ./rate-limiter
```

## API

### Check / Consume

```bash
curl -X POST http://localhost:8080/v1/check \
  -H 'Content-Type: application/json' \
  -d '{
    "key": "user:42:api",
    "max_tokens": 100,
    "window_seconds": 60,
    "algorithm": "token_bucket",
    "cost": 1
  }'
```

Response (200 = allowed, 429 = limited):

```json
{
  "allowed": false,
  "remaining": 0,
  "limit": 100,
  "retry_after_ms": 1240,
  "reset_at": 1718640123,
  "algorithm": "token_bucket"
}
```

### Visualize Interface ★

This is the new feature requested.

```bash
# JSON
curl "http://localhost:8080/v1/visualize?key=user:42:api&algorithm=token_bucket&max_tokens=100&window_seconds=60"

# Pretty HTML (great for debugging in browser)
curl "http://localhost:8080/v1/visualize?key=user:42:api&algorithm=sliding_window&max_tokens=100&window_seconds=60&format=html"
```

**Example Token Bucket output:**

```
Token Bucket [user:42:api]
Capacity : 100
Current  : 67.3
Rate     : 1.67 tokens/sec
Last fill: 420 ms ago

[████████████████████░░░░░░░░░░░░░░░░░░░░]  67.3%
```

**Example Sliding Window output:**

```
Sliding Window [ip:1.2.3.4]
Window   : 60s
In window: 34 / 100

[██████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]

Recent (seconds ago): 0, 1, 3, 7, 12, 19
```

The `Visualize` method is defined on the `Limiter` interface and implemented for both algorithms in both backends (in-memory + Redis).

## CLI

```bash
# One-off check
./rate-limiter check --key "demo" --max-tokens 5 --window 10 --algo token_bucket

# Visualize from CLI
./rate-limiter visualize --key "demo" --max-tokens 5 --window 10 --algo sliding_window
```

## Algorithms

### Token Bucket
- Continuous refill
- Allows bursts up to capacity
- Best for most APIs

### Sliding Window
- Tracks actual request times in the window (via ZSET in Redis)
- Prevents "thundering herd" at window boundaries
- Slightly more expensive but very accurate

## Running with Docker

```bash
docker compose up --build
```

This brings up the service + Redis. The service will automatically use Redis when `-redis` is provided.

## Environment

- `-redis` / `REDIS_ADDR` → use Redis backend
- Port via `-port`

## Design Notes

The core is behind the `Limiter` interface:

```go
type Limiter interface {
    Check(ctx context.Context, req CheckRequest) (*CheckResponse, error)
    Visualize(...) (*Visualization, error)
}
```

This made it trivial to add rich visualization for both algorithms.

In-memory uses simple maps + mutex (fast enough for tens of thousands of QPS).

Redis uses Lua for atomic check+update.

## Development

```bash
go run main.go serve...   # not needed, binary supports flags directly
go build
go test ./...
```

See [plan.md](./plan.md) for the full ambitious roadmap (production hardening, live visualization, policy engine, persistent replay, unified replication + conflict resolution, etc.).

## Phase 1 (Production Foundation) - Implemented
- Prometheus metrics at `/metrics` (checks, latency histograms by algorithm/backend)
- Admin API: `POST /v1/admin/reset?key=...`, `GET /v1/admin/inspect?key=...`
- Structured logging with `slog` + request correlation IDs (via middleware)
- `/ready` endpoint (backend aware)
- Benchmarks: `go test ./limiter -bench=. -benchmem`
- Expanded tests with miniredis for Redis backend
- Improved validation

Example:
```bash
curl -s http://localhost:8080/metrics | grep rate_limit
curl -s "http://localhost:8080/v1/admin/inspect?key=user:123"
curl -X POST "http://localhost:8080/v1/admin/reset?key=user:123"
```
