// Package handler contains the HTTP handlers of the API.
package handler

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/response"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/search"
)

// Searcher is the search use case consumed by ContentHandler.
type Searcher interface {
	Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, search.CacheStatus, error)
	Get(ctx context.Context, id int64) (domain.Content, scoring.Breakdown, error)
}

type ContentHandler struct {
	svc Searcher
}

func NewContentHandler(svc Searcher) *ContentHandler {
	return &ContentHandler{svc: svc}
}

type contentDTO struct {
	ID          int64          `json:"id"`
	Provider    string         `json:"provider"`
	ExternalID  string         `json:"external_id"`
	Title       string         `json:"title"`
	Type        string         `json:"type"`
	Score       float64        `json:"score"`
	Relevance   *float64       `json:"relevance,omitempty"`
	Metrics     domain.Metrics `json:"metrics"`
	Tags        []string       `json:"tags"`
	PublishedAt time.Time      `json:"published_at"`
}

type contentDetailDTO struct {
	contentDTO
	ScoreBreakdown scoring.Breakdown `json:"score_breakdown"`
}

type listMeta struct {
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
	Total      int    `json:"total"`
	TotalPages int    `json:"total_pages"`
	Sort       string `json:"sort"`
}

// List handles GET /api/v1/contents.
func (h *ContentHandler) List(c *fiber.Ctx) error {
	page, err := queryInt(c, "page")
	if err != nil {
		return err
	}
	perPage, err := queryInt(c, "per_page")
	if err != nil {
		return err
	}

	q := domain.SearchQuery{
		Keyword: c.Query("q"),
		Type:    domain.ContentType(c.Query("type")),
		Sort:    domain.SortBy(c.Query("sort")),
		Page:    page,
		PerPage: perPage,
	}
	res, status, err := h.svc.Search(c.UserContext(), q)
	if err != nil {
		return err
	}
	c.Set("X-Cache", string(status))

	items := make([]contentDTO, 0, len(res.Hits))
	for _, hit := range res.Hits {
		dto := toDTO(hit.Content)
		if res.Sort == domain.SortRelevance {
			r := round(hit.Relevance, 4)
			dto.Relevance = &r
		}
		items = append(items, dto)
	}
	return response.OK(c, items, listMeta{
		Page: res.Page, PerPage: res.PerPage, Total: res.Total, TotalPages: res.TotalPages(), Sort: string(res.Sort),
	})
}

// Get handles GET /api/v1/contents/:id.
func (h *ContentHandler) Get(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("%w: id must be a positive integer", domain.ErrInvalidInput)
	}
	content, breakdown, err := h.svc.Get(c.UserContext(), id)
	if err != nil {
		return err
	}
	breakdown = scoring.Breakdown{
		Base:            round(breakdown.Base, 4),
		TypeCoefficient: breakdown.TypeCoefficient,
		Recency:         breakdown.Recency,
		Engagement:      round(breakdown.Engagement, 4),
		Final:           round(breakdown.Final, 2),
	}
	return response.OK(c, contentDetailDTO{contentDTO: toDTO(content), ScoreBreakdown: breakdown}, nil)
}

func toDTO(c domain.Content) contentDTO {
	tags := c.Tags
	if tags == nil {
		tags = []string{}
	}
	return contentDTO{
		ID:          c.ID,
		Provider:    c.Provider,
		ExternalID:  c.ExternalID,
		Title:       c.Title,
		Type:        string(c.Type),
		Score:       round(c.Score, 2),
		Metrics:     c.Metrics,
		Tags:        tags,
		PublishedAt: c.PublishedAt,
	}
}

// queryInt parses an optional integer query parameter; absent means 0 (use the default).
func queryInt(c *fiber.Ctx, key string) (int, error) {
	v := c.Query(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", domain.ErrInvalidInput, key)
	}
	return n, nil
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}
