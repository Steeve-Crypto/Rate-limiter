// Package middleware provides rate limiting middleware for gin (Phase 7).
package middleware

import (
	"net/http"

	"github.com/crypto/rate-limiter-service/client"
	"github.com/gin-gonic/gin"
)

// GinRateLimit returns a gin middleware.
func GinRateLimit(c *client.Client, keyFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		key := keyFunc(ctx)
		if key == "" {
			key = ctx.ClientIP()
		}
		resp, err := c.Check(ctx.Request.Context(), client.CheckRequest{
			Key:           key,
			MaxTokens:     100,
			WindowSeconds: 60,
		})
		if err != nil || !resp.Allowed {
			ctx.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limited"})
			return
		}
		ctx.Next()
	}
}