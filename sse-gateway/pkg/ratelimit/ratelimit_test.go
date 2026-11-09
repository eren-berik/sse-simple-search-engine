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
