// Package ratelimit limits requests per key (the client IP unless configured otherwise),
// optionally sharing counters through Redis.
package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
)

// Config describes one limit. Name separates the counters of different limits.
type Config struct {
	Name   string
	Max    int
	Window time.Duration
	// Key identifies whose requests are counted together; it defaults to the client IP.
	Key func(c *fiber.Ctx) string
	// Redis is optional; when set, counters are shared across gateway instances.
	Redis *redis.Client
	Log   *slog.Logger
}

// New limits requests per key to Max per Window. Rejected requests get 429 with Retry-After.
// Several limits may apply to one request; the X-RateLimit-* headers report the tightest.
func New(rc Config) fiber.Handler {
	cfg := limiter.Config{
		Max:          rc.Max,
		Expiration:   rc.Window,
		KeyGenerator: rc.Key,
		LimitReached: func(c *fiber.Ctx) error {
			// The limiter only sets Retry-After on rejection; report the exhausted limit too.
			c.Set(headerLimit, strconv.Itoa(rc.Max))
			c.Set(headerRemaining, "0")
			c.Set(headerReset, c.GetRespHeader(fiber.HeaderRetryAfter))
			return httpx.Error(c, fiber.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
		},
	}
	if rc.Redis != nil {
		cfg.Storage = &redisStorage{rdb: rc.Redis, prefix: "ratelimit:" + rc.Name + ":", log: rc.Log}
	}
	limit := limiter.New(cfg)
	return func(c *fiber.Ctx) error {
		err := limit(c)
		keepTightest(c)
		return err
	}
}

const (
	headerLimit     = "X-RateLimit-Limit"
	headerRemaining = "X-RateLimit-Remaining"
	headerReset     = "X-RateLimit-Reset"
	tightestKey     = "ratelimit.tightest"
)

type rateHeaders struct{ limit, remaining, reset string }

// keepTightest runs after a limiter has set its headers. Nested limiters finish inside out, so an
// outer limit would overwrite an inner, stricter one; restore whichever has fewer requests left.
func keepTightest(c *fiber.Ctx) {
	// Header values alias fasthttp buffers that the next Set overwrites; keep copies.
	cur := rateHeaders{
		strings.Clone(c.GetRespHeader(headerLimit)),
		strings.Clone(c.GetRespHeader(headerRemaining)),
		strings.Clone(c.GetRespHeader(headerReset)),
	}
	if cur.remaining == "" {
		return
	}
	prev, ok := c.Locals(tightestKey).(rateHeaders)
	if ok && atoi(prev.remaining) < atoi(cur.remaining) {
		c.Set(headerLimit, prev.limit)
		c.Set(headerRemaining, prev.remaining)
		c.Set(headerReset, prev.reset)
		return
	}
	c.Locals(tightestKey, cur)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// redisStorage adapts go-redis to fiber.Storage. It fails open: if Redis is unreachable,
// requests are let through rather than rejected, matching the cache's degradation policy.
type redisStorage struct {
	rdb    *redis.Client
	prefix string
	log    *slog.Logger
}

const storageTimeout = 500 * time.Millisecond

func (s *redisStorage) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	val, err := s.rdb.Get(ctx, s.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		s.log.Warn("rate limit storage get failed", "error", err)
		return nil, nil
	}
	return val, nil
}

func (s *redisStorage) Set(key string, val []byte, exp time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	if err := s.rdb.Set(ctx, s.prefix+key, val, exp).Err(); err != nil {
		s.log.Warn("rate limit storage set failed", "error", err)
	}
	return nil
}

func (s *redisStorage) Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	return s.rdb.Del(ctx, s.prefix+key).Err()
}

// Reset is a no-op: flushing would wipe unrelated keys in the shared database.
func (s *redisStorage) Reset() error { return nil }

// Close is a no-op: the client is owned by the application.
func (s *redisStorage) Close() error { return nil }
