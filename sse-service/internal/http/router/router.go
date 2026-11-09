// Package router wires handlers and middleware into a Fiber application.
package router

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/jwtauth"
)

type Deps struct {
	Contents  *handler.ContentHandler
	Sync      *handler.SyncHandler
	Providers *handler.ProviderHandler
	Verifier  *jwtauth.Verifier
	Ready     fiber.Handler
	Log       *slog.Logger
}

// New builds the API. Every /api/v1 route requires a valid access token; viewers can read
// and filter, admins can additionally manage providers and trigger syncs. The service is
// API-only: the dashboard and docs are served by the gateway.
func New(d Deps) *fiber.App {
	app := httpx.NewApp(httpx.AppConfig{
		Name:              "sse-service",
		Log:               d.Log,
		ErrorMappers:      []httpx.ErrorMapper{handler.ErrorMapper},
		TrustProxyHeaders: true, // only reachable through the gateway
	})

	app.Get("/healthz", httpx.Live)
	app.Get("/readyz", d.Ready)

	v1 := app.Group("/api/v1", jwtauth.Authenticate(d.Verifier))
	v1.Get("/contents", d.Contents.List)
	v1.Get("/contents/:id", d.Contents.Get)
	v1.Get("/providers", d.Providers.List)
	v1.Get("/sync/runs", d.Sync.Status)

	admin := jwtauth.RequireRole(jwtauth.RoleAdmin)
	v1.Post("/providers", admin, d.Providers.Create)
	v1.Patch("/providers/:id", admin, d.Providers.Update)
	v1.Delete("/providers/:id", admin, d.Providers.Delete)
	v1.Post("/providers/:id/sync", admin, d.Sync.TriggerProvider)
	v1.Post("/sync", admin, d.Sync.Trigger)

	return app
}
