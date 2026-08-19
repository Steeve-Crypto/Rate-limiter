# Rate Limiter Service

**High-performance, low-latency rate limiting microservice** written in Go.

Token-bucket and sliding-window algorithms with Redis-backed atomic decisions, full observability, React control center, and production-oriented APIs.

Designed for real traffic: in-memory mode targets >50k requests/sec per core with sub-1 ms p99 latency.

## Core features

- **Algorithms**: Token Bucket (bursts), Sliding Window (fair boundaries), Leaky Bucket
- **Backends**: Blazing-fast in-memory + Redis (Lua scripts for atomic multi-instance safety)
- **Interfaces**: HTTP, gRPC, CLI, OpenAPI
- **Observability**: OpenTelemetry traces, Prometheus metrics, structured logging
- **Control Center**: React + Tailwind + Recharts dashboard with live SSE, policy management, cluster view, JSON/CSV export
- **Production extras**: Policy engine with labels, two-tier limiting, replication, admin API, Kubernetes-friendly

## Quick start

```bash
go build -o rate-limiter .

# In-memory
./rate-limiter -port 8080

# Redis-backed
./rate-limiter -port 8080 -redis localhost:6379
```

**Check endpoint**
```bash
curl -X POST http://localhost:8080/v1/check \
  -H 'Content-Type: application/json' \
  -d '{"key": "user:42:api", "max_tokens": 100, "window_seconds": 60, "algorithm": "token_bucket", "cost": 1}'
```

**Dashboard**
```
http://localhost:8080/dashboard
```

## Architecture highlights

- Clean `Limiter` interface → easy to swap backends or add algorithms
- Redis Lua for atomic check-and-update across instances
- OpenTelemetry spans on every critical path
- Policy engine supports hierarchical / label-based rules (e.g. VIP tier)
- Official Go + Python clients included

## Performance targets

| Mode       | p99 latency     | Throughput          |
|------------|-----------------|---------------------|
| In-memory  | < 1 ms          | > 50k QPS / core    |
| Redis      | 5–10 ms typical | Depends on network  |

Benchmarks and load tests are included. See `go test ./limiter -bench=.` and `scripts/loadtest.go`.

## Full documentation

The rest of this README covers:
- Detailed API & visualization endpoints
- Policy engine examples
- Distributed / two-tier mode
- gRPC, OpenAPI, client libraries
- Docker, Kubernetes sidecar patterns
- Grafana dashboard & chaos testing

See the sections below for everything you need to run it in production.

---

## Detailed Reference

### Visualize Interface

```bash
curl "http://localhost:8080/v1/visualize?key=user:42:api&algorithm=token_bucket&max_tokens=100&window_seconds=60"
curl "http://localhost:8080/v1/visualize?...&format=html"
```

### CLI

```bash
./rate-limiter check --key "demo" --max-tokens 5 --window 10 --algo token_bucket
./rate-limiter visualize --key "demo" --max-tokens 5 --window 10 --algo sliding_window
```

### Docker

```bash
docker compose up --build
```

### Design

```go
type Limiter interface {
    Check(ctx context.Context, req CheckRequest) (*CheckResponse, error)
    Visualize(...) (*Visualization, error)
}
```

In-memory uses maps + mutex. Redis uses Lua scripts.

### Ecosystem (Phase 7)

- Go client (`client/client.go`)
- Python client
- gRPC on port+1
- OpenAPI + generated clients
- Chi / Gin / Echo middleware patterns
- OpenTelemetry + Prometheus
- Admin token protection
- Namespace isolation for multi-tenant use

### Distributed features

- Node registry + cluster visualization
- Two-tier (local + Redis) with graceful degradation
- Replication via Redis Streams (LWW)
- Policy engine with dynamic rules

### Testing & Observability

- Benchmarks, load tests, chaos tests included
- Grafana dashboard JSON provided
- Full metrics and tracing out of the box

Built for real production use cases. Feedback welcome.
