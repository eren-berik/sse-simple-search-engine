package middleware

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/ratelimit"
)

// Limit allows Max requests per Window.
type Limit struct {
	Max    int
	Window time.Duration
}

// RateLimits are the gateway's tiers. Anonymous traffic can only be told apart by IP; once a
// token is verified, requests are counted per user, so clients behind one NAT do not share a
// quota and a user cannot reset theirs by refreshing the token (the subject stays the same).
type RateLimits struct {
	IP   Limit // every API request, before authentication: a coarse flood guard
	Auth Limit // login, register and refresh per IP, against brute force
	User Limit // authenticated requests per user
	Sync Limit // sync triggers per user; each one fetches every provider

	// Redis is optional; when set, counters are shared across gateway instances.
	Redis   *redis.Client
	Log     *slog.Logger
	Metrics *Metrics
}

// PerIP limits requests per client IP.
func (r RateLimits) PerIP(name string, l Limit) fiber.Handler {
	return ratelimit.New(ratelimit.Config{
		Name: name, Max: l.Max, Window: l.Window, Redis: r.Redis, Log: r.Log,
		OnLimit: func() { r.Metrics.rateLimit(name) },
	})
}

// PerUser limits requests per authenticated user. It must run after Authenticate: only a
// verified token's subject can be trusted as the key.
func (r RateLimits) PerUser(name string, l Limit) fiber.Handler {
	return ratelimit.New(ratelimit.Config{
		Name: name, Max: l.Max, Window: l.Window, Redis: r.Redis, Log: r.Log, Key: userKey,
		OnLimit: func() { r.Metrics.rateLimit(name) },
	})
}

func userKey(c *fiber.Ctx) string {
	if claims, ok := ClaimsFrom(c); ok {
		return "user:" + claims.Subject
	}
	// Unreachable when used after Authenticate; never share one bucket among anonymous clients.
	return "ip:" + c.IP()
}
