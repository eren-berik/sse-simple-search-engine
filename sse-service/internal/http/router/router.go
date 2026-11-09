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

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/middleware"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/response"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/web"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/openapi"
)

type Deps struct {
	Contents  *handler.ContentHandler
	Sync      *handler.SyncHandler
	Providers *handler.ProviderHandler
	Health    *handler.HealthHandler
	Log       *slog.Logger

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
	v1.Get("/providers", d.Providers.List)
	v1.Get("/sync/runs", d.Sync.Status)

	admin := middleware.APIKey(d.AdminAPIKey)
	v1.Post("/providers", admin, d.Providers.Create)
	v1.Patch("/providers/:id", admin, d.Providers.Update)
	v1.Post("/providers/:id/sync", admin, d.Sync.TriggerProvider)
	v1.Post("/sync", admin, d.Sync.Trigger)

	app.Get("/openapi.yaml", func(c *fiber.Ctx) error {
		c.Type("yaml")
		return c.Send(openapi.Spec)
	})
	app.Get("/docs", func(c *fiber.Ctx) error { return c.Redirect("/docs.html") })
	app.Use("/", filesystem.New(filesystem.Config{
		Root:   http.FS(web.Static),
		Index:  "index.html",
		MaxAge: 300,
	}))

	return app
}
