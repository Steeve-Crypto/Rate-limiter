# Rate-Limiter-Service Ambitious Evolution Plan

**Goal:** Evolve the current high-performance rate limiter microservice into a **production-grade Real-Time Coordination & Control Plane**.

This platform will combine:
- Sophisticated rate limiting with rich introspection
- Live visualization and simulation capabilities
- Real-time state replication with conflict resolution
- A unified policy engine and event log for distributed systems

The end state is a single, low-latency Go service (or coordinated set of services) that can be deployed as a sidecar, standalone, or cluster that safely manages high-throughput operations across many instances while giving operators and applications deep visibility and control.

---

## Vision & Principles

### Core Principles
1. **Everything is observable by default** — The `Visualize` interface is not a nice-to-have; it is a first-class citizen.
2. **Atomicity and low latency first** — Never sacrifice the sub-millisecond characteristics that make the current Redis + Lua design powerful.
3. **Unified event log** — Rate limit decisions, state mutations, and replication events should share the same durable log (Redis Streams, etc.).
4. **Pluggable everything** — Algorithms, conflict resolvers, policies, storage backends, and output formats must be replaceable via interfaces.
5. **Simulation & "what-if" built-in** — Being able to replay history against different policies is a superpower.
6. **Graceful degradation** — Must work extremely well in-memory for single-node / sidecar use, and scale cleanly with Redis/cluster.
7. **Bridge rate limiting and replication** — Rate limit state itself should be replicatable, and the same infrastructure should support other replicated primitives (counters, feature flags, leader election hints, session state).

### Target Use Cases
- High-QPS public APIs (per-user, per-IP, per-route limits)
- Internal microservice protection + backpressure signaling
- Multi-tenant platforms with hierarchical quotas
- Real-time collaborative systems that need both rate limits **and** consistent replicated state
- Operations tooling (dashboards, simulations, debugging production incidents)

---

## Current State (Baseline - June 2026)

- Go implementation with clean `Limiter` interface (`Check` + `Visualize`)
- Token Bucket + Sliding Window (in-memory + Redis + Lua)
- HTTP + CLI
- ASCII visualization + HTML view
- Docker support
- Basic tests

**Gaps:**
- No metrics/observability story
- No live or historical visualization
- No persistence beyond Redis TTLs
- No policy engine
- No replication / conflict resolution layer
- No multi-dimensional limits
- Limited distributed features

---

## Phased Roadmap

### Phase 1: Production Foundation (High Confidence, Immediate Value)

**Objective:** Make the service reliable and observable enough for serious production use.

**Key Deliverables:**
- Prometheus metrics (counters, gauges for tokens, window usage, decisions)
- Structured logging with correlation IDs
- OpenTelemetry tracing (optional but supported)
- Admin API (`/v1/admin/...`): reset key, inspect raw state, force refill
- Improved error handling, validation, and configuration via env + flags + (later) config file
- Basic benchmarks (using `testing.B` or a `benches/` package)
- Expanded test coverage, including miniredis for Redis logic
- Health/readiness endpoints that check backend connectivity

**Tasks:**
1. Add `/metrics` endpoint using `prometheus/client_golang`.
2. Instrument `Check` and `Visualize` with metrics (labels: `algorithm`, `backend`, `key_prefix` for cardinality control).
3. Introduce `admin` package or routes for key management.
4. Add structured logging (use `log/slog`).
5. Create benchmark suite that exercises both backends and algorithms under load.
6. Add integration test harness (miniredis + test HTTP client).
7. Document SLOs (p99 latency targets for in-memory vs Redis).

**Success Criteria:**
- Can run `go test -bench=.` and get meaningful numbers.
- Grafana dashboard sketch exists (even if just in docs).
- Production teams can reset a misbehaving key without restart.

---

### Phase 2: Supercharged Visualization & Live Introspection

**Objective:** Make the Visualize interface dramatically more powerful.

**Key Deliverables:**
- Live streaming visualization (SSE or WebSocket) for a key or set of keys.
- Historical window data (last N windows or time range).
- Richer diagram formats: Mermaid, simple SVG, JSON for frontend consumption.
- Side-by-side comparison and "what-if" simulation in the visualize layer.
- Visualization of replication events when Phase 5 lands.

