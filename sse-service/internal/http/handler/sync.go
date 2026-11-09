package handler

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/ingest"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
)

// Syncer is the ingestion use case consumed by SyncHandler.
type Syncer interface {
	Sync(ctx context.Context) (ingest.Report, error)
	SyncProvider(ctx context.Context, id int64) (ingest.Report, error)
	Status(ctx context.Context) ([]domain.SyncRun, error)
}

type SyncHandler struct {
	svc Syncer
}

func NewSyncHandler(svc Syncer) *SyncHandler {
	return &SyncHandler{svc: svc}
}

// Trigger handles POST /api/v1/sync: syncs every enabled provider.
func (h *SyncHandler) Trigger(c *fiber.Ctx) error {
	report, err := h.svc.Sync(c.UserContext())
	if err != nil {
		return err
	}
	return httpx.OK(c, report, nil)
}

// TriggerProvider handles POST /api/v1/providers/:id/sync.
func (h *SyncHandler) TriggerProvider(c *fiber.Ctx) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	report, err := h.svc.SyncProvider(c.UserContext(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, report, nil)
}

// Status handles GET /api/v1/sync/runs.
func (h *SyncHandler) Status(c *fiber.Ctx) error {
	runs, err := h.svc.Status(c.UserContext())
	if err != nil {
		return err
	}
	if runs == nil {
		runs = []domain.SyncRun{}
	}
	return httpx.OK(c, runs, nil)
}
