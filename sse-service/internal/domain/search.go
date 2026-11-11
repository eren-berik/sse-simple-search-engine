package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// SortBy defines the ordering of search results.
type SortBy string

const (
	SortPopularity SortBy = "popularity"
	SortRelevance  SortBy = "relevance"
)

// TagMatch defines how several tags filter: items carrying any of them, or all of them.
type TagMatch string

const (
	TagMatchAny TagMatch = "any"
	TagMatchAll TagMatch = "all"
)

const (
	DefaultPerPage   = 20
	MaxPerPage       = 100
	MaxKeywordLength = 200
	MaxTagLength     = 50
	MaxTags          = 20
)

// SearchQuery describes a content search request.
type SearchQuery struct {
	Keyword string
	Type    ContentType // empty means all types
	// Provider filters by provider name; empty means all providers.
	Provider string
	// Tags keeps contents carrying any or all of these tags, as TagMatch says (tags are stored
	// lowercase); empty means no tag filter.
	Tags     []string
	TagMatch TagMatch
	// PublishedFrom and PublishedTo bound the publication day, both inclusive (UTC dates);
	// the zero time means unbounded.
	PublishedFrom time.Time
	PublishedTo   time.Time
	Sort          SortBy
	Page          int
	PerPage       int
}

// Normalize applies defaults and validates the query in place.
// Results are sorted by popularity unless relevance is asked for. Relevance sorting without a
// keyword falls back to popularity, since there is nothing to be relevant to.
func (q *SearchQuery) Normalize() error {
	q.Keyword = strings.Join(strings.Fields(q.Keyword), " ")
	if utf8.RuneCountInString(q.Keyword) > MaxKeywordLength {
		return fmt.Errorf("%w: keyword must be at most %d characters", ErrInvalidInput, MaxKeywordLength)
	}

	q.Provider = strings.ToLower(strings.TrimSpace(q.Provider))
	if err := q.normalizeTags(); err != nil {
		return err
	}
	if !q.PublishedFrom.IsZero() && !q.PublishedTo.IsZero() && q.PublishedFrom.After(q.PublishedTo) {
		return fmt.Errorf("%w: published_from must not be after published_to", ErrInvalidInput)
	}

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

// normalizeTags lowercases the tags and drops blanks and duplicates. They are sorted, so the
// same set of tags is the same query (and cache entry) in any order. Without tags the match
// mode does not matter and is reset to the default.
func (q *SearchQuery) normalizeTags() error {
	var tags []string
	for _, t := range q.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > MaxTagLength {
			return fmt.Errorf("%w: tag must be at most %d characters", ErrInvalidInput, MaxTagLength)
		}
		tags = append(tags, t)
	}
	slices.Sort(tags)
	tags = slices.Compact(tags)
	if len(tags) > MaxTags {
		return fmt.Errorf("%w: at most %d tags can be filtered by", ErrInvalidInput, MaxTags)
	}
	q.Tags = tags

	switch q.TagMatch {
	case "":
		q.TagMatch = TagMatchAny
	case TagMatchAny, TagMatchAll:
	default:
		return fmt.Errorf("%w: unknown tag_match %q (expected any or all)", ErrInvalidInput, q.TagMatch)
	}
	if len(q.Tags) == 0 {
		q.TagMatch = TagMatchAny
	}
	return nil
}

// Offset returns the row offset for the requested page.
func (q SearchQuery) Offset() int {
	return (q.Page - 1) * q.PerPage
}

// SearchHit is a single search result with its query-time relevance. Match is the relevance
// relative to the best match of the whole search (0..1, the best is 1); it is only set when
// results are sorted by relevance.
type SearchHit struct {
	Content   Content
	Relevance float64
	Match     float64
}

// SearchResult is a page of search hits.
type SearchResult struct {
	// BestRelevance is the relevance of the search's best match, the reference of every Match;
	// zero unless sorted by relevance.
	BestRelevance float64
	Hits          []SearchHit
	Total         int
	Page          int
	PerPage       int
	Sort          SortBy
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

// TagCount is a tag and how many listed items carry it.
type TagCount struct {
	Name  string
	Count int
}
