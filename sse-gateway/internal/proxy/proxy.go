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

// Route sends every request whose path starts with Prefix to Upstream.
type Route struct {
	Prefix   string
	Upstream string // base URL, e.g. http://sse-service:8080
}

// Forward proxies the request to upstream, keeping path and query. The gateway is the edge,
// so it overwrites forwarding headers instead of trusting values sent by clients.
func Forward(upstream string, timeout time.Duration) fiber.Handler {
	upstream = strings.TrimRight(upstream, "/")
	return func(c *fiber.Ctx) error {
		requestID, _ := c.Locals("requestid").(string)
		h := &c.Request().Header
		h.Set(fiber.HeaderXForwardedFor, c.IP())
		h.Set(fiber.HeaderXForwardedProto, c.Protocol())
		h.Set(fiber.HeaderXForwardedHost, c.Hostname())
		h.Set(httpx.HeaderRequestID, requestID)

		if err := proxy.DoTimeout(c, upstream+c.OriginalURL(), timeout); err != nil {
			if errors.Is(err, fasthttp.ErrTimeout) {
				return httpx.Error(c, fiber.StatusGatewayTimeout, "gateway_timeout", "upstream service timed out")
			}
			return httpx.Error(c, fiber.StatusBadGateway, "bad_gateway", "upstream service unavailable")
		}
		// The upstream response replaces ours; restore the request id and hide upstream details.
		c.Response().Header.Set(httpx.HeaderRequestID, requestID)
		c.Response().Header.Del(fiber.HeaderServer)
		return nil
	}
}

// Mount registers a catch-all forwarder for each route on app. Middleware runs before
// forwarding, in the order given.
func Mount(app fiber.Router, routes []Route, timeout time.Duration, middleware ...fiber.Handler) {
	for _, r := range routes {
		handlers := append(append([]fiber.Handler{}, middleware...), Forward(r.Upstream, timeout))
		app.All(r.Prefix, handlers...)
		app.All(r.Prefix+"/*", handlers...)
	}
}
