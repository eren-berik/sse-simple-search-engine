package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SortBy defines the ordering of search results.
type SortBy string

const (
	SortPopularity SortBy = "popularity"
	SortRelevance  SortBy = "relevance"
)

const (
	DefaultPerPage   = 20
	MaxPerPage       = 100
	MaxKeywordLength = 200
)

// SearchQuery describes a content search request.
type SearchQuery struct {
	Keyword string
	Type    ContentType // empty means all types
	// Provider filters by provider name; empty means all providers.
	Provider string
	Sort     SortBy
	Page     int
	PerPage  int
}

// Normalize applies defaults and validates the query in place.
// Relevance sorting without a keyword falls back to popularity, since there is nothing to be relevant to.
func (q *SearchQuery) Normalize() error {
	q.Keyword = strings.Join(strings.Fields(q.Keyword), " ")
	if utf8.RuneCountInString(q.Keyword) > MaxKeywordLength {
		return fmt.Errorf("%w: keyword must be at most %d characters", ErrInvalidInput, MaxKeywordLength)
	}

	q.Provider = strings.ToLower(strings.TrimSpace(q.Provider))

	if q.Type != "" {
		t, err := ParseContentType(string(q.Type))
		if err != nil {
			return err
		}
		q.Type = t
	}

	switch q.Sort {
	case "":
		q.Sort = SortPopularity
		if q.Keyword != "" {
			q.Sort = SortRelevance
		}
	case SortPopularity, SortRelevance:
	default:
		return fmt.Errorf("%w: unknown sort %q (expected popularity or relevance)", ErrInvalidInput, q.Sort)
	}
	if q.Sort == SortRelevance && q.Keyword == "" {
		q.Sort = SortPopularity
	}

	if q.Page == 0 {
		q.Page = 1
	}
	if q.Page < 1 {
		return fmt.Errorf("%w: page must be >= 1", ErrInvalidInput)
	}
	if q.PerPage == 0 {
		q.PerPage = DefaultPerPage
	}
	if q.PerPage < 1 || q.PerPage > MaxPerPage {
		return fmt.Errorf("%w: per_page must be between 1 and %d", ErrInvalidInput, MaxPerPage)
	}
	return nil
}

// Offset returns the row offset for the requested page.
func (q SearchQuery) Offset() int {
	return (q.Page - 1) * q.PerPage
}

// SearchHit is a single search result with its query-time relevance.
type SearchHit struct {
	Content   Content
	Relevance float64
}

// SearchResult is a page of search hits.
type SearchResult struct {
	Hits    []SearchHit
	Total   int
	Page    int
	PerPage int
	Sort    SortBy
}

// TotalPages returns the number of pages for the result's total and page size.
func (r SearchResult) TotalPages() int {
	if r.PerPage <= 0 {
		return 0
	}
	return (r.Total + r.PerPage - 1) / r.PerPage
}

// ContentCount is the number of stored items of one type from one provider.
type ContentCount struct {
	Provider string
	Type     ContentType
	Count    int64
}
