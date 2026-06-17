package limiter

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ChecksTotal counts total rate limit check requests.
	ChecksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_checks_total",
			Help: "Total number of rate limit check requests",
		},
		[]string{"algorithm", "allowed"},
	)

	// CheckDuration measures latency of check operations.
	CheckDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rate_limit_check_duration_seconds",
			Help:    "Duration of rate limit check operations",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10), // sub-ms to ~100ms
		},
		[]string{"algorithm", "backend"},
	)

	// VisualizeTotal counts visualize calls.
	VisualizeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_visualize_total",
			Help: "Total number of visualize requests",
		},
		[]string{"algorithm"},
	)

	// VisualizeDuration measures latency of visualize operations.
	VisualizeDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rate_limit_visualize_duration_seconds",
			Help:    "Duration of visualize operations",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 8),
		},
		[]string{"algorithm", "backend"},
	)

	// ResetsTotal counts admin reset operations.
	ResetsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_resets_total",
			Help: "Total number of rate limit reset operations",
		},
		[]string{"backend"},
	)

	// CurrentTokens is a gauge for current token bucket levels (best effort, high cardinality warning).
	// Use with care; prefer per-prefix or sampled.
	CurrentTokens = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rate_limit_current_tokens",
			Help: "Current approximate tokens in bucket (in-memory only)",
		},
		[]string{"key"},
	)
)

// BackendName returns a string label for the limiter implementation.
func BackendName(l Limiter) string {
	switch l.(type) {
	case *RedisLimiter:
		return "redis"
	default:
		return "inmemory"
	}
}