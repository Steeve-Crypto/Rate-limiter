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

type Config struct {
	RedisAddr string
	Port      int
}

func main() {
	// Structured logging with slog
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	var (
		port     = flag.Int("port", 8080, "HTTP port to listen on")
		redisURL = flag.String("redis", "", "Redis address e.g. localhost:6379 (empty = in-memory)")
	)
	flag.Parse()

	cfg := Config{
		RedisAddr: *redisURL,
		Port:      *port,
	}

	var lim limiter.Limiter

	if cfg.RedisAddr != "" {
		rdb := redis.NewClient(&redis.Options{
			Addr: cfg.RedisAddr,
		})
		// quick ping
		if pingErr := rdb.Ping(context.Background()).Err(); pingErr != nil {
			slog.Warn("Redis ping failed, falling back to in-memory", "error", pingErr)
		} else {
			lim = limiter.NewRedisLimiter(rdb)
			slog.Info("using Redis backend", "addr", cfg.RedisAddr)
		}
	}

	if lim == nil {
		lim = limiter.NewInMemoryLimiter()
		slog.Info("using in-memory backend")
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	// Structured request logging
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			slog.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "rate-limiter-service",
			"backend": backendName(lim),
		})
	})

	r.Handle("/metrics", promhttp.Handler())

	r.Get("/ready", readyHandler(lim))

	r.Post("/v1/check", func(w http.ResponseWriter, r *http.Request) {
		var req limiter.CheckRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Cost == 0 {
			req.Cost = 1
		}

		if req.MaxTokens == 0 {
			req.MaxTokens = 100
		}
		if req.WindowSeconds == 0 {
			req.WindowSeconds = 60
		}
		if req.MaxTokens > 1_000_000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_tokens too large"})
			return
		}

		resp, err := lim.Check(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		status := http.StatusOK
		if !resp.Allowed {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, resp)
	})

	// NEW: Visualize interface
	r.Get("/v1/visualize", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}

		algoStr := r.URL.Query().Get("algorithm")
		if algoStr == "" {
			algoStr = string(limiter.TokenBucket)
		}

		maxTokensStr := r.URL.Query().Get("max_tokens")
		windowStr := r.URL.Query().Get("window_seconds")

		maxTokens, _ := strconv.ParseUint(maxTokensStr, 10, 32)
		if maxTokens == 0 {
			maxTokens = 100
		}
		windowSeconds, _ := strconv.ParseUint(windowStr, 10, 32)
		if windowSeconds == 0 {
			windowSeconds = 60
		}

		algo := limiter.Algorithm(algoStr)

		viz, err := lim.Visualize(r.Context(), key, algo, uint32(maxTokens), uint32(windowSeconds))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// Support ?format=html for a simple browser view
		if r.URL.Query().Get("format") == "html" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Rate Limit Visualize - %s</title>
<style>
body { font-family: monospace; background:#111; color:#0f0; padding:20px; }
pre { background:#222; padding:16px; border:1px solid #0a0; }
</style>
</head>
<body>
<h2>%s [%s]</h2>
<pre>%s</pre>
<h3>Raw State</h3>
<pre>%s</pre>
</body>
</html>`, key, algo, key, viz.Diagram, prettyJSON(viz.State))
			return
		}

		writeJSON(w, http.StatusOK, viz)
	})

	// Also allow a simple CLI-friendly visualize via POST for scripts
	r.Post("/v1/visualize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key           string            `json:"key"`
			Algorithm     string            `json:"algorithm"`
			MaxTokens     uint32            `json:"max_tokens"`
			WindowSeconds uint32            `json:"window_seconds"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Key == "" {
			http.Error(w, "key required", 400)
			return
		}
		if body.MaxTokens == 0 {
			body.MaxTokens = 100
		}
		if body.WindowSeconds == 0 {
			body.WindowSeconds = 60
		}
		if body.Algorithm == "" {
			body.Algorithm = string(limiter.TokenBucket)
		}

		viz, err := lim.Visualize(r.Context(), body.Key, limiter.Algorithm(body.Algorithm), body.MaxTokens, body.WindowSeconds)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, 200, viz)
	})

	// Admin API (Phase 1)
	r.Route("/v1/admin", func(r chi.Router) {
		r.Post("/reset", adminResetHandler(lim))
		r.Get("/inspect", adminInspectHandler(lim))
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("rate-limiter-service listening", "addr", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func backendName(l limiter.Limiter) string {
	return limiter.BackendName(l)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func prettyJSON(m map[string]any) string {
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}

// Admin handlers

func adminResetHandler(lim limiter.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}

		if err := lim.Reset(r.Context(), key); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		limiter.ResetsTotal.WithLabelValues(limiter.BackendName(lim)).Inc()

		slog.Info("rate limit reset", "key", key)
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "reset",
			"key":    key,
		})
	}
}

func adminInspectHandler(lim limiter.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}

		state, err := lim.Inspect(r.Context(), key)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, state)
	}
}

func readyHandler(lim limiter.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := map[string]any{
			"status":  "ready",
			"backend": backendName(lim),
		}

		// For Redis, do a quick ping if possible via type assert or known
		if rl, ok := lim.(*limiter.RedisLimiter); ok && rl != nil {
			// simple check, in real we'd expose a Ping method
			status["redis"] = "connected (assumed if limiter created)"
		}

		writeJSON(w, http.StatusOK, status)
	}
}

// CLI support (simple, for `go run main.go check` and visualize)
func init() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "check", "visualize":
			runCLI()
			os.Exit(0)
		}
	}
}

func runCLI() {
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)

	key := fs.String("key", "", "rate limit key (required)")
	maxT := fs.Uint("max-tokens", 100, "max tokens / limit")
	win := fs.Uint("window", 60, "window in seconds")
	algo := fs.String("algo", "token_bucket", "token_bucket or sliding_window")
	cost := fs.Uint("cost", 1, "cost of this request")
	redisAddr := fs.String("redis", os.Getenv("REDIS_ADDR"), "redis addr (optional)")

	fs.Parse(os.Args[2:])

	if *key == "" {
		fmt.Println("Error: --key is required")
		fs.Usage()
		os.Exit(1)
	}

	var lim limiter.Limiter
	if *redisAddr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: *redisAddr})
		lim = limiter.NewRedisLimiter(rdb)
	} else {
		lim = limiter.NewInMemoryLimiter()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch cmd {
	case "check":
		req := limiter.CheckRequest{
			Key:           *key,
			MaxTokens:     uint32(*maxT),
			WindowSeconds: uint32(*win),
			Algorithm:     limiter.Algorithm(*algo),
			Cost:          uint32(*cost),
		}
		resp, err := lim.Check(ctx, req)
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(b))

	case "visualize":
		viz, err := lim.Visualize(ctx, *key, limiter.Algorithm(*algo), uint32(*maxT), uint32(*win))
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		fmt.Println("=== VISUALIZATION ===")
		fmt.Println(viz.Diagram)
		fmt.Println("\n=== RAW STATE ===")
		b, _ := json.MarshalIndent(viz.State, "", "  ")
		fmt.Println(string(b))
	}
}
