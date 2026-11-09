package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sony/gobreaker/v2"
	"github.com/valyala/fasthttp"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/internal/proxy"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
)

// ErrCircuitOpen means an upstream failed repeatedly and is not called until its breaker
// lets a trial request through.
var ErrCircuitOpen = errors.New("upstream circuit is open")

// ResilienceConfig tunes how calls to one upstream are protected.
type ResilienceConfig struct {
	FailureThreshold uint32        // consecutive failures that open the circuit
	OpenTimeout      time.Duration // how long the circuit stays open before a trial request
	HalfOpenRequests uint32        // concurrent trial requests while half-open
	MaxRetries       int           // retries for idempotent requests; 0 disables retrying
	BaseBackoff      time.Duration // first retry delay; doubles per attempt, with full jitter
	MaxBackoff       time.Duration
}

// Resilience guards the calls to one upstream service with a circuit breaker and retries.
//
// Retries wrap the breaker, so every attempt is counted and an open circuit stops retrying
// at once. Only idempotent requests (GET, HEAD) are retried, and only after failures that
// are likely transient and fast: connection errors, 502 and 503. Timeouts (also while
// connecting) are not retried, since they already used the whole time budget. Writes such as login or sync are never
// retried: the first attempt may have reached the service even though the response was lost.
type Resilience struct {
	name    string
	cfg     ResilienceConfig
	breaker *gobreaker.CircuitBreaker[struct{}]
	sleep   func(ctx context.Context, d time.Duration) error
}

func NewResilience(name string, cfg ResilienceConfig, log *slog.Logger) *Resilience {
	return &Resilience{
		name: name,
		cfg:  cfg,
		breaker: gobreaker.NewCircuitBreaker[struct{}](gobreaker.Settings{
			Name:        name,
			MaxRequests: cfg.HalfOpenRequests,
			Timeout:     cfg.OpenTimeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= cfg.FailureThreshold
			},
			OnStateChange: func(name string, from, to gobreaker.State) {
				log.Warn("circuit breaker state changed", "upstream", name, "from", from.String(), "to", to.String())
			},
		}),
		sleep: sleepContext,
	}
}

// Guard is a proxy.Guard.
func (r *Resilience) Guard(c *fiber.Ctx, call proxy.Call) error {
	retries := 0
	if c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead {
		retries = r.cfg.MaxRetries
	}
	for attempt := 0; ; attempt++ {
		err := r.attempt(c, call)
		if errors.Is(err, ErrCircuitOpen) {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(max(1, int(r.cfg.OpenTimeout.Seconds()))))
			return err
		}
		if err == nil || attempt >= retries || !retryable(err) {
			return finalError(err)
		}
		if r.sleep(c.UserContext(), r.backoff(attempt)) != nil {
			return finalError(err)
		}
	}
}

// Check reports an open circuit; used for readiness.
func (r *Resilience) Check(context.Context) error {
	if r.breaker.State() == gobreaker.StateOpen {
		return fmt.Errorf("circuit open after repeated failures of %s", r.name)
	}
	return nil
}

// attempt makes one call through the breaker. An upstream that answers 502, 503 or 504 is
// counted as failing even though the call itself succeeded.
func (r *Resilience) attempt(c *fiber.Ctx, call proxy.Call) error {
	_, err := r.breaker.Execute(func() (struct{}, error) {
		if err := call(); err != nil {
			return struct{}{}, err
		}
		if status := c.Response().StatusCode(); isUnavailableStatus(status) {
			return struct{}{}, unavailableStatus(status)
		}
		return struct{}{}, nil
	})
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return ErrCircuitOpen
	}
	return err
}

// backoff returns the delay before retry number attempt+1: exponential, capped, with full
// jitter so that clients retrying together do not hit the recovering service in lockstep.
func (r *Resilience) backoff(attempt int) time.Duration {
	ceiling := min(r.cfg.BaseBackoff<<attempt, r.cfg.MaxBackoff)
	if ceiling <= 0 {
		return 0
	}
	return rand.N(ceiling) //nolint:gosec // jitter, not a secret
}

// unavailableStatus is an upstream response that signals the service is down or overloaded.
type unavailableStatus int

func (s unavailableStatus) Error() string { return fmt.Sprintf("upstream answered %d", int(s)) }

func isUnavailableStatus(status int) bool {
	return status == fiber.StatusBadGateway || status == fiber.StatusServiceUnavailable ||
		status == fiber.StatusGatewayTimeout
}

func retryable(err error) bool {
	var status unavailableStatus
	if errors.As(err, &status) {
		return int(status) != fiber.StatusGatewayTimeout
	}
	var callErr *proxy.CallError
	return errors.As(err, &callErr) && !isTimeout(err)
}

// isTimeout reports a call that used up its time budget, while connecting or waiting for the
// response; retrying it would multiply the client's wait.
func isTimeout(err error) bool {
	return errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, fasthttp.ErrDialTimeout)
}

// finalError hands an upstream's own 5xx answer back to the client unchanged.
func finalError(err error) error {
	var status unavailableStatus
	if errors.As(err, &status) {
		return nil
	}
	return err
}

// ErrorMapper maps ErrCircuitOpen for httpx.ErrorHandler.
func ErrorMapper(err error) (httpx.MappedError, bool) {
	if errors.Is(err, ErrCircuitOpen) {
		return httpx.MappedError{
			Status:  fiber.StatusServiceUnavailable,
			Code:    "upstream_unavailable",
			Message: "the service is temporarily unavailable, try again shortly",
		}, true
	}
	return httpx.MappedError{}, false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
