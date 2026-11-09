package middleware

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

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/proxy"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// flakyUpstream answers with the next status in statuses (repeating the last one) and counts hits.
func flakyUpstream(t *testing.T, statuses ...int) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(hits.Add(1)) - 1
		w.WriteHeader(statuses[min(n, len(statuses)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func testConfig() ResilienceConfig {
	return ResilienceConfig{
		FailureThreshold: 3, OpenTimeout: time.Minute, HalfOpenRequests: 1,
		MaxRetries: 2, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
	}
}

// guardedApp proxies every request to upstream through r, without waiting between retries.
func guardedApp(r *Resilience, upstream string, timeout time.Duration) *fiber.App {
	r.sleep = func(context.Context, time.Duration) error { return nil }
	app := httpx.NewApp(httpx.AppConfig{Log: discard, ErrorMappers: []httpx.ErrorMapper{ErrorMapper}})
	app.All("/*", proxy.Forward(proxy.Target{URL: upstream, Timeout: timeout, Guard: r.Guard}))
	return app
}

type result struct {
	status     int
	code       string
	retryAfter string
}

func do(t *testing.T, app *fiber.App, method string) result {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, "/x", nil), -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return result{resp.StatusCode, body.Error.Code, resp.Header.Get("Retry-After")}
}

func TestRetriesIdempotentRequests(t *testing.T) {
	url, hits := flakyUpstream(t, 503, 502, 200)
	app := guardedApp(NewResilience("svc", testConfig(), discard), url, time.Second)

	assert.Equal(t, http.StatusOK, do(t, app, http.MethodGet).status)
	assert.Equal(t, int32(3), hits.Load(), "two retries, then success")
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	url, hits := flakyUpstream(t, 503)
	cfg := testConfig()
	cfg.FailureThreshold = 100
	app := guardedApp(NewResilience("svc", cfg, discard), url, time.Second)

	assert.Equal(t, http.StatusServiceUnavailable, do(t, app, http.MethodGet).status, "the upstream answer is passed through")
	assert.Equal(t, int32(3), hits.Load())
}

func TestDoesNotRetryWrites(t *testing.T) {
	url, hits := flakyUpstream(t, 503, 200)
	app := guardedApp(NewResilience("svc", testConfig(), discard), url, time.Second)

	assert.Equal(t, http.StatusServiceUnavailable, do(t, app, http.MethodPost).status)
	assert.Equal(t, int32(1), hits.Load(), "the first attempt may have had side effects")
}

func TestDoesNotRetryTimeouts(t *testing.T) {
	var hits atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(slow.Close)
	app := guardedApp(NewResilience("svc", testConfig(), discard), slow.URL, 50*time.Millisecond)

	res := do(t, app, http.MethodGet)
	assert.Equal(t, http.StatusGatewayTimeout, res.status)
	assert.Equal(t, "gateway_timeout", res.code)
	assert.Equal(t, int32(1), hits.Load(), "the time budget is already spent")
}

func TestRetriesConnectionErrors(t *testing.T) {
	cfg := testConfig()
	cfg.FailureThreshold = 100
	r := NewResilience("svc", cfg, discard)
	var calls int
	r.sleep = func(context.Context, time.Duration) error { calls++; return nil }
	app := httpx.NewApp(httpx.AppConfig{Log: discard, ErrorMappers: []httpx.ErrorMapper{ErrorMapper}})
	app.All("/*", proxy.Forward(proxy.Target{URL: "http://127.0.0.1:1", Timeout: time.Second, Guard: r.Guard}))

	res := do(t, app, http.MethodGet)
	assert.Equal(t, http.StatusBadGateway, res.status)
	assert.Equal(t, "bad_gateway", res.code)
	assert.Equal(t, 2, calls, "connection errors are retried")
}

func TestCircuitOpensAndRecovers(t *testing.T) {
	url, hits := flakyUpstream(t, 503, 503, 503, 200)
	cfg := testConfig()
	cfg.MaxRetries = 0
	cfg.OpenTimeout = 50 * time.Millisecond
	r := NewResilience("svc", cfg, discard)
	app := guardedApp(r, url, time.Second)

	for range 3 {
		assert.Equal(t, http.StatusServiceUnavailable, do(t, app, http.MethodGet).status)
	}
	require.Error(t, r.Check(context.Background()), "readiness reports the open circuit")

	res := do(t, app, http.MethodGet)
	assert.Equal(t, result{http.StatusServiceUnavailable, "upstream_unavailable", "1"}, res, "fails fast while open")
	assert.Equal(t, int32(3), hits.Load(), "an open circuit does not call the upstream")

	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, http.StatusOK, do(t, app, http.MethodGet).status, "a trial request closes the circuit")
	assert.NoError(t, r.Check(context.Background()))
}

func TestOpenCircuitStopsRetries(t *testing.T) {
	url, hits := flakyUpstream(t, 503)
	cfg := testConfig()
	cfg.FailureThreshold = 2
	cfg.MaxRetries = 5
	app := guardedApp(NewResilience("svc", cfg, discard), url, time.Second)

	res := do(t, app, http.MethodGet)
	assert.Equal(t, "upstream_unavailable", res.code)
	assert.Equal(t, int32(2), hits.Load(), "every attempt counts; the circuit opened mid-retry")
}

func TestApplicationErrorsDoNotOpenTheCircuit(t *testing.T) {
	url, hits := flakyUpstream(t, 500)
	cfg := testConfig()
	cfg.FailureThreshold = 1
	r := NewResilience("svc", cfg, discard)
	app := guardedApp(r, url, time.Second)

	for range 3 {
		assert.Equal(t, http.StatusInternalServerError, do(t, app, http.MethodGet).status)
	}
	assert.Equal(t, int32(3), hits.Load(), "500 is not retried either")
	assert.NoError(t, r.Check(context.Background()))
}

func TestBackoffIsCappedWithJitter(t *testing.T) {
	r := NewResilience("svc", ResilienceConfig{BaseBackoff: 100 * time.Millisecond, MaxBackoff: time.Second}, discard)

	for attempt, ceiling := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, time.Second, time.Second} {
		for range 50 {
			d := r.backoff(attempt)
			assert.GreaterOrEqual(t, d, time.Duration(0))
			assert.Less(t, d, ceiling, "attempt %d", attempt)
		}
	}
}
