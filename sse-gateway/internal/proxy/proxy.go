// Package proxy forwards API requests to the backing services.
package proxy

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
	"github.com/valyala/fasthttp"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
)

// Call sends the request upstream once. On success the upstream response is in c.Response().
type Call func() error

// Guard decides whether and how often a call is made, e.g. a circuit breaker with retries.
// It returns the last call error, or an error of its own that the app's error handler maps.
type Guard func(c *fiber.Ctx, call Call) error

// Target is a backing service.
type Target struct {
	URL     string // base URL, e.g. http://sse-service:8080
	Timeout time.Duration
	Guard   Guard // optional
}

// Route sends every request whose path starts with Prefix to Target.
type Route struct {
	Prefix string
	Target Target
}

// CallError is a failed attempt to reach the upstream (connection refused, reset, timeout).
// Errors that are not CallErrors came from the Guard.
type CallError struct{ Err error }

func (e *CallError) Error() string { return "upstream call: " + e.Err.Error() }
func (e *CallError) Unwrap() error { return e.Err }

// Forward proxies the request to the target, keeping path and query. The gateway is the edge,
// so it overwrites forwarding headers instead of trusting values sent by clients.
func Forward(t Target) fiber.Handler {
	base := strings.TrimRight(t.URL, "/")
	return func(c *fiber.Ctx) error {
		requestID, _ := c.Locals("requestid").(string)
		h := &c.Request().Header
		h.Set(fiber.HeaderXForwardedFor, c.IP())
		h.Set(fiber.HeaderXForwardedProto, c.Protocol())
		h.Set(fiber.HeaderXForwardedHost, c.Hostname())
		h.Set(httpx.HeaderRequestID, requestID)

		url := base + c.OriginalURL()
		call := func() error {
			if err := proxy.DoTimeout(c, url, t.Timeout); err != nil {
				return &CallError{Err: err}
			}
			return nil
		}
		var err error
		if t.Guard != nil {
			err = t.Guard(c, call)
		} else {
			err = call()
		}

		var callErr *CallError
		switch {
		case errors.Is(err, fasthttp.ErrTimeout):
			return httpx.Error(c, fiber.StatusGatewayTimeout, "gateway_timeout", "upstream service timed out")
		case errors.As(err, &callErr):
			return httpx.Error(c, fiber.StatusBadGateway, "bad_gateway", "upstream service unavailable")
		case err != nil:
			return err
		}
		// The upstream response replaces ours; restore the request id and hide upstream details.
		c.Response().Header.Set(httpx.HeaderRequestID, requestID)
		c.Response().Header.Del(fiber.HeaderServer)
		return nil
	}
}

// Mount registers a catch-all forwarder for each route on app. Middleware runs before
// forwarding, in the order given.
func Mount(app fiber.Router, routes []Route, middleware ...fiber.Handler) {
	for _, r := range routes {
		handlers := append(append([]fiber.Handler{}, middleware...), Forward(r.Target))
		app.All(r.Prefix, handlers...)
		app.All(r.Prefix+"/*", handlers...)
	}
}
