package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/config"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/http/middleware"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/http/router"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
)

// application holds the wired components and the resources that must be released on shutdown.
type application struct {
	cfg   config.Config
	log   *slog.Logger
	redis *redis.Client // nil when REDIS_URL is not set
	http  *fiber.App
}

func boot(ctx context.Context, cfg config.Config, log *slog.Logger) (*application, error) {
	app := &application{cfg: cfg, log: log}

	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("parse redis url: %w", err)
		}
		app.redis = redis.NewClient(opts)
		if err := app.redis.Ping(ctx).Err(); err != nil {
			log.Warn("redis unreachable at startup; rate limits fail open until it recovers", "error", err)
		}
	} else {
		log.Info("REDIS_URL not set; rate limit counters are per instance")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	required := map[string]httpx.Check{
		"iam-service": upstreamCheck(client, cfg.IAMURL),
		"sse-service": upstreamCheck(client, cfg.SSEURL),
	}
	// Already fetched signing keys keep verifying tokens while the key set is unreachable.
	keys := jwtauth.NewRemoteKeySource(cfg.JWKSURL, 5*time.Second)
	resilience := middleware.ResilienceConfig{
		FailureThreshold: cfg.BreakerFailureThreshold,
		OpenTimeout:      cfg.BreakerOpenTimeout,
		HalfOpenRequests: cfg.BreakerHalfOpenRequests,
		MaxRetries:       cfg.UpstreamMaxRetries,
		BaseBackoff:      cfg.UpstreamBaseBackoff,
		MaxBackoff:       cfg.UpstreamMaxBackoff,
	}
	iamGuard := middleware.NewResilience("iam-service", resilience, log)
	sseGuard := middleware.NewResilience("sse-service", resilience, log)

	// An open circuit is reported but does not fail readiness: the liveness checks above decide.
	optional := map[string]httpx.Check{
		"jwks":                keys.Ping,
		"circuit:iam-service": iamGuard.Check,
		"circuit:sse-service": sseGuard.Check,
	}
	if app.redis != nil {
		optional["redis"] = func(ctx context.Context) error { return app.redis.Ping(ctx).Err() }
	}

	app.http = router.New(router.Deps{
		IAMURL:          cfg.IAMURL,
		SSEURL:          cfg.SSEURL,
		UpstreamTimeout: cfg.UpstreamTimeout,
		IAMGuard:        iamGuard.Guard,
		SSEGuard:        sseGuard.Guard,
		Ready:           httpx.Ready(required, optional),
		Log:             log,
		Verifier:        jwtauth.NewVerifier(keys, cfg.JWTIssuer, cfg.JWTAudience),
		RateLimits: middleware.RateLimits{
			IP:    middleware.Limit{Max: cfg.IPRateLimitMax, Window: cfg.IPRateLimitWindow},
			Auth:  middleware.Limit{Max: cfg.AuthRateLimitMax, Window: cfg.AuthRateLimitWindow},
			User:  middleware.Limit{Max: cfg.UserRateLimitMax, Window: cfg.UserRateLimitWindow},
			Sync:  middleware.Limit{Max: cfg.SyncRateLimitMax, Window: cfg.SyncRateLimitWindow},
			Redis: app.redis,
			Log:   log,
		},
	})
	return app, nil
}

func (a *application) close() {
	if a.redis != nil {
		if err := a.redis.Close(); err != nil {
			a.log.Warn("closing redis", "error", err)
		}
	}
}

// upstreamCheck reports whether a backing service answers its liveness probe.
func upstreamCheck(client *http.Client, baseURL string) httpx.Check {
	url := strings.TrimRight(baseURL, "/") + "/healthz"
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		return nil
	}
}
