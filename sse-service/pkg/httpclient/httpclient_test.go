package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClient(retries int) *Client {
	return New(Config{
		Timeout:     time.Second,
		MaxRetries:  retries,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
	})
}

// sequenceServer responds with the given status codes in order, then 200 "ok".
func sequenceServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n < len(statuses) {
			w.WriteHeader(statuses[n])
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestClientRetriesTransientErrors(t *testing.T) {
	srv, calls := sequenceServer(t, http.StatusInternalServerError, http.StatusTooManyRequests)

	body, err := testClient(3).Get(context.Background(), srv.URL)

	require.NoError(t, err)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, int32(3), calls.Load())
}

func TestClientDoesNotRetryClientErrors(t *testing.T) {
	srv, calls := sequenceServer(t, http.StatusNotFound)

	_, err := testClient(3).Get(context.Background(), srv.URL)

	var se *StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusNotFound, se.StatusCode)
	assert.Equal(t, int32(1), calls.Load())
}

func TestClientGivesUpAfterMaxRetries(t *testing.T) {
	srv, calls := sequenceServer(t, 500, 500, 500, 500, 500)

	_, err := testClient(2).Get(context.Background(), srv.URL)

	require.Error(t, err)
	assert.Equal(t, int32(3), calls.Load())
}

func TestClientHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	c := New(Config{Timeout: time.Second, MaxRetries: 1, BaseBackoff: time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	start := time.Now()
	_, err := c.Get(context.Background(), srv.URL)

	require.NoError(t, err)
	// Retry-After (1s) is capped by MaxBackoff, so the wait is ~50ms rather than ~1ms.
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
}

func TestClientRateLimits(t *testing.T) {
	srv, _ := sequenceServer(t)
	c := New(Config{Timeout: time.Second, RatePerSecond: 20, Burst: 1})

	start := time.Now()
	for range 3 {
		_, err := c.Get(context.Background(), srv.URL)
		require.NoError(t, err)
	}
	// 3 requests at 20 rps with burst 1 need at least 2 intervals of 50ms.
	assert.GreaterOrEqual(t, time.Since(start), 90*time.Millisecond)
}

func TestClientStopsOnContextCancel(t *testing.T) {
	srv, _ := sequenceServer(t, 500, 500, 500)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := testClient(3).Get(ctx, srv.URL)

	assert.ErrorIs(t, err, context.Canceled)
}

func breakerClient(threshold uint32, open time.Duration, retries int, states *[]BreakerState) *Client {
	return New(Config{
		Timeout: time.Second, MaxRetries: retries, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
		BreakerFailureThreshold: threshold, BreakerOpenTimeout: open, Name: "feed",
		OnStateChange: func(name string, _, to BreakerState) {
			if name == "feed" {
				*states = append(*states, to)
			}
		},
	})
}

func TestBreakerOpensAndRecovers(t *testing.T) {
	srv, calls := sequenceServer(t, 500, 500, 500) // then 200
	var states []BreakerState
	c := breakerClient(3, 50*time.Millisecond, 0, &states)

	for range 3 {
		_, err := c.Get(context.Background(), srv.URL)
		require.Error(t, err)
	}
	assert.Equal(t, BreakerOpen, c.State())

	_, err := c.Get(context.Background(), srv.URL)
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.Equal(t, int32(3), calls.Load(), "an open circuit does not call the remote")

	time.Sleep(60 * time.Millisecond)
	body, err := c.Get(context.Background(), srv.URL)
	require.NoError(t, err, "a trial call closes the circuit")
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, BreakerClosed, c.State())
	assert.Equal(t, []BreakerState{BreakerOpen, BreakerHalfOpen, BreakerClosed}, states)
}

func TestBreakerCountsEveryAttemptAndStopsRetries(t *testing.T) {
	srv, calls := sequenceServer(t, 503, 503, 503, 503, 503, 503)
	var states []BreakerState
	c := breakerClient(2, time.Minute, 5, &states)

	_, err := c.Get(context.Background(), srv.URL)

	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.Equal(t, int32(2), calls.Load(), "retries stop once the circuit opens")
}

func TestBreakerIgnoresCancellation(t *testing.T) {
	srv, _ := sequenceServer(t)
	var states []BreakerState
	c := breakerClient(1, time.Minute, 0, &states)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Get(ctx, srv.URL)

	require.Error(t, err)
	assert.Equal(t, BreakerClosed, c.State(), "a cancelled sync says nothing about the remote")
}

func TestBreakerDisabledByDefault(t *testing.T) {
	srv, calls := sequenceServer(t, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500)
	c := testClient(0)

	for range 10 {
		_, err := c.Get(context.Background(), srv.URL)
		assert.NotErrorIs(t, err, ErrCircuitOpen)
	}
	assert.Equal(t, int32(10), calls.Load())
}
