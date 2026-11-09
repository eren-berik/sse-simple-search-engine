// Package router wires handlers and middleware into a Fiber application.
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/internal/http/middleware"
	"github.com/eren-berik/sse-simple-search-engine/internal/http/response"
	"github.com/eren-berik/sse-simple-search-engine/internal/web"
)

type Deps struct {
	Contents *handler.ContentHandler
	Sync     *handler.SyncHandler
	Health   *handler.HealthHandler
	Log      *slog.Logger

	AdminAPIKey     string
	RateLimitMax    int
	RateLimitWindow time.Duration
	// Redis is optional; when set, rate limit counters are shared across instances.
	Redis *redis.Client
}

func New(d Deps) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "content-search",
		DisableStartupMessage: true,
		ErrorHandler:          response.ErrorHandler(d.Log),
		// fasthttp reuses request buffers; immutable strings are safe to keep beyond the handler.
		Immutable:    true,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	})

	app.Use(requestid.New())
	app.Use(middleware.RequestLogger(d.Log))
	app.Use(recover.New())

	app.Get("/healthz", d.Health.Live)
	app.Get("/readyz", d.Health.Ready)

	v1 := app.Group("/api/v1", middleware.RateLimit(d.RateLimitMax, d.RateLimitWindow, d.Redis, d.Log))
	v1.Get("/contents", d.Contents.List)
	v1.Get("/contents/:id", d.Contents.Get)
	v1.Get("/sync/runs", d.Sync.Status)
	v1.Post("/admin/sync", middleware.APIKey(d.AdminAPIKey), d.Sync.Trigger)

	app.Use("/", filesystem.New(filesystem.Config{
		Root:   http.FS(web.Static),
		Index:  "index.html",
		MaxAge: 300,
	}))

	return app
}
