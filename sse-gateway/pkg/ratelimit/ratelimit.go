// Package ratelimit limits requests per client IP, optionally sharing counters through Redis.
package ratelimit

import (
	"context"
	"errors"
	"log/slog"
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
	// Redis is optional; when set, counters are shared across gateway instances.
	Redis *redis.Client
	Log   *slog.Logger
}

// New limits requests per client IP to Max per Window.
func New(rc Config) fiber.Handler {
	cfg := limiter.Config{
		Max:        rc.Max,
		Expiration: rc.Window,
		LimitReached: func(c *fiber.Ctx) error {
			return httpx.Error(c, fiber.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
		},
	}
	if rc.Redis != nil {
		cfg.Storage = &redisStorage{rdb: rc.Redis, prefix: "ratelimit:" + rc.Name + ":", log: rc.Log}
	}
	return limiter.New(cfg)
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