**Tasks:**
1. Define `type Visualizer interface` more richly (currently it's a method on Limiter).
2. Add `StreamVisualize(ctx, key, opts)` that returns a channel or SSE writer.
3. Store lightweight history:
   - In-memory: ring buffer of recent windows.
   - Redis: use a secondary stream or sorted set per key for history.
4. Implement output formatters (pluggable `DiagramRenderer` interface).
5. Build a small static + HTMX single-file dashboard (`/dashboard`) that consumes the visualize endpoints.
6. Add simulation endpoint: `POST /v1/simulate` that replays a sequence of requests against a hypothetical policy without mutating state.

**Success Criteria:**
- Operator can open browser to `http://localhost:8080/visualize?key=foo&live=true` and see updates.
- "What-if 50% lower limit" simulation returns accurate diagrams.

---

### Phase 3: Advanced Rate Limiting & Policy Engine

**Objective:** Move beyond static per-request algorithm choice.

**Key Deliverables:**
- Hierarchical / multi-dimensional limits (e.g. `user:42` + `route:/api/v1/jobs` + `ip:1.2.3.4` combined).
- Configurable policies (YAML/JSON or via API) that select algorithm, limits, and cost functions based on key patterns or labels.
- Hot-reloadable policies (watch config file or Redis key).
- Additional algorithms (Leaky Bucket, Fixed Window Counter, Sliding Log for exactness when needed).
- Cost calculators (e.g. dynamic cost based on request size or complexity).

**Tasks:**
1. Introduce a `PolicyEngine` that takes a key + attributes and returns a resolved `LimitConfig` (algorithm, max, window, burst, cost func).
2. Support key hierarchies with inheritance (user-level + endpoint-level + global).
3. Add policy storage (in-memory map + Redis-backed for cluster).
4. Implement at least Leaky Bucket.
5. Create a `Policy` interface and default implementations.
6. Expose `GET /v1/policies` and `POST /v1/policies` (with validation).
7. Add label support in requests (`labels: { "tenant": "acme", "priority": "high" }`).

**Success Criteria:**
- A single key can be rate-limited by multiple overlapping dimensions with correct merging.
- Changing a policy file causes immediate behavior change without restart (for non-distributed case).

---

### Phase 4: Persistent + Replayable State

**Objective:** Turn rate limit state into durable, queryable, and replayable history.

**Key Deliverables:**
- Snapshot + restore of limiter state (to disk or Redis).
- Full event log of decisions (using Redis Streams or append-only log).
- Replay / simulation engine that can answer "what would the state be if we had used algorithm X with these limits?"
- Time-travel visualization.

**Tasks:**
1. Define a `StateStore` interface (separate from the algorithm logic).
2. Implement file-based snapshot (JSON or protobuf) and Redis snapshot.
3. Emit structured decision events to a log (key, algo, decision, timestamp, remaining, etc.).
4. Build a `Replayer` that can consume the event log and project state forward.
5. Expose `POST /v1/replay` API that takes a time range + alternate policy and returns projected results + diagrams.
6. Store enough data to support "last 24h" history queries for visualization.

**Success Criteria:**
- Operator can snapshot current state, restart the service, and restore.
- Can replay last hour of traffic against a new sliding window config and see different rejection rates.

---

### Phase 5: Unified Real-Time Coordination Platform (Ambitious Core)

**Objective:** Merge rate limiting with real-time replication + conflict resolution into one coherent system.

This is the biggest ambitious step. It unifies the original rate-limiter request with the data replication request.

**Vision:**
Use the same durable event log (Redis Streams or equivalent) for:
- Rate limit mutations
- General state replication events (upsert, delete, counter increments)
- Versioned records with conflict resolution

The service becomes a **coordination node** that can:
- Enforce rate limits
- Replicate arbitrary state across nodes/instances with LWW, vector clocks, or pluggable CRDTs
- Expose visualization for both rate limit buckets **and** replicated objects

**Key Deliverables:**
- Shared event log abstraction (build on or replace the replication demo in `../realtime-replicator/`).
- `ReplicatedState` interface + concrete implementations (e.g. `ReplicatedCounter`, `ReplicatedMap`, `ReplicatedFeatureFlag`).
- Conflict resolution strategies (LWW, Highest-Wins, custom merge functions).
- Rate limit state itself becomes replicatable (so multiple limiter instances stay eventually consistent even without a central Redis for the limits).
- Unified `Visualize` that can show both rate limit state and replicated objects.

**Tasks:**
1. Port/refactor the Python replication demo concepts into Go as a `replication/` package.
2. Define `Event` struct that can carry both rate limit decisions and general mutations.
3. Implement a `ReplicationHub` or `LogApplier` using Redis Streams + consumer groups (or NATS, etc.).
4. Add CRDT-inspired or vector-clock based conflict resolution (start simple with LWW + pluggable resolvers).
5. Make the existing `TokenBucketState` and `SlidingWindowState` implement the replication primitives.
6. Create example replicated objects: distributed counters, feature flags, session presence.
7. Update the Visualize layer to render both rate limits and replicated state.
8. Document how to run a small cluster of these services with replication.

**Success Criteria:**
- You can run 3 instances. Update a feature flag on one. See it appear (with conflict handling) on the others.
- Rate limit state for a hot key stays reasonably consistent across instances.
- The same Redis Streams topic powers both rate limiting and general replication.

---

### Phase 6: Distributed Scale & Resilience

**Objective:** Excellent multi-instance and multi-region behavior.

**Key Deliverables:**
- Cluster-aware visualization (aggregate view across instances via gossip or Redis).
- Peer-to-peer state exchange for ultra-low-latency local decisions with eventual consistency.
- Better handling of Redis outages (local degradation modes, circuit breakers).
- Consistent hashing or key sharding awareness.
- Support for multiple Redis clusters or hybrid backends.

**Tasks:**
1. Gossip or Redis-based instance registry + health.
2. `/v1/cluster/visualize?key=...` endpoint that fans out or reads from shared log.
3. Local fast-path + periodic reconciliation mode (two-tier rate limiting).
4. Backpressure signals exposed via headers or response fields.
5. Chaos testing scenarios (partition Redis, kill nodes).

---

### Phase 7: Ecosystem, SDKs, and Integrations

**Objective:** Make the service easy to adopt broadly.

**Key Deliverables:**
- Official Go client library (`github.com/crypto/rate-limiter-service/client`).
- Thin HTTP client for other languages + examples.
- gRPC service definition (in addition to REST).
- Helm chart + Kubernetes examples (including sidecar pattern).
- Integration examples:
  - Middleware for popular Go routers (chi, gin, echo).
  - Usage inside `hool-freelance` (replace/extend the Python rate limit logic).
- Full OpenAPI spec + generated clients.
- Comprehensive documentation site / examples repo structure.

**Tasks:**
1. Extract a clean client package.
2. Add gRPC support (using `google.golang.org/grpc`).
3. Create middleware packages.
4. Add a "cookbook" section in docs with common patterns.
5. Wire a proof-of-concept into the hool project if possible.
6. Publish (or prepare for publishing) as a proper module.

---

## Cross-Cutting Concerns (Apply Across All Phases)

- **Security**: mTLS support, auth on admin endpoints, key namespace isolation.
- **Performance Budgets**: Document and enforce p50/p99 targets.
- **Testing Strategy**:
  - Unit + property-based tests for algorithms
  - Integration with miniredis + real Redis in CI
  - Load + chaos tests
- **Observability**: Every new feature must emit metrics + traces.
- **Backward Compatibility**: New features must not break existing `/v1/check` contract.
- **Pluggability**: Use Go interfaces liberally. New algorithms, resolvers, renderers, and backends should be easy to add.

---

## Suggested Implementation Order (Pragmatic Path)

1. **Phase 1** (foundational value, quick wins)
2. **Phase 2** (leverage existing Visualize strength)
3. **Phase 3** (policy engine + hierarchy) — high user impact
4. **Phase 4** (persistence + replay) — enables the ambitious parts
5. **Phase 5** (replication unification) — the big ambitious bet
6. **Phase 6 + 7** in parallel or after

---

## Success Metrics (Long Term)

- Can handle > 100k QPS per instance in-memory with < 1ms p99.
- Cluster of 5+ nodes maintains < 5% discrepancy on hot keys.
- Visualization is used daily by operators for debugging production rate limit issues.
- The replication layer is used for at least one non-rate-limiting use case (e.g. distributed feature flags or counters).
- At least one external project (hool or new) successfully integrates it.

---

## Open Questions / Future Exploration

- Should we support other durable logs natively (NATS JetStream, Kafka, Postgres LISTEN/NOTIFY)?
- CRDTs vs. operational transformation for more complex replicated data?
- Should visualization be a separate lightweight sidecar service in very large clusters?
- Multi-region conflict resolution strategies?

---

**This plan is intentionally ambitious.** It takes the existing clean interface design and the two original user requests (advanced rate limiting + real-time replication with conflict resolution) and turns them into one coherent, high-leverage platform.

Once this plan is reviewed, individual phases can be broken into concrete implementation tasks or executed via subagents.