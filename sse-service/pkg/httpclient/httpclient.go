// Package httpclient is an outbound HTTP client with per-client rate limiting, retries and
// an optional circuit breaker.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

// ErrCircuitOpen means the remote failed repeatedly and calls are paused until the breaker
// lets a trial request through.
var ErrCircuitOpen = errors.New("circuit open: the remote failed repeatedly, calls are paused")

// BreakerState is the state of a client's circuit breaker.
type BreakerState int

const (
	BreakerClosed BreakerState = iota
	BreakerHalfOpen
	BreakerOpen
)

func (s BreakerState) String() string {
	switch s {
	case BreakerHalfOpen:
		return "half-open"
	case BreakerOpen:
		return "open"
	default:
		return "closed"
	}
}

// maxBodyBytes caps provider responses to protect against unbounded payloads.
const maxBodyBytes = 10 << 20

// Config configures the outbound HTTP client used by providers.
type Config struct {
	Timeout       time.Duration // per attempt
	RatePerSecond float64       // sustained request rate towards the provider
	Burst         int
	MaxRetries    int
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration

	// Circuit breaker. After BreakerFailureThreshold consecutive failed attempts calls fail fast
	// with ErrCircuitOpen for BreakerOpenTimeout; then one trial call decides whether to close.
	// A zero threshold disables the breaker.
	BreakerFailureThreshold uint32
	BreakerOpenTimeout      time.Duration
	// Name identifies the client in OnStateChange.
	Name          string
	OnStateChange func(name string, from, to BreakerState)
}

// Client is an HTTP client that enforces a per-provider request rate limit, retries transient
// failures (network errors, 429, 5xx) with exponential backoff and, when configured, stops
// calling a remote that keeps failing.
type Client struct {
	http    *http.Client
	limiter *rate.Limiter
	breaker *gobreaker.CircuitBreaker[[]byte] // nil when disabled
	cfg     Config
}

func New(cfg Config) *Client {
	if cfg.Burst < 1 {
		cfg.Burst = 1
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 200 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Second
	}
	limit := rate.Inf
	if cfg.RatePerSecond > 0 {
		limit = rate.Limit(cfg.RatePerSecond)
	}
	c := &Client{
		http:    &http.Client{Timeout: cfg.Timeout},
		limiter: rate.NewLimiter(limit, cfg.Burst),
		cfg:     cfg,
	}
	if cfg.BreakerFailureThreshold > 0 {
		c.breaker = gobreaker.NewCircuitBreaker[[]byte](gobreaker.Settings{
			Name:        cfg.Name,
			MaxRequests: 1,
			Timeout:     cfg.BreakerOpenTimeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= cfg.BreakerFailureThreshold
			},
			// A cancelled sync says nothing about the remote's health.
			IsExcluded: func(err error) bool {
				return errors.Is(err, context.Canceled)
			},
			OnStateChange: func(name string, from, to gobreaker.State) {
				if cfg.OnStateChange != nil {
					cfg.OnStateChange(name, breakerState(from), breakerState(to))
				}
			},
		})
	}
	return c
}

// State reports the breaker state; BreakerClosed when the breaker is disabled.
func (c *Client) State() BreakerState {
	if c.breaker == nil {
		return BreakerClosed
	}
	return breakerState(c.breaker.State())
}

func breakerState(s gobreaker.State) BreakerState {
	switch s {
	case gobreaker.StateHalfOpen:
		return BreakerHalfOpen
	case gobreaker.StateOpen:
		return BreakerOpen
	default:
		return BreakerClosed
	}
}

// StatusError is returned for non-2xx responses.
type StatusError struct {
	StatusCode int
	retryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.StatusCode)
}

func (e *StatusError) retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// Get fetches url and returns the response body.
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, c.backoff(attempt, lastErr)); err != nil {
				return nil, errors.Join(lastErr, err)
			}
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter: %w", err)
		}

		body, err := c.attempt(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if errors.Is(err, ErrCircuitOpen) || !isRetryable(ctx, err) {
			break
		}
	}
	return nil, fmt.Errorf("GET %s: %w", url, lastErr)
}

// attempt makes one request through the breaker, so every attempt counts towards opening it.
func (c *Client) attempt(ctx context.Context, url string) ([]byte, error) {
	if c.breaker == nil {
		return c.do(ctx, url)
	}
	body, err := c.breaker.Execute(func() ([]byte, error) { return c.do(ctx, url) })
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return nil, ErrCircuitOpen
	}
	return body, err
}

func (c *Client) do(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		return nil, &StatusError{StatusCode: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBodyBytes)
	}
	return body, nil
}

func (c *Client) backoff(attempt int, lastErr error) time.Duration {
	var se *StatusError
	if errors.As(lastErr, &se) && se.retryAfter > 0 {
		return min(se.retryAfter, c.cfg.MaxBackoff)
	}
	d := c.cfg.BaseBackoff << (attempt - 1)
	return min(d, c.cfg.MaxBackoff)
}

func isRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.retryable()
	}
	// Transport errors (timeouts, connection resets) are considered transient.
	return true
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
