# Next Phase Items (Post Core 1-7 + Recent UI/Clients Work)

**Status as of 2026-06-17**

## Audit Summary
Core of the ambitious plan is **largely complete**:

- Phases 1-6 fully delivered in functionality (metrics, structured logging + RequestID, admin, viz + SSE + simulate + history, PolicyEngine + Leaky + labels/hierarchy, snapshot/replay/event log, unified replication + LWW + ReplicatedStore + Streams, cluster registry + two-tier + backpressure).
- Phase 7 Ecosystem: Go client, Python client (manual + pyproject), OpenAPI + `./scripts/generate-clients.sh`, middlewares (chi/gin/echo), Helm + k8s, dashboard (now **React framework** with persistent log, exports, charts, full wiring — major upgrade over original HTMX/static), proto + pb (gRPC server code exists).
- Recent user requests (framework dashboard + persistent log + charts + export + Python + generated clients) **completed**.
- Dockerfile, docker-compose, replication unification, UseCase.md (fintech + start-to-finish), plan.md all present and updated.
- Tests: unit + miniredis + benchmarks (`go test ./limiter -bench=.`).

**Gaps (from plan.md + cross-cutting + UseCase.md + code):**

## Prioritized Remaining Items

### P0 - Production Hardening (High Value, Relatively Quick)
1. **OpenTelemetry Tracing** (Plan Phase 1 + cross-cut) ✅ **IMPLEMENTED**
   - Basic but functional: `initTracer()` with stdout exporter (syncer for visibility).
   - Spans for: `rate_limit.check`, `.visualize`, `.simulate`, `.replicate`, `.replay`, `.policies.*`, `.admin.*`
   - Rich attributes + events (allowed, rate_limited, key, algorithm, remaining, etc.)
   - Context propagation + defer shutdown.
   - Easy to extend to OTLP (just change exporter).
   - See main.go and README. Resource includes service name.

2. **Security Basics**
   - mTLS example for gRPC + HTTP (Docker/K8s snippets).
   - Simple auth middleware for admin endpoints (`/v1/admin/*`) and policies (API key or basic via env).
   - Key namespace helper / sanitizer (e.g., enforce tenant: prefixes).
   - Document in UseCase + README (fintech requires this).
   - Optional: rate-limit the rate-limiter itself on admin.

3. **gRPC Reliability**
   - Current: pb init panics on protobuf descriptor version skew → gRPC server disabled via build ignore + comments.
   - Fix: Either (a) proper re-generation with pinned protoc versions matching go.mod, or (b) robust build tags + separate package, or (c) vendor descriptor fix.
   - Re-enable server + update clients + add example gRPC usage.
   - Expose on 8081 by default when enabled.

4. **Dashboard / SPA Serving Robustness**
   - Current serves `index.html` + `/assets/*` + `/favicon.svg` via custom fs handlers.
   - Improve: Use `http.StripPrefix` + `http.FileServer` for `frontend/dist` when present.
   - `//go:embed frontend/dist` (conditional) for single-binary deployments.
   - Support HEAD requests properly.
   - Serve other public assets cleanly (icons.svg etc.).
   - Bonus: embed option controlled by build tag or flag.

### P1 - Testing, Performance & Resilience
5. **Expanded Testing & Chaos**
   - Add chaos scenarios (documented in plan): Redis partition simulation, node kill, high contention.
   - Use `toxiproxy` or simple fault injection in tests.
   - Property-based tests (e.g. `testing/quick` or `rapid`) for token bucket / sliding window invariants.
   - Load/soak bench script or `cmd/loadtest`.
   - CI notes (even if no .github yet).

6. **Performance Budgets + SLOs**
   - Formalize in docs: p99 <1ms in-memory, <5-10ms with Redis, QPS targets.
   - Add benchmark output assertions or golden files.
   - Grafana dashboard JSON example (in `grafana/` or docs).
   - Latency histograms already good via metrics.

### P2 - Ecosystem & Polish
7. **hool / External Integration Example**
   - Concrete small example (Go service or Python flask/fastapi using the client that enforces rate limits + replication for feature flags).
   - Or a `examples/hool/` folder demonstrating "replace the Python rate limit logic".

8. **Docs & Cookbook Expansion**
   - Run the generator and include a generated Python client example (or note).
   - Grafana + alert examples.
   - "Production deployment checklist" (two-tier, policies, security, monitoring).
   - Update UseCase.md with any new patterns.
   - Optional: simple docs site (mkdocs or just polished README + plan).

9. **Advanced Pluggability**
   - File watcher for policies (hot reload without API POST).
   - Config file support (viper or simple) beyond flags + env.
   - More replication resolvers (vector clocks example, or simple CRDT counter).
   - Pluggable backends for the event log (NATS, etc. — future exploration).

### Cross-Cutting (Always)
- Every addition must update metrics + (now) traces.
- Keep `/v1/check` contract stable.
- Update plan.md / NEXT-PHASES.md + README status after work.

## Suggested Order
1. Tracing (big observability win, easy to add now that slog + context are used).
2. gRPC fix + security basics (makes "production-grade coordination plane" real).
3. SPA serving + embed (nice for single-binary sidecar deploys).
4. Chaos + perf budgets + Grafana.
5. hool example + docs polish.

## Quick Wins Already Done Recently
- React framework dashboard with persistent log + exports + charts + full wiring.
- Python client + generation script + expanded OpenAPI.
- Short commits.

**Run `go test ./limiter -bench=. -benchmem` and `curl http://localhost:8080/metrics | grep rate_limit` to baseline.**

Next steps: Pick items above (or let me implement #1 + #4 now).
