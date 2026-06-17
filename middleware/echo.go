// Package middleware provides rate limiting middleware for echo (Phase 7).
package middleware

import (
	"net/http"

	"github.com/crypto/rate-limiter-service/client"
	"github.com/labstack/echo/v4"
)

// EchoRateLimit returns an echo middleware.
func EchoRateLimit(c *client.Client, keyFunc func(echo.Context) string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			key := keyFunc(ctx)
			if key == "" {
				key = ctx.RealIP()
			}
			resp, err := c.Check(ctx.Request().Context(), client.CheckRequest{
				Key:           key,
				MaxTokens:     100,
				WindowSeconds: 60,
			})
			if err != nil || !resp.Allowed {
				return ctx.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
			}
			return next(ctx)
		}
	}
}