package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimitsPerWindow(t *testing.T) {
	app := fiber.New()
	app.Get("/", New(Config{Name: "test", Max: 2, Window: time.Minute}), func(c *fiber.Ctx) error { return c.SendString("ok") })

	status := func() int {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil), -1)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusOK, status())
	assert.Equal(t, http.StatusOK, status())
	assert.Equal(t, http.StatusTooManyRequests, status())
}

func TestCustomKeyAndTightestHeaders(t *testing.T) {
	app := fiber.New()
	// The outer limit is generous and per client; the inner one is strict and keyed by a header.
	outer := New(Config{Name: "outer", Max: 100, Window: time.Minute})
	inner := New(Config{Name: "inner", Max: 2, Window: time.Minute, Key: func(c *fiber.Ctx) string { return c.Get("X-User") }})
	app.Get("/", outer, inner, func(c *fiber.Ctx) error { return c.SendString("ok") })

	call := func(user string) *http.Response {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User", user)
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp
	}

	resp := call("alice")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "2", resp.Header.Get("X-RateLimit-Limit"), "the stricter inner limit is reported")
	assert.Equal(t, "1", resp.Header.Get("X-RateLimit-Remaining"))

	call("alice")
	resp = call("alice")
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("Retry-After"))
	assert.Equal(t, "0", resp.Header.Get("X-RateLimit-Remaining"))

	assert.Equal(t, http.StatusOK, call("bob").StatusCode, "another key has its own counter")
}
