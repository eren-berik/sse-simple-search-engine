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
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/search"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
)

// Searcher is the search use case consumed by ContentHandler.
type Searcher interface {
	Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, search.CacheStatus, error)
	Get(ctx context.Context, id int64) (domain.Content, scoring.Breakdown, error)
	ExplainRelevance(ctx context.Context, id int64, keyword string) (domain.RelevanceExplanation, error)
}

type ContentHandler struct {
	svc Searcher
}

func NewContentHandler(svc Searcher) *ContentHandler {
	return &ContentHandler{svc: svc}
}

type contentDTO struct {
	ID         int64    `json:"id"`
	Provider   string   `json:"provider"`
	ExternalID string   `json:"external_id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Score      float64  `json:"score"`
	Relevance  *float64 `json:"relevance,omitempty"`
	// Match is the relevance as a percentage of the search's best match (100); only when sorted by relevance.
	Match       *int           `json:"match,omitempty"`
	Metrics     domain.Metrics `json:"metrics"`
	Tags        []string       `json:"tags"`
	PublishedAt time.Time      `json:"published_at"`
}

type contentDetailDTO struct {
	contentDTO
	ScoreBreakdown scoring.Breakdown `json:"score_breakdown"`
	// RelevanceBreakdown is present when the detail was requested with ?q=.
	RelevanceBreakdown *relevanceDTO `json:"relevance_breakdown,omitempty"`
}

type relevanceDTO struct {
	Query          string        `json:"query"`
	Matches        bool          `json:"matches"`
	Kind           string        `json:"kind"` // "full_text", "partial" or "none"
	Terms          []termDTO     `json:"terms"`
	TitleWeight    float64       `json:"title_weight"`
	TagWeight      float64       `json:"tag_weight"`
	Rank           float64       `json:"rank"`
	Steps          []rankStepDTO `json:"rank_steps"`
	RankNormalizer float64       `json:"rank_normalizer"`
	WordSimilarity float64       `json:"word_similarity"`
	PartialFactor  float64       `json:"partial_factor"`
	Relevance      float64       `json:"relevance"`
}

type termDTO struct {
	Term    string `json:"term"`
	InTitle int    `json:"in_title"`
	InTags  int    `json:"in_tags"`
}

type rankStepDTO struct {
	Field    string  `json:"field"`
	Weight   float64 `json:"weight"`
	Position int     `json:"position"`
	Value    float64 `json:"value"`
}

func toRelevanceDTO(e domain.RelevanceExplanation) *relevanceDTO {
	kind := "none"
	switch {
	case e.FullText:
		kind = "full_text"
	case e.Matches:
		kind = "partial"
	}
	dto := &relevanceDTO{
		Query: e.Query, Matches: e.Matches, Kind: kind, Terms: []termDTO{}, Steps: []rankStepDTO{},
		TitleWeight: e.TitleWeight, TagWeight: e.TagWeight, Rank: round(e.Rank, 4),
		RankNormalizer: round(domain.RankNormalizer, 4), WordSimilarity: round(e.WordSimilarity, 4),
		PartialFactor: e.PartialFactor, Relevance: round(e.Relevance, 4),
	}
	for _, t := range e.Terms {
		dto.Terms = append(dto.Terms, termDTO{Term: t.Term, InTitle: t.InTitle, InTags: t.InTags})
	}
	for _, s := range e.Steps {
		dto.Steps = append(dto.Steps, rankStepDTO{Field: s.Field, Weight: s.Weight, Position: s.Position, Value: round(s.Value, 4)})
	}
	return dto
}

type listMeta struct {
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
	Total      int    `json:"total"`
	TotalPages int    `json:"total_pages"`
	Sort       string `json:"sort"`
	// BestRelevance is the relevance of the search's best match, the 100% of every match.
	BestRelevance *float64 `json:"best_relevance,omitempty"`
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

	from, err := queryDate(c, "published_from")
	if err != nil {
		return err
	}
	to, err := queryDate(c, "published_to")
	if err != nil {
		return err
	}

	q := domain.SearchQuery{
		Keyword:       c.Query("q"),
		Type:          domain.ContentType(c.Query("type")),
		Provider:      c.Query("provider"),
		Tag:           c.Query("tag"),
		PublishedFrom: from,
		PublishedTo:   to,
		Sort:          domain.SortBy(c.Query("sort")),
		Page:          page,
		PerPage:       perPage,
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
			m := int(math.Round(hit.Match * 100))
			dto.Match = &m
		}
		items = append(items, dto)
	}
	meta := listMeta{
		Page: res.Page, PerPage: res.PerPage, Total: res.Total, TotalPages: res.TotalPages(), Sort: string(res.Sort),
	}
	if res.Sort == domain.SortRelevance && res.BestRelevance > 0 {
		best := round(res.BestRelevance, 4)
		meta.BestRelevance = &best
	}
	return httpx.OK(c, items, meta)
}

// Get handles GET /api/v1/contents/:id.
func (h *ContentHandler) Get(c *fiber.Ctx) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	content, breakdown, err := h.svc.Get(c.UserContext(), id)
	if err != nil {
		return err
	}
	dto := contentDetailDTO{contentDTO: toDTO(content), ScoreBreakdown: roundBreakdown(breakdown)}
	if keyword := c.Query("q"); keyword != "" {
		explanation, err := h.svc.ExplainRelevance(c.UserContext(), id, keyword)
		if err != nil {
			return err
		}
		dto.RelevanceBreakdown = toRelevanceDTO(explanation)
	}
	return httpx.OK(c, dto, nil)
}

// roundBreakdown trims floating-point noise (0.0800000001) from the score and every step of
// its explanation, keeping the explanation intact.
func roundBreakdown(b scoring.Breakdown) scoring.Breakdown {
	b.Base = round(b.Base, 4)
	b.Engagement = round(b.Engagement, 4)
	b.Final = round(b.Final, 2)
	terms := make([]scoring.BaseTerm, len(b.Explanation.BaseTerms))
	for i, t := range b.Explanation.BaseTerms {
		t.Result = round(t.Result, 4)
		terms[i] = t
	}
	b.Explanation.BaseTerms = terms
	b.Explanation.Engagement.Result = round(b.Explanation.Engagement.Result, 4)
	return b
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

// queryDate parses an optional YYYY-MM-DD query parameter as a UTC day; absent means the zero time.
func queryDate(c *fiber.Ctx, key string) (time.Time, error) {
	v := c.Query(key)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s must be a date in YYYY-MM-DD format", domain.ErrInvalidInput, key)
	}
	return t, nil
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
