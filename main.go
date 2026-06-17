package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/crypto/rate-limiter-service/limiter"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	redis "github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	var (
		port     = flag.Int("port", 8080, "port")
		redisURL = flag.String("redis", "", "redis")
	)
	flag.Parse()

	var lim limiter.Limiter
	var nodeID = "node-" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)
	var replicator *limiter.Replicator
	if *redisURL != "" {
		rdb := redis.NewClient(&redis.Options{Addr: *redisURL})
		if rdb.Ping(context.Background()).Err() == nil {
			lim = limiter.NewRedisLimiter(rdb)
			replicator = limiter.NewReplicator(nodeID, rdb, "rl:replication")
			replicator.StartConsumer(context.Background())
		}
	}
	if lim == nil {
		lim = limiter.NewInMemoryLimiter()
		slog.Info("using inmemory")
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

	r.Get("/health", func(w http.ResponseWriter, r *http.Request){ writeJSON(w, 200, map[string]any{"ok":true, "backend": limiter.BackendName(lim)}) })
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request){ writeJSON(w, 200, map[string]any{"ready":true}) })
	r.Handle("/metrics", promhttp.Handler())

	r.Post("/v1/check", func(w http.ResponseWriter, r *http.Request) {
		var req limiter.CheckRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.MaxTokens == 0 { req.MaxTokens=100 }
		if req.WindowSeconds==0 { req.WindowSeconds=60 }
		if req.Cost==0 { req.Cost=1 }

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

		writeJSON(w, st, resp)
	})

	r.Get("/v1/visualize", func(w http.ResponseWriter, r *http.Request) {
		k := r.URL.Query().Get("key"); if k=="" { http.Error(w,"key",400); return }
		a := limiter.Algorithm(r.URL.Query().Get("algorithm")); if a=="" { a=limiter.TokenBucket }
		mt:=uint32(100); if v,_:=strconv.ParseUint(r.URL.Query().Get("max_tokens"),10,32);v>0{mt=uint32(v)}
		ws:=uint32(60); if v,_:=strconv.ParseUint(r.URL.Query().Get("window_seconds"),10,32);v>0{ws=uint32(v)}
		viz, _ := lim.Visualize(r.Context(), k, a, mt, ws)
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
			ev.Node = nodeID
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

	r.Get("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h2>Phase 2 Live Dashboard</h2>
<input id=k value=demo:1> <button onclick=load()>Load+Hist</button> <button onclick=live()>Live</button> <button onclick=simu()>Sim</button>
<pre id=o></pre><pre id=l style="height:10em"></pre>
<script>
function load(){fetch('/v1/visualize?key='+document.getElementById('k').value+'&include_history=true').then(r=>r.text()).then(t=>document.getElementById('o').textContent=t)}
function live(){let e=new EventSource('/v1/visualize/stream?key='+document.getElementById('k').value);e.onmessage=m=>document.getElementById('l').textContent=Date.now()+' '+m.data+'\n'+document.getElementById('l').textContent}
function simu(){fetch('/v1/simulate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({key:document.getElementById('k').value,costs:[1,2,1]})}).then(r=>r.text()).then(t=>document.getElementById('l').textContent='SIM '+t)}
setTimeout(load,100)
</script></body></html>`))
	})

	slog.Info("server", "port", *port)
	http.ListenAndServe(fmt.Sprintf(":%d", *port), r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
