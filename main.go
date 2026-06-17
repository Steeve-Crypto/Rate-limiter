package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	// "net"   // gRPC disabled
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/crypto/rate-limiter-service/limiter"
	// pb import disabled (see limiter/grpc.go build ignore + regen note)
	// "github.com/crypto/rate-limiter-service/limiter/pb"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	redis "github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	// google.golang.org/grpc disabled for now (pb descriptor issue)
)

//go:embed ui/*
var uiFS embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	var (
		port     = flag.Int("port", 8080, "port")
		redisURL = flag.String("redis", "", "redis")
		nodeID   = flag.String("node-id", "node-"+fmt.Sprintf("%d", time.Now().UnixNano()%10000), "node identifier for cluster")
		twoTier  = flag.Bool("two-tier", false, "enable local fast-path + Redis reconciliation")
	)
	flag.Parse()

	var lim limiter.Limiter
	var rdb *redis.Client
	var replicator *limiter.Replicator
	if *redisURL != "" {
		rdb = redis.NewClient(&redis.Options{Addr: *redisURL})
		if rdb.Ping(context.Background()).Err() == nil {
			lim = limiter.NewRedisLimiter(rdb)
			replicator = limiter.NewReplicator(*nodeID, rdb, "rl:replication")
			replicator.StartConsumer(context.Background())
			// Phase 6: register node for cluster awareness
			registerNode(rdb, *nodeID)
		} else {
			slog.Warn("redis unavailable")
		}
	}
	if lim == nil {
		lim = limiter.NewInMemoryLimiter()
		slog.Info("using inmemory")
	}

	// Phase 6: two-tier mode - local inmemory + global Redis
	if *twoTier && rdb != nil {
		local := limiter.NewInMemoryLimiter()
		lim = &twoTierLimiter{local: local, global: lim.(*limiter.RedisLimiter), rdb: rdb, nodeID: *nodeID}
		slog.Info("two-tier mode enabled")
	}

	policyEngine := limiter.NewPolicyEngine()
	// seed a default example policy
	policyEngine.AddPolicy(limiter.Policy{
		Name:    "default",
		Pattern: "*",
		Config: limiter.LimitConfig{
			Algorithm:     limiter.TokenBucket,
			MaxTokens:     100,
			WindowSeconds: 60,
		},
		Priority: 0,
	})

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request){
		info := map[string]any{"ok":true, "backend": limiter.BackendName(lim), "node": *nodeID}
		if rdb != nil {
			info["cluster_nodes"] = len(getNodes(rdb))
		}
		writeJSON(w, 200, info)
	})
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request){ writeJSON(w, 200, map[string]any{"ready":true}) })
	r.Handle("/metrics", promhttp.Handler())

	// Phase 6: cluster-aware endpoints
	r.Get("/v1/cluster/nodes", func(w http.ResponseWriter, r *http.Request) {
		if rdb != nil {
			writeJSON(w, 200, map[string]any{"nodes": getNodes(rdb), "self": *nodeID})
		} else {
			writeJSON(w, 200, map[string]any{"nodes": []string{*nodeID}, "self": *nodeID})
		}
	})
	r.Get("/v1/cluster/visualize", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		// For demo: local viz + cluster info. In real, fanout via registry or aggregate from stream
		a := limiter.Algorithm(r.URL.Query().Get("algorithm"))
		if a == "" { a = limiter.TokenBucket }
		mt := uint32(100); if v, _ := strconv.ParseUint(r.URL.Query().Get("max_tokens"), 10, 32); v > 0 { mt = uint32(v) }
		ws := uint32(60); if v, _ := strconv.ParseUint(r.URL.Query().Get("window_seconds"), 10, 32); v > 0 { ws = uint32(v) }
		viz, _ := lim.Visualize(r.Context(), key, a, mt, ws)
		nodes := []string{*nodeID}
		if rdb != nil { nodes = getNodes(rdb) }
		writeJSON(w, 200, map[string]any{
			"key": key,
			"viz": viz,
			"cluster": map[string]any{"nodes": nodes, "note": "aggregated view (demo: local + registry)"},
		})
	})

	r.Post("/v1/check", func(w http.ResponseWriter, r *http.Request) {
		var req limiter.CheckRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.MaxTokens == 0 { req.MaxTokens=100 }
		if req.WindowSeconds==0 { req.WindowSeconds=60 }
		if req.Cost==0 { req.Cost=1 }
		if req.Algorithm == "" { req.Algorithm = "token_bucket" }

		// Phase 3: resolve policy if not fully specified
		if cfg, ok := policyEngine.Resolve(req.Key, req.Labels); ok {
			if req.Algorithm == "" {
				req.Algorithm = cfg.Algorithm
			}
			if req.MaxTokens == 100 { // default, override
				req.MaxTokens = cfg.MaxTokens
			}
			if req.WindowSeconds == 60 {
				req.WindowSeconds = cfg.WindowSeconds
			}
		}

		resp, _ := lim.Check(r.Context(), req)
		st := 200; if !resp.Allowed { st=429 }

		// Phase 4: log decision
		lim.LogDecision(r.Context(), limiter.DecisionEvent{
			Timestamp: time.Now().UnixMilli(),
			Key:       req.Key,
			Algorithm: req.Algorithm,
			Allowed:   resp.Allowed,
			Remaining: resp.Remaining,
			Limit:     resp.Limit,
			Cost:      req.Cost,
			Labels:    req.Labels,
		})

		// Phase 5: also emit as replication event for rate state replication
		lim.EmitReplicationEvent(r.Context(), limiter.ReplicationEvent{
			Op:      "rate_decision",
			Key:     req.Key,
			Value:   map[string]any{"allowed": resp.Allowed, "remaining": resp.Remaining},
			Ts:      time.Now().UnixMilli(),
			Node:    "default",
			Version: 1,
		})

		// Phase 6: backpressure signals
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(int(resp.Limit)))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(resp.Remaining)))
		if resp.RetryAfterMs != nil {
			w.Header().Set("Retry-After", strconv.FormatInt(*resp.RetryAfterMs/1000, 10))
		}

		writeJSON(w, st, resp)
	})

	r.Get("/v1/visualize", func(w http.ResponseWriter, r *http.Request) {
		k := r.URL.Query().Get("key"); if k=="" { http.Error(w,"key",400); return }
		a := limiter.Algorithm(r.URL.Query().Get("algorithm")); if a=="" { a=limiter.TokenBucket }
		mt:=uint32(100); if v,_:=strconv.ParseUint(r.URL.Query().Get("max_tokens"),10,32);v>0{mt=uint32(v)}
		ws:=uint32(60); if v,_:=strconv.ParseUint(r.URL.Query().Get("window_seconds"),10,32);v>0{ws=uint32(v)}
		viz, _ := lim.Visualize(r.Context(), k, a, mt, ws)
		if r.URL.Query().Get("format") == "html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			historyHTML := ""
			if r.URL.Query().Get("include_history") == "true" {
				h, _ := lim.History(r.Context(), k, a, mt, ws, 5)
				for _, item := range h {
					historyHTML += fmt.Sprintf(`<div class="mb-2 p-2 bg-zinc-900 rounded text-xs"><strong>%s</strong><br><pre class="text-[10px] mt-1">%s</pre></div>`, item.Algorithm, item.Diagram)
				}
			}
			fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Visualize • %s</title><script src="https://cdn.tailwindcss.com"></script></head>
<body class="bg-zinc-950 text-zinc-200 p-6 font-sans">
<div class="max-w-4xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <div><h1 class="text-2xl font-semibold tracking-tight">RateFlow Visualize</h1><p class="text-zinc-400 text-sm">%s</p></div>
    <a href="/dashboard" class="text-xs px-3 py-1 bg-zinc-800 hover:bg-zinc-700 rounded-2xl">Open Full Dashboard →</a>
  </div>
  <div class="bg-zinc-900 border border-zinc-800 rounded-3xl p-6 mb-6">
    <div class="flex items-center gap-3 mb-4">
      <span class="px-3 py-1 text-xs bg-indigo-600/20 text-indigo-400 rounded-2xl">%s</span>
      <span class="text-sm text-zinc-400">Live snapshot</span>
    </div>
    <pre class="text-emerald-400 text-sm leading-tight whitespace-pre overflow-auto p-4 bg-black/60 rounded-2xl">%s</pre>
  </div>
  %s
  <div class="text-xs text-zinc-500">Powered by RateFlow • <a href="/v1/visualize?key=%s&algorithm=%s&max_tokens=%d&window_seconds=%d" class="underline">JSON</a></div>
</div>
<script>tailwind.config = {theme:{extend:{}}}</script>
</body></html>`, k, k, a, viz.Diagram, historyHTML, k, a, mt, ws)
			return
		}
		if r.URL.Query().Get("include_history")=="true" {
			h,_ := lim.History(r.Context(), k, a, mt, ws, 5)
			writeJSON(w,200,map[string]any{"current":viz,"history":h})
			return
		}
		writeJSON(w,200,viz)
	})

	r.Get("/v1/visualize/stream", func(w http.ResponseWriter, r *http.Request) {
		k := r.URL.Query().Get("key")
		a := limiter.Algorithm(r.URL.Query().Get("algorithm")); if a==""{a=limiter.TokenBucket}
		mt,ws := uint32(100),uint32(60)
		w.Header().Set("Content-Type","text/event-stream")
		w.Header().Set("Cache-Control","no-cache")
		fl, _ := w.(http.Flusher)
		t := time.NewTicker(600 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-r.Context().Done(): return
			case <-t.C:
				if v,_ := lim.Visualize(r.Context(), k, a, mt, ws); v != nil {
					fmt.Fprintf(w, "data: %s\n\n", mustJSON(v))
					if fl != nil { fl.Flush() }
				}
			}
		}
	})

	r.Post("/v1/simulate", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key string `json:"key"`
			MaxTokens uint32 `json:"max_tokens"`
			WindowSeconds uint32 `json:"window_seconds"`
			Algorithm string `json:"algorithm"`
			Costs []uint32 `json:"costs"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if b.MaxTokens==0 {b.MaxTokens=50}
		if b.WindowSeconds==0 {b.WindowSeconds=30}
		if len(b.Costs)==0 {b.Costs = []uint32{1,1,1}}
		alg := limiter.Algorithm(b.Algorithm); if alg=="" {alg = limiter.TokenBucket}
		sim := limiter.NewInMemoryLimiter()
		res := []map[string]any{}
		for _,c:=range b.Costs {
			resp,_ := sim.Check(r.Context(), limiter.CheckRequest{Key: b.Key, MaxTokens: b.MaxTokens, WindowSeconds: b.WindowSeconds, Algorithm: alg, Cost: c})
			res = append(res, map[string]any{"cost":c, "allowed":resp.Allowed, "remaining":resp.Remaining})
		}
		writeJSON(w, 200, map[string]any{"results":res})
	})

	// Phase 3 Policy API
	r.Get("/v1/policies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, policyEngine.ListPolicies())
	})
	r.Post("/v1/policies", func(w http.ResponseWriter, r *http.Request) {
		var p limiter.Policy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid policy"})
			return
		}
		policyEngine.AddPolicy(p)
		writeJSON(w, 200, map[string]string{"status": "added", "name": p.Name})
	})

	// Phase 5: Replication endpoints (unified with event log)
	r.Post("/v1/replicate", func(w http.ResponseWriter, r *http.Request) {
		var ev limiter.ReplicationEvent
		json.NewDecoder(r.Body).Decode(&ev)
		if ev.Node == "" {
			ev.Node = *nodeID
		}
		if replicator != nil {
			replicator.Emit(r.Context(), ev.Op, ev.Key, ev.Value, ev.Version)
		} else {
			lim.EmitReplicationEvent(r.Context(), ev)
		}
		writeJSON(w, 200, map[string]string{"status": "emitted", "key": ev.Key})
	})
	r.Get("/v1/replicated/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "key")
		val, ok := lim.GetReplicatedState(key)
		writeJSON(w, 200, map[string]any{"key": key, "value": val, "found": ok})
	})

	// Phase 4: Replay endpoint
	r.Post("/v1/replay", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			FromTs int64  `json:"from_ts"`
			ToTs   int64  `json:"to_ts"`
			Key    string `json:"key"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		events, err := lim.Replay(r.Context(), req.FromTs, req.ToTs)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"events": events, "count": len(events)})
	})

	// Phase 4 snapshot/restore (admin style)
	r.Post("/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		dest := r.URL.Query().Get("dest")
		if dest == "" { dest = "/tmp/rate-snapshot.json" }
		err := lim.Snapshot(r.Context(), dest)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "snapped", "dest": dest})
	})
	r.Post("/v1/restore", func(w http.ResponseWriter, r *http.Request) {
		src := r.URL.Query().Get("src")
		if src == "" { src = "/tmp/rate-snapshot.json" }
		err := lim.Restore(r.Context(), src)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "restored", "src": src})
	})

	r.Get("/v1/admin/inspect", func(w http.ResponseWriter, r *http.Request) {
		k := r.URL.Query().Get("key")
		st, _ := lim.Inspect(r.Context(), k)
		writeJSON(w, 200, st)
	})
	r.Post("/v1/admin/reset", func(w http.ResponseWriter, r *http.Request) {
		k := r.URL.Query().Get("key")
		lim.Reset(r.Context(), k)
		writeJSON(w, 200, map[string]string{"reset":k})
	})

	// Serve the React framework dashboard (preferred). Falls back to legacy HTML dashboard.
	slog.Info("registering dashboard route")
	r.Get("/dashboard", serveDashboard)

	// Serve React build assets + root statics (favicon etc)
	r.Get("/assets/*", func(w http.ResponseWriter, r *http.Request) {
		serveReactAsset(w, r)
	})
	r.Get("/favicon.svg", func(w http.ResponseWriter, r *http.Request) { serveReactAsset(w, r) })

	slog.Info("server", "port", *port)

	// Phase 7: gRPC support disabled temporarily (pb descriptor version skew).
	// gRPC disabled (see comment above). HTTP + React dashboard fully working.
	slog.Info("gRPC disabled temporarily (protobuf mismatch in generated pb); dashboard + all HTTP endpoints ready")

	http.ListenAndServe(fmt.Sprintf(":%d", *port), r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// Phase 6: Redis-based node registry for cluster awareness
func registerNode(rdb *redis.Client, nodeID string) {
	key := "rl:nodes"
	rdb.SAdd(context.Background(), key, nodeID)
	rdb.Expire(context.Background(), key, 30*time.Second) // heartbeat TTL
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			rdb.SAdd(context.Background(), key, nodeID)
			rdb.Expire(context.Background(), key, 30*time.Second)
		}
	}()
}

func getNodes(rdb *redis.Client) []string {
	key := "rl:nodes"
	nodes, _ := rdb.SMembers(context.Background(), key).Result()
	return nodes
}

// twoTierLimiter: local fast path + global Redis (Phase 6)
type twoTierLimiter struct {
	local  *limiter.InMemoryLimiter
	global *limiter.RedisLimiter
	rdb    *redis.Client
	nodeID string
}

func (t *twoTierLimiter) Check(ctx context.Context, req limiter.CheckRequest) (*limiter.CheckResponse, error) {
	// Fast local check (Phase 6)
	resp, err := t.local.Check(ctx, req)
	if err != nil {
		return nil, err
	}
	// Phase 6: try global with fallback for resilience (outage handling)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, gerr := t.global.Check(ctx, req); gerr != nil {
			// degrade: local continues, log warning
			slog.Warn("global check degraded, using local", "key", req.Key, "err", gerr)
		}
	}()
	return resp, nil
}

func (t *twoTierLimiter) Visualize(ctx context.Context, key string, algo limiter.Algorithm, maxTokens, windowSeconds uint32) (*limiter.Visualization, error) {
	// Prefer local for speed, fallback
	v, err := t.local.Visualize(ctx, key, algo, maxTokens, windowSeconds)
	if err != nil || v == nil {
		return t.global.Visualize(ctx, key, algo, maxTokens, windowSeconds)
	}
	return v, nil
}

// Implement other Limiter methods by delegating (simplified for Phase 6)
func (t *twoTierLimiter) Reset(ctx context.Context, key string) error {
	t.local.Reset(ctx, key)
	return t.global.Reset(ctx, key)
}

func (t *twoTierLimiter) Inspect(ctx context.Context, key string) (map[string]any, error) {
	return t.global.Inspect(ctx, key)
}

func (t *twoTierLimiter) History(ctx context.Context, key string, algo limiter.Algorithm, maxTokens, windowSeconds uint32, limit int) ([]*limiter.Visualization, error) {
	return t.global.History(ctx, key, algo, maxTokens, windowSeconds, limit)
}

func (t *twoTierLimiter) Snapshot(ctx context.Context, dest string) error { return t.global.Snapshot(ctx, dest) }
func (t *twoTierLimiter) Restore(ctx context.Context, src string) error { return t.global.Restore(ctx, src) }
func (t *twoTierLimiter) LogDecision(ctx context.Context, ev limiter.DecisionEvent) error { return t.global.LogDecision(ctx, ev) }
func (t *twoTierLimiter) Replay(ctx context.Context, fromTs, toTs int64) ([]limiter.DecisionEvent, error) {
	return t.global.Replay(ctx, fromTs, toTs)
}
func (t *twoTierLimiter) EmitReplicationEvent(ctx context.Context, ev limiter.ReplicationEvent) error {
	return t.global.EmitReplicationEvent(ctx, ev)
}
func (t *twoTierLimiter) ApplyReplicationEvent(ev limiter.ReplicationEvent) bool {
	return t.global.ApplyReplicationEvent(ev)
}
func (t *twoTierLimiter) GetReplicatedState(key string) (interface{}, bool) {
	return t.global.GetReplicatedState(key)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// serveDashboard prefers the built React SPA at frontend/dist. Otherwise serves the legacy single-file dashboard.
func serveDashboard(w http.ResponseWriter, r *http.Request) {
	distPath := "frontend/dist"
	if fi, err := os.Stat(distPath); err == nil && fi.IsDir() {
		indexPath := filepath.Join(distPath, "index.html")
		if b, err := os.ReadFile(indexPath); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
			return
		}
	}
	// fallback to legacy
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data, err := uiFS.ReadFile("ui/dashboard.html"); err == nil {
		w.Write(data)
		return
	}
	http.Error(w, "dashboard unavailable", 503)
}

// serveReactAsset serves JS/CSS and other assets from the React build dir.
func serveReactAsset(w http.ResponseWriter, r *http.Request) {
	distPath := "frontend/dist"
	assetPath := r.URL.Path // e.g. /assets/index-xxx.js
	clean := filepath.Clean(assetPath)
	full := filepath.Join(distPath, clean)
	// security: ensure inside dist
	if !isSubPath(distPath, full) {
		http.Error(w, "forbidden", 403)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, _ := f.Stat()
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

func isSubPath(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || len(rel) > 2 && rel[:3] == "../" {
		return false
	}
	return true
}

// optional helper to copy built assets into embed dir at build time (omitted for simplicity)
func _ensureDist() fs.FS { return nil }
