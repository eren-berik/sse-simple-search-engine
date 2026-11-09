package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsCountRetriesAndCircuit(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	url, _ := flakyUpstream(t, 503)
	cfg := testConfig()
	cfg.FailureThreshold = 3
	r := NewResilience("svc", cfg, discard, m)
	app := guardedApp(r, url, time.Second)

	do(t, app, http.MethodGet) // 3 attempts: 2 retries, then the circuit opens
	do(t, app, http.MethodGet) // fails fast

	assert.Equal(t, 2.0, testutil.ToFloat64(m.retries.WithLabelValues("svc")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.circuitRejected.WithLabelValues("svc")))
	state, err := reg.Gather()
	require.NoError(t, err)
	var found bool
	for _, f := range state {
		if f.GetName() == "gateway_circuit_state" {
			found = true
			assert.Equal(t, 2.0, f.GetMetric()[0].GetGauge().GetValue(), "2 means open")
		}
	}
	assert.True(t, found)
}

func TestMetricsCountRateLimitsByTier(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry())
	limits := RateLimits{Metrics: m, Log: discard}
	app := fiber.New()
	app.Get("/", limits.PerIP("auth", Limit{Max: 1, Window: time.Minute}), func(c *fiber.Ctx) error { return c.SendString("ok") })

	for range 3 {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil), -1)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	assert.Equal(t, 2.0, testutil.ToFloat64(m.rateLimited.WithLabelValues("auth")))
}
