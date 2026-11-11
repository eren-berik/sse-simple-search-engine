// Package router wires the gateway's routes.
package router

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/http/middleware"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/proxy"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/web"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/openapi"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
)

type Deps struct {
	IAMURL          string
	SSEURL          string
	UpstreamTimeout time.Duration
	// Instrument records request metrics; optional.
	Instrument fiber.Handler
	// IAMGuard and SSEGuard protect the calls to each service (circuit breaker, retries); optional.
	IAMGuard proxy.Guard
	SSEGuard proxy.Guard
	Ready    fiber.Handler
	Log      *slog.Logger
	// Verifier checks access tokens at the edge; services verify them again.
	Verifier *jwtauth.Verifier

	RateLimits middleware.RateLimits
}

// New builds the gateway. It owns no business logic: API calls are forwarded to the service
// that owns them; the gateway only adds edge concerns (authentication, coarse role checks,
// rate limits, request ids) and serves the dashboard and API docs.
func New(d Deps) *fiber.App {
	app := httpx.NewApp(httpx.AppConfig{
		Name: "sse-gateway", Log: d.Log, ErrorMappers: []httpx.ErrorMapper{middleware.ErrorMapper},
		Instrument: d.Instrument,
	})
	iam := proxy.Target{URL: d.IAMURL, Timeout: d.UpstreamTimeout, Guard: d.IAMGuard}
	sse := proxy.Target{URL: d.SSEURL, Timeout: d.UpstreamTimeout, Guard: d.SSEGuard}

	app.Get("/healthz", httpx.Live)
	app.Get("/readyz", d.Ready)

	limits := d.RateLimits
	api := app.Group("/api", limits.PerIP("ip", limits.IP))

	// Public: these endpoints are how a client gets a token in the first place, so they can
	// only be limited per IP; the stricter limit slows down password guessing.
	authLimit := limits.PerIP("auth", limits.Auth)
	for _, path := range []string{"/v1/auth/login", "/v1/auth/register", "/v1/auth/refresh"} {
		api.Post(path, authLimit, proxy.Forward(iam))
	}

	// Everything below is per user: authenticate first, so the limit is keyed by a verified subject.
	authenticate := middleware.Authenticate(d.Verifier)
	userLimit := limits.PerUser("user", limits.User)
	user := []fiber.Handler{authenticate, userLimit}
	adminWrites := middleware.WriteRequiresRole(jwtauth.RoleAdmin)

	api.Get("/v1/auth/me", append(user, proxy.Forward(iam))...)
	proxy.Mount(api, []proxy.Route{{Prefix: "/v1/auth", Target: iam}})

	// A sync fetches every provider, so triggers get their own, much smaller budget.
	syncLimit := limits.PerUser("sync", limits.Sync)
	for _, path := range []string{"/v1/sync", "/v1/providers/:id/sync"} {
		api.Post(path, append(user, adminWrites, syncLimit, proxy.Forward(sse))...)
	}

	proxy.Mount(api, []proxy.Route{
		{Prefix: "/v1/contents", Target: sse},
		{Prefix: "/v1/tags", Target: sse},
	}, user...)
	// Every user may read providers and sync runs; changing them is for admins.
	proxy.Mount(api, []proxy.Route{
		{Prefix: "/v1/providers", Target: sse},
		{Prefix: "/v1/sync", Target: sse},
	}, append(user, adminWrites)...)

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
