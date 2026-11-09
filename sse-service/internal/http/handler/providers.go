package handler

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/response"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/providers"
)

// ProviderManager is the provider management use case consumed by ProviderHandler.
type ProviderManager interface {
	List(ctx context.Context) ([]domain.Provider, error)
	Create(ctx context.Context, in providers.CreateInput) (domain.Provider, error)
	Update(ctx context.Context, id int64, in providers.UpdateInput) (domain.Provider, error)
}

type ProviderHandler struct {
	svc ProviderManager
}

func NewProviderHandler(svc ProviderManager) *ProviderHandler {
	return &ProviderHandler{svc: svc}
}

type providerDTO struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Format    string    `json:"format"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type createProviderRequest struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Format  string `json:"format"`
	Enabled *bool  `json:"enabled"`
}

type updateProviderRequest struct {
	URL     *string `json:"url"`
	Format  *string `json:"format"`
	Enabled *bool   `json:"enabled"`
}

// List handles GET /api/v1/providers.
func (h *ProviderHandler) List(c *fiber.Ctx) error {
	list, err := h.svc.List(c.UserContext())
	if err != nil {
		return err
	}
	out := make([]providerDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toProviderDTO(p))
	}
	return response.OK(c, out, nil)
}

// Create handles POST /api/v1/providers.
func (h *ProviderHandler) Create(c *fiber.Ctx) error {
	var req createProviderRequest
	if err := c.BodyParser(&req); err != nil {
		return fmt.Errorf("%w: request body must be valid JSON", domain.ErrInvalidInput)
	}
	p, err := h.svc.Create(c.UserContext(), providers.CreateInput{Name: req.Name, URL: req.URL, Format: req.Format, Enabled: req.Enabled})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(response.Data{Data: toProviderDTO(p)})
}

// Update handles PATCH /api/v1/providers/:id.
func (h *ProviderHandler) Update(c *fiber.Ctx) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req updateProviderRequest
	if err := c.BodyParser(&req); err != nil {
		return fmt.Errorf("%w: request body must be valid JSON", domain.ErrInvalidInput)
	}
	p, err := h.svc.Update(c.UserContext(), id, providers.UpdateInput{URL: req.URL, Format: req.Format, Enabled: req.Enabled})
	if err != nil {
		return err
	}
	return response.OK(c, toProviderDTO(p), nil)
}

func toProviderDTO(p domain.Provider) providerDTO {
	return providerDTO{
		ID: p.ID, Name: p.Name, URL: p.URL, Format: string(p.Format), Enabled: p.Enabled,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// pathID parses the :id route parameter.
func pathID(c *fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: id must be a positive integer", domain.ErrInvalidInput)
	}
	return id, nil
}
