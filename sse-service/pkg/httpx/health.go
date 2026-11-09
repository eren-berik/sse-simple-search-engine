package httpx

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Check reports whether a dependency is available.
type Check func(ctx context.Context) error

// Live handles GET /healthz: the process is up.
func Live(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// Ready handles GET /readyz. A failing required check makes the service unavailable (503);
// a failing optional check only reports "degraded", because the service can work without it.
func Ready(required, optional map[string]Check) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
		defer cancel()

		checks := fiber.Map{}
		status, code := "ok", fiber.StatusOK

		for name, check := range required {
			checks[name] = "ok"
			if err := check(ctx); err != nil {
				checks[name] = err.Error()
				status, code = "unavailable", fiber.StatusServiceUnavailable
			}
		}
		for name, check := range optional {
			checks[name] = "ok"
			if err := check(ctx); err != nil {
				checks[name] = err.Error()
				if code == fiber.StatusOK {
					status = "degraded"
				}
			}
		}
		return c.Status(code).JSON(fiber.Map{"status": status, "checks": checks})
	}
}
