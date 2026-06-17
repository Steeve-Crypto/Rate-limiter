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

See [UseCase.md](./UseCase.md) for real-world use cases and [plan.md](./plan.md) for roadmap.

## Phase 7: Ecosystem, SDKs, and Integrations - Implemented

- **Go Client Library**: See `client/client.go`. Simple, zero-dep HTTP client for Check, Visualize, Simulate, Replicate.
  ```go
  c := client.New("http://localhost:8080")
  resp, _ := c.Check(ctx, client.CheckRequest{Key: "user:42", MaxTokens: 100, WindowSeconds: 60})
  ```

- **gRPC Support**: gRPC server runs on `port+1` (e.g. 8081). See `limiter/grpc.go` for service interface. Use standard gRPC clients or reflection for now. Full .proto can be added easily.

## Beautiful Control Center UI (React Framework)

Visit `http://localhost:8080/dashboard` — served by the full **React + Vite + Tailwind + Recharts** dashboard in `frontend/`.

### Key features
- Persistent **Recent Results Log** panel (right sidebar) collecting actions across all tabs with live filter
- Export log to **JSON** and **CSV**
- Premium charts (distribution pie, activity line/bar) driven by actual results
- All major endpoints wired: Check, Visualize + Live SSE, Simulate, Policies, Replication, Cluster, Replay, Admin
- Results shown inline with color-coded cards, progress, and full backend polling (health + cluster every 15s)
- Tabbed professional layout — no more raw HTML

### Development (framework dashboard)
```bash
cd frontend
npm install
npm run dev          # http://localhost:5173 (proxies to Go on :8080)
npm run build        # produces dist/ — served automatically by Go at /dashboard
```

The legacy single-file dashboard.html is kept as fallback when no dist/ exists.

- **Middleware**:
  - `middleware/chi.go`: Ready-to-use chi middleware.
  - Similar patterns for gin/echo (adapt the handler func).

- **Cookbook / Examples** (added to this README):
  - Basic usage above.
  - Kubernetes sidecar: Run alongside app, use localhost for low latency.
  - Policy + labels example in previous phases.
  - Replication for distributed state.

- **hool-freelance Integration**: See example Python client below (or call HTTP directly from Python).

- **OpenAPI + Generated Clients**: Full spec in `openapi.yaml`. Run `./scripts/generate-clients.sh python` (or typescript/go) to emit clients using openapi-generator.

- **OpenTelemetry Tracing**: Initialized on startup (stdout exporter by default for demo). Spans are created for `rate_limit.check`, `visualize`, `simulate`, `replicate`, `replay`, `admin.*`, `policies.*` with rich attributes (key, algorithm, allowed, etc.) and events. 
  Use an OTLP collector in production. See code in main.go: `initTracer()` + `startSpan()`. Context is propagated.

- **Admin Security**: Set `ADMIN_TOKEN=...` env and send `X-Admin-Token` header to protect `/v1/admin/*` and sensitive ops. Easy to extend.
- **gRPC TLS/mTLS**: Use `-grpc-tls-cert`, `-grpc-tls-key`, `-grpc-tls-ca` flags (or env equiv). See `scripts/gen-grpc-certs.sh` to generate certs for demo/production mTLS.
- **Key Namespaces (richer security)**: Use `limiter.NamespaceKey("tenant:acme", labels, originalKey)` before checks to enforce isolation. `ValidateKeyNamespace` for admin enforcement. Prevents cross-tenant pollution in shared deployments (see UseCase.md for fintech/ multi-tenant examples).

- **Official Go Client**: `client/client.go`
- **Official Python Client**: `client/python/rate_limiter_client/` (zero-dep, or install via pyproject). See `client/python/README.md`.
- Also manual thin client for hool-freelance or quick use.

```python
from rate_limiter_client import RateLimiterClient
c = RateLimiterClient("http://localhost:8080")
resp = c.check("user:42:api", max_tokens=100)
print(resp.allowed, resp.remaining)
```

