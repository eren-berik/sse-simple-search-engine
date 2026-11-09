// Package middleware contains the HTTP middleware of the API.
package middleware

import (
	"crypto/subtle"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/response"
)

// RequestLogger logs one structured line per request.
func RequestLogger(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		if err != nil {
			// Run the error handler now so the logged status is the one sent to the client.
			if herr := c.App().ErrorHandler(c, err); herr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		status := c.Response().StatusCode()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		log.LogAttrs(c.UserContext(), level, "request",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.IP()),
			slog.Any("request_id", c.Locals("requestid")),
		)
		return nil
	}
}

// APIKey protects a route with a static key sent in the X-API-Key header.
// An empty key disables the route entirely.
func APIKey(key string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if key == "" {
			return response.Error(c, fiber.StatusForbidden, "forbidden", "admin endpoints are disabled (ADMIN_API_KEY is not set)")
		}
		if subtle.ConstantTimeCompare([]byte(c.Get("X-API-Key")), []byte(key)) != 1 {
			return response.Error(c, fiber.StatusUnauthorized, "unauthorized", "missing or invalid X-API-Key header")
		}
		return c.Next()
	}
}
