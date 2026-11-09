package handler

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Pinger is a dependency that can report its availability.
type Pinger interface {
	Ping(ctx context.Context) error
}

type HealthHandler struct {
	db    Pinger
	cache Pinger
}

func NewHealthHandler(db, cache Pinger) *HealthHandler {
	return &HealthHandler{db: db, cache: cache}
}

// Live handles GET /healthz: the process is up.
func (h *HealthHandler) Live(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// Ready handles GET /readyz. The database is required; the cache is optional because
// searches fall back to the database, so a cache outage only reports "degraded".
func (h *HealthHandler) Ready(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()

	checks := fiber.Map{"database": "ok", "cache": "ok"}
	status, code := "ok", fiber.StatusOK

	if err := h.db.Ping(ctx); err != nil {
		checks["database"] = err.Error()
		status, code = "unavailable", fiber.StatusServiceUnavailable
	}
	if err := h.cache.Ping(ctx); err != nil {
		checks["cache"] = err.Error()
		if code == fiber.StatusOK {
			status = "degraded"
		}
	}
	return c.Status(code).JSON(fiber.Map{"status": status, "checks": checks})
}
