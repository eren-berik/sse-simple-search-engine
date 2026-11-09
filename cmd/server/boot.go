package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/internal/config"
	"github.com/eren-berik/sse-simple-search-engine/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/internal/http/router"
	"github.com/eren-berik/sse-simple-search-engine/internal/ingest"
	"github.com/eren-berik/sse-simple-search-engine/internal/provider"
	"github.com/eren-berik/sse-simple-search-engine/internal/provider/jsonprovider"
	"github.com/eren-berik/sse-simple-search-engine/internal/provider/xmlprovider"
	"github.com/eren-berik/sse-simple-search-engine/internal/repository/postgres"
	"github.com/eren-berik/sse-simple-search-engine/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/internal/service/search"
	"github.com/eren-berik/sse-simple-search-engine/pkg/cache"
	"github.com/eren-berik/sse-simple-search-engine/pkg/httpclient"
)

// application holds the wired components and the resources that must be released on shutdown.
type application struct {
	cfg    config.Config
	log    *slog.Logger
	pool   *pgxpool.Pool
	redis  *redis.Client // nil when REDIS_URL is not set
	http   *fiber.App
	ingest *ingest.Service
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

	repo := postgres.NewContentRepository(pool)
	scorer := newScorer(cfg)
	searchSvc := search.NewService(repo, searchCache, cfg.CacheTTL, scorer, log)
	app.ingest = ingest.NewService(buildProviders(cfg, log), repo, scorer, searchSvc, log)

	app.http = router.New(router.Deps{
		Contents:        handler.NewContentHandler(searchSvc),
		Sync:            handler.NewSyncHandler(app.ingest),
		Health:          handler.NewHealthHandler(pool, searchCache),
		Log:             log,
		AdminAPIKey:     cfg.AdminAPIKey,
		RateLimitMax:    cfg.RateLimitMax,
		RateLimitWindow: cfg.RateLimitWindow,
		Redis:           rdb,
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

// buildProviders lists the content providers. A new provider only needs an implementation
// of provider.Provider and an entry here.
func buildProviders(cfg config.Config, log *slog.Logger) []provider.Provider {
	newClient := func() *httpclient.Client {
		// Each provider gets its own client so rate limits are enforced per provider.
		return httpclient.New(httpclient.Config{
			Timeout:       cfg.ProviderTimeout,
			RatePerSecond: cfg.ProviderRatePerSec,
			Burst:         cfg.ProviderBurst,
			MaxRetries:    cfg.ProviderMaxRetries,
		})
	}
	return []provider.Provider{
		jsonprovider.New("provider1", cfg.Provider1URL, newClient(), log),
		xmlprovider.New("provider2", cfg.Provider2URL, newClient(), log),
	}
}
