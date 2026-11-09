package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counterValue returns the value of the series of name with exactly labels, or -1.
func counterValue(t *testing.T, reg prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	series:
		for _, m := range f.GetMetric() {
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			for _, l := range m.GetLabel() {
				if labels[l.GetName()] != l.GetValue() {
					continue series
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return -1
}

func TestHTTPLabelsByRoutePattern(t *testing.T) {
	reg := NewRegistry()
	app := fiber.New()
	app.Use(HTTP(reg))
	app.Get("/items/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/teapot", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusTeapot) })

	for _, path := range []string{"/items/1", "/items/2", "/teapot"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil), -1)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	assert.Equal(t, 2.0, counterValue(t, reg, "http_requests_total",
		map[string]string{"method": "GET", "route": "/items/:id", "status": "200"}), "ids do not create new series")
	assert.Equal(t, 1.0, counterValue(t, reg, "http_requests_total",
		map[string]string{"method": "GET", "route": "/teapot", "status": "418"}))
}

func TestRegistryHasRuntimeMetrics(t *testing.T) {
	families, err := NewRegistry().Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	assert.True(t, names["go_goroutines"])
}
