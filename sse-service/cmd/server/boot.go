package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/config"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/router"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/ingest"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/registry"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/repository/postgres"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/providers"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/search"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/telemetry"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/cache"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/metrics"
)

// application holds the wired components and the resources that must be released on shutdown.
type application struct {
	cfg     config.Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	redis   *redis.Client // nil when REDIS_URL is not set
	http    *fiber.App
	ingest  *ingest.Service
	metrics *metrics.Server
}

// boot connects to infrastructure and wires every component together.
func boot(ctx context.Context, cfg config.Config, log *slog.Logger) (*application, error) {
	app := &application{cfg: cfg, log: log}

	pool, err := connectDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	app.pool = pool

	searchCache, rdb, err := connectCache(ctx, cfg.RedisURL, log)
	if err != nil {
		app.close()
		return nil, err
	}
	app.redis = rdb

	contentRepo := postgres.NewContentRepository(pool)
	providerRepo := postgres.NewProviderRepository(pool)
	scorer := newScorer(cfg)
	searchSvc := search.NewService(contentRepo, searchCache, cfg.CacheTTL, scorer, log)
	providerRegistry := newProviderRegistry(cfg, log)

	reg := metrics.NewRegistry()
	app.metrics = metrics.NewServer(cfg.MetricsAddr, reg, log)
	domainMetrics := telemetry.New(reg)
	telemetry.RegisterState(reg, telemetry.StateSource{
		Providers: providerRepo.List,
		Contents:  contentRepo.CountContents,
		Breakers:  providerRegistry.BreakerStates,
		Log:       log,
	})

	app.ingest = ingest.NewService(providerRepo, providerRegistry, domainMetrics.InstrumentSyncStore(contentRepo), scorer, searchSvc, log)

	keys := jwtauth.NewRemoteKeySource(cfg.JWKSURL, 5*time.Second)
	app.http = router.New(router.Deps{
		Contents:  handler.NewContentHandler(domainMetrics.InstrumentSearcher(searchSvc)),
		Sync:      handler.NewSyncHandler(app.ingest),
		Providers: handler.NewProviderHandler(providers.NewService(providerRepo, providerRegistry, searchSvc, log)),
		Verifier:  newVerifier(keys, cfg, rdb, log),
		// The cache and the identity service are optional for readiness: searches fall back to the
		// database and already fetched signing keys keep verifying tokens.
		Ready: httpx.Ready(
			map[string]httpx.Check{"database": pool.Ping},
			map[string]httpx.Check{"cache": searchCache.Ping, "jwks": keys.Ping},
		),
		Log:        log,
		Instrument: metrics.HTTP(reg),
	})
	return app, nil
}

// close releases infrastructure connections.
func (a *application) close() {
	if a.redis != nil {
		if err := a.redis.Close(); err != nil {
			a.log.Warn("closing redis", "error", err)
		}
	}
	if a.pool != nil {
		a.pool.Close()
	}
}

// connectDatabase opens the pool, waiting up to 30s for the database, and applies migrations.
func connectDatabase(ctx context.Context, url string) (*pgxpool.Pool, error) {
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := postgres.Connect(connectCtx, url)
	if err != nil {
		return nil, err
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// connectCache returns a Redis-backed cache when url is set, otherwise an in-process one.
// An unreachable Redis at startup is not fatal: searches bypass the cache until it recovers.
func connectCache(ctx context.Context, url string, log *slog.Logger) (cache.Cache, *redis.Client, error) {
	if url == "" {
		log.Info("REDIS_URL not set; using in-process cache")
		return cache.NewMemory(), nil, nil
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Warn("redis unreachable at startup; continuing, searches will bypass the cache until it recovers", "error", err)
	}
	return cache.NewRedis(rdb), rdb, nil
}

func newScorer(cfg config.Config) *scoring.Scorer {
	scoreCfg := scoring.DefaultConfig()
	scoreCfg.VideoCoefficient = cfg.VideoCoefficient
	scoreCfg.TextCoefficient = cfg.TextCoefficient
	return scoring.New(scoreCfg, time.Now)
}

// newProviderRegistry builds providers from their stored configuration. Each provider gets its
// own HTTP client so rate limits and circuit breakers apply per provider.
func newProviderRegistry(cfg config.Config, log *slog.Logger) *registry.Registry {
	return registry.New(httpclient.Config{
		Timeout:       cfg.ProviderTimeout,
		RatePerSecond: cfg.ProviderRatePerSec,
		Burst:         cfg.ProviderBurst,
		MaxRetries:    cfg.ProviderMaxRetries,

		BreakerFailureThreshold: cfg.ProviderBreakerFailureThreshold,
		BreakerOpenTimeout:      cfg.ProviderBreakerOpenTimeout,
	}, cfg.ProviderMaxPages, log)
}

// newVerifier checks access tokens against the identity service's keys and, when Redis is
// configured, against the sessions it revoked (logout, stolen refresh token).
func newVerifier(keys jwtauth.KeySource, cfg config.Config, rdb *redis.Client, log *slog.Logger) *jwtauth.Verifier {
	v := jwtauth.NewVerifier(keys, cfg.JWTIssuer, cfg.JWTAudience)
	if rdb != nil {
		v.WithRevocations(jwtauth.NewSessionRevocations(rdb, log))
	}
	return v
}
