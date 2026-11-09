package jwtauth_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/jwtauth/jwtauthtest"
)

func jwksServer(t *testing.T, iss *jwtauthtest.TokenIssuer) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(iss.JWKS())
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestRemoteKeySourceVerifiesAndCaches(t *testing.T) {
	iss := jwtauthtest.New(t)
	srv, hits := jwksServer(t, iss)
	v := jwtauth.NewVerifier(jwtauth.NewRemoteKeySource(srv.URL, time.Second), jwtauthtest.Issuer, jwtauthtest.Audience)

	for range 3 {
		claims, err := v.Verify(context.Background(), iss.Token(t, jwtauth.RoleViewer))
		require.NoError(t, err)
		assert.Equal(t, jwtauth.RoleViewer, claims.Role)
	}
	assert.Equal(t, int32(1), hits.Load(), "keys are fetched once and cached")
}

func TestRemoteKeySourceRateLimitsUnknownKids(t *testing.T) {
	iss := jwtauthtest.New(t)
	srv, hits := jwksServer(t, iss)
	src := jwtauth.NewRemoteKeySource(srv.URL, time.Second)
	ctx := context.Background()

	_, err := src.Key(ctx, "unknown-1")
	assert.ErrorIs(t, err, jwtauth.ErrUnknownKey)
	_, err = src.Key(ctx, "unknown-2")
	assert.ErrorIs(t, err, jwtauth.ErrUnknownKey)

	assert.Equal(t, int32(1), hits.Load(), "a second unknown kid within the refresh interval does not refetch")
}

func TestRemoteKeySourceUnavailable(t *testing.T) {
	iss := jwtauthtest.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	v := jwtauth.NewVerifier(jwtauth.NewRemoteKeySource(srv.URL, time.Second), jwtauthtest.Issuer, jwtauthtest.Audience)

	_, err := v.Verify(context.Background(), iss.Token(t, jwtauth.RoleAdmin))

	assert.ErrorIs(t, err, jwtauth.ErrKeysUnavailable)
	assert.NotErrorIs(t, err, jwtauth.ErrInvalidToken, "an outage is not reported as a bad token")
}

func TestVerifierRejectsExpired(t *testing.T) {
	iss := jwtauthtest.New(t)

	_, err := iss.Verifier().Verify(context.Background(), iss.TokenWithExpiry(t, jwtauth.RoleAdmin, time.Now().Add(-time.Hour)))

	assert.ErrorIs(t, err, jwtauth.ErrInvalidToken)
}

func TestMiddleware(t *testing.T) {
	iss := jwtauthtest.New(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(down.Close)

	app := httpx.NewApp(httpx.AppConfig{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Get("/viewer", jwtauth.Authenticate(iss.Verifier()), func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/admin", jwtauth.Authenticate(iss.Verifier()), jwtauth.RequireRole(jwtauth.RoleAdmin),
		func(c *fiber.Ctx) error { return c.SendString("ok") })
	outage := jwtauth.NewVerifier(jwtauth.NewRemoteKeySource(down.URL, time.Second), jwtauthtest.Issuer, jwtauthtest.Audience)
	app.Get("/outage", jwtauth.Authenticate(outage), func(c *fiber.Ctx) error { return c.SendString("ok") })

	call := func(path, token string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	viewer, admin := iss.Token(t, jwtauth.RoleViewer), iss.Token(t, jwtauth.RoleAdmin)

	assert.Equal(t, http.StatusUnauthorized, call("/viewer", ""))
	assert.Equal(t, http.StatusOK, call("/viewer", viewer))
	assert.Equal(t, http.StatusForbidden, call("/admin", viewer))
	assert.Equal(t, http.StatusOK, call("/admin", admin))
	assert.Equal(t, http.StatusServiceUnavailable, call("/outage", admin))
}
