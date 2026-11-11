// Package search implements content search with a cache-aside layer.
package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/cache"
)

// ContentSearcher is the storage backend used for searches. The PostgreSQL repository implements it;
// a dedicated search engine (e.g. Elasticsearch) could replace it without touching this service.
type ContentSearcher interface {
	Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error)
	GetByID(ctx context.Context, id int64) (domain.Content, error)
	ExplainRelevance(ctx context.Context, id int64, keyword string) (domain.RelevanceExplanation, error)
	Tags(ctx context.Context) ([]domain.TagCount, error)
}

// CacheStatus reports how a search was served.
type CacheStatus string

const (
	CacheHit    CacheStatus = "HIT"
	CacheMiss   CacheStatus = "MISS"
	CacheBypass CacheStatus = "BYPASS" // cache unavailable, served from the database
)

const (
	versionKey   = "search:version"
	queryTimeout = 5 * time.Second
	// warmTimeout bounds warming as a whole; it runs on the caller's path after invalidation.
	warmTimeout = 5 * time.Second
)

type Service struct {
	repo   ContentSearcher
	cache  cache.Cache
	ttl    time.Duration
	scorer *scoring.Scorer
	log    *slog.Logger
	group  singleflight.Group
	// warm are searched right after every invalidation; see WithWarmQueries.
	warm []domain.SearchQuery
}

func NewService(repo ContentSearcher, c cache.Cache, ttl time.Duration, scorer *scoring.Scorer, log *slog.Logger) *Service {
	return &Service{repo: repo, cache: c, ttl: ttl, scorer: scorer, log: log}
}

// WithWarmQueries makes every invalidation refill the cache for qs, typically the searches a
// dashboard runs when it opens. Otherwise the first users after each sync would all miss.
func (s *Service) WithWarmQueries(qs []domain.SearchQuery) *Service {
	s.warm = qs
	return s
}

// Search validates q and returns matching contents, serving from cache when possible.
// Concurrent identical queries on a cache miss share a single database query.
// Cache failures never fail the request; the database is the source of truth.
func (s *Service) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, CacheStatus, error) {
	if err := q.Normalize(); err != nil {
		return domain.SearchResult{}, "", err
	}
	return cached(ctx, s, func(version string) string { return buildKey(version, q) },
		func(ctx context.Context) (domain.SearchResult, error) { return s.repo.Search(ctx, q) })
}

// Tags returns the tags of the listed items with their counts, for the dashboard's tag filter.
// It is cached like a search and goes stale with them, since a sync changes both.
func (s *Service) Tags(ctx context.Context) ([]domain.TagCount, CacheStatus, error) {
	return cached(ctx, s, func(version string) string { return "search:v" + version + ":tags" }, s.repo.Tags)
}

// cached serves a value from the cache under the key built from the current cache version,
// and on a miss loads it, stores it and returns it. Concurrent loads of one key share a single
// call. Cache failures never fail the request; the database is the source of truth.
func cached[T any](ctx context.Context, s *Service, key func(version string) string, load func(context.Context) (T, error)) (T, CacheStatus, error) {
	var zero T
	version, err := s.version(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "cache unavailable, bypassing", "error", err)
		v, err := load(ctx)
		return v, CacheBypass, err
	}
	k := key(version)

	if data, ok, err := s.cache.Get(ctx, k); err != nil {
		s.log.WarnContext(ctx, "cache get failed", "error", err)
	} else if ok {
		var v T
		if err := json.Unmarshal(data, &v); err == nil {
			return v, CacheHit, nil
		}
		s.log.WarnContext(ctx, "discarding undecodable cache entry", "key", k)
	}

	v, err, _ := s.group.Do(k, func() (any, error) {
		// The shared query must not be cancelled just because the first caller went away.
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queryTimeout)
		defer cancel()

		v, err := load(qctx)
		if err != nil {
			return zero, err
		}
		if data, err := json.Marshal(v); err == nil {
			if err := s.cache.Set(qctx, k, data, s.ttl); err != nil {
				s.log.WarnContext(ctx, "cache set failed", "error", err)
			}
		}
		return v, nil
	})
	if err != nil {
		return zero, CacheMiss, err
	}
	return v.(T), CacheMiss, nil
}

// Get returns a content item with a live breakdown of its score.
func (s *Service) Get(ctx context.Context, id int64) (domain.Content, scoring.Breakdown, error) {
	c, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Content{}, scoring.Breakdown{}, err
	}
	return c, s.scorer.Score(c), nil
}

// ExplainRelevance shows how an item's relevance for keyword was computed. The keyword is
// normalized like a search's, so the explanation matches the ranking the user saw.
func (s *Service) ExplainRelevance(ctx context.Context, id int64, keyword string) (domain.RelevanceExplanation, error) {
	q := domain.SearchQuery{Keyword: keyword}
	if err := q.Normalize(); err != nil {
		return domain.RelevanceExplanation{}, err
	}
	if q.Keyword == "" {
		return domain.RelevanceExplanation{}, fmt.Errorf("%w: a keyword is needed to explain relevance", domain.ErrInvalidInput)
	}
	return s.repo.ExplainRelevance(ctx, id, q.Keyword)
}

// Invalidate makes every cached search stale by bumping the global version embedded in cache keys.
// Old entries are never read again and expire through their TTL, so no key scanning is needed.
// The warm queries are then searched again, so the most common searches stay cached.
func (s *Service) Invalidate(ctx context.Context) error {
	if _, err := s.cache.Incr(ctx, versionKey); err != nil {
		return fmt.Errorf("invalidate search cache: %w", err)
	}
	s.warmUp(ctx)
	return nil
}

// warmUp searches the warm queries and loads the tags, which caches them under the new version. Failures
// are logged: a query that could not be warmed is simply computed by its first user.
func (s *Service) warmUp(ctx context.Context) {
	if len(s.warm) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, warmTimeout)
	defer cancel()
	start := time.Now()
	warmed := 0
	for _, q := range s.warm {
		if _, status, err := s.Search(ctx, q); err != nil {
			s.log.WarnContext(ctx, "warming a cached search failed", "error", err)
		} else if status != CacheBypass {
			warmed++
		}
	}
	if _, status, err := s.Tags(ctx); err != nil {
		s.log.WarnContext(ctx, "warming the cached tags failed", "error", err)
	} else if status != CacheBypass {
		warmed++
	}
	s.log.InfoContext(ctx, "search cache warmed", "queries", warmed, "duration", time.Since(start))
}

// version returns the current cache version, which every cache key embeds.
func (s *Service) version(ctx context.Context) (string, error) {
	data, ok, err := s.cache.Get(ctx, versionKey)
	if err != nil {
		return "", err
	}
	if !ok {
		return "0", nil
	}
	return string(data), nil
}

// buildKey derives a cache key from a normalized query. Keyword matching is case-insensitive, so the key is too.
func buildKey(version string, q domain.SearchQuery) string {
	raw := strings.Join([]string{
		strings.ToLower(q.Keyword), string(q.Type), q.Provider, strings.Join(q.Tags, "\x01"), string(q.TagMatch),
		dateKey(q.PublishedFrom), dateKey(q.PublishedTo), string(q.Sort),
		strconv.Itoa(q.Page), strconv.Itoa(q.PerPage),
	}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return "search:v" + version + ":" + hex.EncodeToString(sum[:])
}

func dateKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}
