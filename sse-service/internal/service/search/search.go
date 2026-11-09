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
)

type Service struct {
	repo   ContentSearcher
	cache  cache.Cache
	ttl    time.Duration
	scorer *scoring.Scorer
	log    *slog.Logger
	group  singleflight.Group
}

func NewService(repo ContentSearcher, c cache.Cache, ttl time.Duration, scorer *scoring.Scorer, log *slog.Logger) *Service {
	return &Service{repo: repo, cache: c, ttl: ttl, scorer: scorer, log: log}
}

// Search validates q and returns matching contents, serving from cache when possible.
// Concurrent identical queries on a cache miss share a single database query.
// Cache failures never fail the request; the database is the source of truth.
func (s *Service) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, CacheStatus, error) {
	if err := q.Normalize(); err != nil {
		return domain.SearchResult{}, "", err
	}

	key, err := s.cacheKey(ctx, q)
	if err != nil {
		s.log.WarnContext(ctx, "cache unavailable, bypassing", "error", err)
		res, err := s.repo.Search(ctx, q)
		return res, CacheBypass, err
	}

	if data, ok, err := s.cache.Get(ctx, key); err != nil {
		s.log.WarnContext(ctx, "cache get failed", "error", err)
	} else if ok {
		var res domain.SearchResult
		if err := json.Unmarshal(data, &res); err == nil {
			return res, CacheHit, nil
		}
		s.log.WarnContext(ctx, "discarding undecodable cache entry", "key", key)
	}

	v, err, _ := s.group.Do(key, func() (any, error) {
		// The shared query must not be cancelled just because the first caller went away.
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queryTimeout)
		defer cancel()

		res, err := s.repo.Search(qctx, q)
		if err != nil {
			return domain.SearchResult{}, err
		}
		if data, err := json.Marshal(res); err == nil {
			if err := s.cache.Set(qctx, key, data, s.ttl); err != nil {
				s.log.WarnContext(ctx, "cache set failed", "error", err)
			}
		}
		return res, nil
	})
	if err != nil {
		return domain.SearchResult{}, CacheMiss, err
	}
	return v.(domain.SearchResult), CacheMiss, nil
}

// Get returns a content item with a live breakdown of its score.
func (s *Service) Get(ctx context.Context, id int64) (domain.Content, scoring.Breakdown, error) {
	c, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Content{}, scoring.Breakdown{}, err
	}
	return c, s.scorer.Score(c), nil
}

// Invalidate makes every cached search stale by bumping the global version embedded in cache keys.
// Old entries are never read again and expire through their TTL, so no key scanning is needed.
func (s *Service) Invalidate(ctx context.Context) error {
	if _, err := s.cache.Incr(ctx, versionKey); err != nil {
		return fmt.Errorf("invalidate search cache: %w", err)
	}
	return nil
}

func (s *Service) cacheKey(ctx context.Context, q domain.SearchQuery) (string, error) {
	version := "0"
	data, ok, err := s.cache.Get(ctx, versionKey)
	if err != nil {
		return "", err
	}
	if ok {
		version = string(data)
	}
	return buildKey(version, q), nil
}

// buildKey derives a cache key from a normalized query. Keyword matching is case-insensitive, so the key is too.
func buildKey(version string, q domain.SearchQuery) string {
	raw := strings.Join([]string{
		strings.ToLower(q.Keyword), string(q.Type), q.Provider, string(q.Sort),
		strconv.Itoa(q.Page), strconv.Itoa(q.PerPage),
	}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return "search:v" + version + ":" + hex.EncodeToString(sum[:])
}
