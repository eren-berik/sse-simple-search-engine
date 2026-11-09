package middleware_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/http/middleware"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth/jwtauthtest"
)

func newTestApp() *fiber.App {
	return httpx.NewApp(httpx.AppConfig{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func ok(c *fiber.Ctx) error { return c.SendString("ok") }

func call(t *testing.T, app *fiber.App, method, path, token string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

func TestAuthenticateAndRoles(t *testing.T) {
	iss := jwtauthtest.New(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(down.Close)

	app := newTestApp()
	app.Get("/viewer", middleware.Authenticate(iss.Verifier()), ok)
	app.Get("/admin", middleware.Authenticate(iss.Verifier()), middleware.RequireRole(jwtauth.RoleAdmin), ok)
	app.All("/writes", middleware.Authenticate(iss.Verifier()), middleware.WriteRequiresRole(jwtauth.RoleAdmin), ok)
	outage := jwtauth.NewVerifier(jwtauth.NewRemoteKeySource(down.URL, time.Second), jwtauthtest.Issuer, jwtauthtest.Audience)
	app.Get("/outage", middleware.Authenticate(outage), ok)

	viewer, admin := iss.Token(t, jwtauth.RoleViewer), iss.Token(t, jwtauth.RoleAdmin)
	expired := iss.TokenWithExpiry(t, jwtauth.RoleAdmin, time.Now().Add(-time.Hour))

	tests := []struct {
		method, path, token string
		want                int
	}{
		{http.MethodGet, "/viewer", "", http.StatusUnauthorized},
		{http.MethodGet, "/viewer", expired, http.StatusUnauthorized},
		{http.MethodGet, "/viewer", viewer, http.StatusOK},
		{http.MethodGet, "/admin", viewer, http.StatusForbidden},
		{http.MethodGet, "/admin", admin, http.StatusOK},
		{http.MethodGet, "/writes", viewer, http.StatusOK},
		{http.MethodHead, "/writes", viewer, http.StatusOK},
		{http.MethodPost, "/writes", viewer, http.StatusForbidden},
		{http.MethodDelete, "/writes", viewer, http.StatusForbidden},
		{http.MethodPost, "/writes", admin, http.StatusOK},
		{http.MethodGet, "/outage", admin, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, call(t, app, tt.method, tt.path, tt.token).StatusCode, "%s %s", tt.method, tt.path)
	}
	assert.Equal(t, "Bearer", call(t, app, http.MethodGet, "/viewer", "").Header.Get("WWW-Authenticate"))
}