Full generated clients available for Python / TS / others from OpenAPI.

### Kubernetes Sidecar
```yaml
# In pod spec, sidecar container running the rate-limiter binary
# App talks to localhost:8080
```

Build the binary and include in your image or use the provided Dockerfile.


## Phase 6 (Distributed Scale & Resilience) - Implemented
- Redis-based node registry + health (nodes register with TTL).
- `/v1/cluster/nodes` and `/v1/cluster/visualize` for aggregate view.
- Two-tier rate limiting: local fast InMemory + async/global Redis with fallback (degradation on outage).
- Backpressure signals via HTTP headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`.
- Circuit-breaker style short timeouts + local fallback for Redis resilience.
- Notes on consistent hashing for sharding (key prefix hashing can be added easily).
- Usage: `./rate-limiter -two-tier -redis ...`

Example cluster viz:
```bash
curl http://localhost:8080/v1/cluster/visualize?key=user:42
```

## Phase 5 (Unified Real-Time Coordination Platform) - Implemented
- `ReplicationEvent` + `ReplicatedStore` with LWW conflict resolution (ported from demo, with node/version/tiebreak).
- `Replicator` using Redis Streams (unified with Phase 4 decision log).
- `ReplicatedCounter` and general replicated state examples.
- Rate limit decisions can emit replication events.
- New endpoints:
  - `POST /v1/replicate` (emit general replication event)
  - `GET /v1/replicated/{key}`
- Node ID support and consumer for applying remote events.
- Integrated with existing Limiter interface and policy engine.
- Unified log allows rate limiting + arbitrary replicated state (flags, counters) in one platform.

Example:
```bash
curl -X POST /v1/replicate -d '{"op":"upsert","key":"feature:beta","value":true,"node":"nodeA","version":1}'
curl /v1/replicated/feature:beta
```

## Phase 3 (Advanced Rate Limiting & Policy Engine) - Implemented
- New `leaky_bucket` algorithm
- `PolicyEngine` + `Policy` with pattern + label matching (hierarchical / multi-dimensional)
- Labels support in `CheckRequest`
- Dynamic policy management via `GET/POST /v1/policies`
- Policy resolution integrated into `/v1/check` (overrides defaults)
- Example: VIP label can use different algo/limits than default

Usage:
```bash
curl -X POST /v1/check -d '{"key":"u","labels":{"tier":"vip"},"cost":1}'
curl -X POST /v1/policies -d '{"name":"vip","pattern":"*","labels":{"tier":"vip"},"config":{"algorithm":"leaky_bucket","max_tokens":5,"window_seconds":5},"priority":100}'
```

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

## Load, Chaos & Performance Testing (Implemented)
- **Benchmarks**: `go test ./limiter -bench=. -benchmem` (includes concurrent load bench).
- **Load testing**: `go run scripts/loadtest.go -url http://localhost:8080 -concurrency 100 -duration 10s`
  Reports QPS, latency, allowed/rejected.
- **Chaos testing**: `go test ./limiter -run Chaos -count=5` (high contention goroutines + correctness checks).
  Simulates bursty / contended workloads. For real Redis chaos use toxiproxy or kill nodes manually.
- **Performance budgets** (documented + monitored):
  - In-memory: p99 < 1ms, > 50k QPS per core typical.
  - Redis-backed: p99 < 5-10ms under normal load.
  - Rejection rate alert > 20% for sustained period.
  See Grafana p99 stat panel for live budget violations.

Run under load and watch `/metrics` + Grafana.

## Grafana Dashboard
- Import [grafana/rate-limiter-dashboard.json](/grafana/rate-limiter-dashboard.json)
- Uses Prometheus datasource.
- Key panels: QPS, p50/p99 latency by algo+backend, allowed vs limited, rejection rate, budget indicator.
- Recommended alerts on p99 breach and high rejection rate.
