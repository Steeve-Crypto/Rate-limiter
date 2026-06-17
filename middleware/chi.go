// Package middleware provides rate limiting middleware for popular Go routers (Phase 7).
package middleware

import (
	"net/http"

	"github.com/crypto/rate-limiter-service/client"
)

// ChiRateLimit is a middleware for chi router using the rate limiter client.
func ChiRateLimit(c *client.Client, keyFunc func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			if key == "" {
				key = r.RemoteAddr // fallback
			}
			resp, err := c.Check(r.Context(), client.CheckRequest{
				Key:           key,
				MaxTokens:     100,
				WindowSeconds: 60,
			})
			if err != nil || !resp.Allowed {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Example usage:
// r := chi.NewRouter()
// r.Use(middleware.ChiRateLimit(client, func(r *http.Request) string { return r.Header.Get("X-User-ID") }))
// Note: similar can be made for gin, echo by adapting the signature.