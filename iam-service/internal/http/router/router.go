// Package router wires the identity service's HTTP routes.
package router

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

type Deps struct {
	Auth     *handler.AuthHandler
	Signer   *jwtauth.Signer
	Verifier *jwtauth.Verifier
	Ready    fiber.Handler
	Log      *slog.Logger
}

func New(d Deps) *fiber.App {
	app := httpx.NewApp(httpx.AppConfig{
		Name:              "iam-service",
		Log:               d.Log,
		ErrorMappers:      []httpx.ErrorMapper{handler.ErrorMapper},
		TrustProxyHeaders: true, // only reachable through the gateway
	})

	app.Get("/healthz", httpx.Live)
	app.Get("/readyz", d.Ready)

	// Public keys for other services to verify access tokens.
	jwks := d.Signer.JWKS()
	app.Get("/.well-known/jwks.json", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "public, max-age=300")
		return c.JSON(jwks)
	})

	v1 := app.Group("/api/v1/auth")
	v1.Post("/register", d.Auth.Register)
	v1.Post("/login", d.Auth.Login)
	v1.Post("/refresh", d.Auth.Refresh)
	v1.Post("/logout", d.Auth.Logout)
	v1.Get("/me", jwtauth.Authenticate(d.Verifier), d.Auth.Me)

	return app
}
