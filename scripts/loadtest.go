//go:build ignore
// +build ignore

// Simple concurrent load tester for the rate limiter service.
// Usage:
//   go run scripts/loadtest.go -url http://localhost:8080 -concurrency 100 -duration 10s -key "load:test"
//
// Reports QPS, error rate, and latency stats.
// Useful for validating perf budgets and chaos resilience.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type checkReq struct {
	Key           string `json:"key"`
	MaxTokens     int    `json:"max_tokens"`
	WindowSeconds int    `json:"window_seconds"`
	Algorithm     string `json:"algorithm"`
	Cost          int    `json:"cost"`
}

type checkResp struct {
	Allowed   bool   `json:"allowed"`
	Remaining int    `json:"remaining"`
	Limit     int    `json:"limit"`
	Algorithm string `json:"algorithm"`
}

func main() {
	url := flag.String("url", "http://localhost:8080", "base URL of rate limiter")
	concurrency := flag.Int("concurrency", 50, "number of concurrent workers")
	duration := flag.Duration("duration", 10*time.Second, "test duration")
	key := flag.String("key", "load:bench", "key to hammer")
	maxTokens := flag.Int("max", 1000, "max tokens")
	window := flag.Int("window", 60, "window seconds")
	algo := flag.String("algo", "token_bucket", "algorithm")
	flag.Parse()

	client := &http.Client{Timeout: 5 * time.Second}

	var wg sync.WaitGroup
	start := time.Now()
	deadline := start.Add(*duration)

	var (
		mu         sync.Mutex
		total      int
		allowed    int
		errors     int
		latencies  []time.Duration
	)

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				req := checkReq{
					Key:           *key,
					MaxTokens:     *maxTokens,
					WindowSeconds: *window,
					Algorithm:     *algo,
					Cost:          1,
				}
				body, _ := json.Marshal(req)

				t0 := time.Now()
				resp, err := client.Post(*url+"/v1/check", "application/json", bytes.NewReader(body))
				lat := time.Since(t0)

				mu.Lock()
				total++
				latencies = append(latencies, lat)
				if err != nil {
					errors++
				} else {
					defer resp.Body.Close()
					if resp.StatusCode != 200 && resp.StatusCode != 429 {
						errors++
					} else {
						var cr checkResp
						io.ReadAll(resp.Body) // drain
						// simplistic: if not 429 count as allowed in stats
						if resp.StatusCode == 200 {
							allowed++
						}
						_ = cr
					}
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()

	qps := float64(total) / elapsed.Seconds()
	errRate := float64(errors) / float64(total) * 100

	// simple latency stats
	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avg := sum / time.Duration(len(latencies))

	fmt.Printf("Load test complete\n")
	fmt.Printf("  Duration:      %v\n", elapsed)
	fmt.Printf("  Concurrency:   %d\n", *concurrency)
	fmt.Printf("  Total reqs:    %d\n", total)
	fmt.Printf("  QPS:           %.1f\n", qps)
	fmt.Printf("  Allowed:       %d (%.1f%%)\n", allowed, float64(allowed)/float64(total)*100)
	fmt.Printf("  Errors:        %d (%.2f%%)\n", errors, errRate)
	fmt.Printf("  Avg latency:   %v\n", avg)
	fmt.Printf("  (Use with -benchtime and Grafana for full budgets)\n")
}
