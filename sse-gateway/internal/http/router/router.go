// Package router wires the gateway's routes.
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/redis/go-redis/v9"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/http/middleware"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/proxy"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/web"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/openapi"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/ratelimit"
)

type Deps struct {
	IAMURL          string
	SSEURL          string
	UpstreamTimeout time.Duration
	Ready           fiber.Handler
	Log             *slog.Logger
	// Verifier checks access tokens at the edge; services verify them again.
	Verifier *jwtauth.Verifier

	RateLimitMax        int
	RateLimitWindow     time.Duration
	AuthRateLimitMax    int
	AuthRateLimitWindow time.Duration
	// Redis is optional; when set, rate limit counters are shared across gateway instances.
	Redis *redis.Client
}

// New builds the gateway. It owns no business logic: API calls are forwarded to the service
// that owns them; the gateway only adds edge concerns (authentication, coarse role checks,
// rate limits, request ids) and serves the dashboard and API docs.
func New(d Deps) *fiber.App {
	app := httpx.NewApp(httpx.AppConfig{Name: "sse-gateway", Log: d.Log})

	app.Get("/healthz", httpx.Live)
	app.Get("/readyz", d.Ready)

	api := app.Group("/api", ratelimit.New(ratelimit.Config{
		Name: "api", Max: d.RateLimitMax, Window: d.RateLimitWindow, Redis: d.Redis, Log: d.Log,
	}))

	// Credential endpoints get a stricter limit against brute force.
	authLimit := ratelimit.New(ratelimit.Config{
		Name: "auth", Max: d.AuthRateLimitMax, Window: d.AuthRateLimitWindow, Redis: d.Redis, Log: d.Log,
	})
	// Public: these endpoints are how a client gets a token in the first place.
	for _, path := range []string{"/v1/auth/login", "/v1/auth/register", "/v1/auth/refresh"} {
		api.Post(path, authLimit, proxy.Forward(d.IAMURL, d.UpstreamTimeout))
	}

	authenticate := middleware.Authenticate(d.Verifier)
	adminWrites := middleware.WriteRequiresRole(jwtauth.RoleAdmin)
	api.Get("/v1/auth/me", authenticate, proxy.Forward(d.IAMURL, d.UpstreamTimeout))
	proxy.Mount(api, []proxy.Route{{Prefix: "/v1/auth", Upstream: d.IAMURL}}, d.UpstreamTimeout)
	proxy.Mount(api, []proxy.Route{{Prefix: "/v1/contents", Upstream: d.SSEURL}}, d.UpstreamTimeout, authenticate)
	// Every user may read providers and sync runs; changing them is for admins.
	proxy.Mount(api, []proxy.Route{
		{Prefix: "/v1/providers", Upstream: d.SSEURL},
		{Prefix: "/v1/sync", Upstream: d.SSEURL},
	}, d.UpstreamTimeout, authenticate, adminWrites)

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
