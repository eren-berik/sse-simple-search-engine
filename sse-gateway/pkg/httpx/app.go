package httpx

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
)

// HeaderRequestID carries the request id across services; the gateway sets it and services reuse it.
const HeaderRequestID = "X-Request-ID"

type AppConfig struct {
	Name         string
	Log          *slog.Logger
	ErrorMappers []ErrorMapper
	// TrustProxyHeaders makes c.IP() read X-Forwarded-For. Enable it only for services that are
	// reachable exclusively through the gateway, otherwise clients could spoof their address.
	TrustProxyHeaders bool
}

// NewApp returns a Fiber app with the shared configuration and base middleware:
// request id propagation, structured request logging and panic recovery.
func NewApp(cfg AppConfig) *fiber.App {
	fc := fiber.Config{
		AppName:               cfg.Name,
		DisableStartupMessage: true,
		ErrorHandler:          ErrorHandler(cfg.Log, cfg.ErrorMappers...),
		// fasthttp reuses request buffers; immutable strings are safe to keep beyond the handler.
		Immutable:    true,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		BodyLimit:    1 << 20,
	}
	if cfg.TrustProxyHeaders {
		fc.ProxyHeader = fiber.HeaderXForwardedFor
	}

	app := fiber.New(fc)
	app.Use(requestid.New(requestid.Config{Header: HeaderRequestID}))
	app.Use(RequestLogger(cfg.Log))
	app.Use(recover.New())
	return app
}

// RequestLogger logs one structured line per request.
func RequestLogger(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		if err := c.Next(); err != nil {
			// Run the error handler now so the logged status is the one sent to the client.
			if herr := c.App().ErrorHandler(c, err); herr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		status := c.Response().StatusCode()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		log.LogAttrs(c.UserContext(), level, "request",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.IP()),
			slog.Any("request_id", c.Locals("requestid")),
		)
		return nil
	}
}
