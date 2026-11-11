package search

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/cache"
)

type fakeRepo struct {
	calls  atomic.Int32
	delay  time.Duration
	result domain.SearchResult
	err    error
}

func (f *fakeRepo) Search(_ context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	res := f.result
	res.Page, res.PerPage, res.Sort = q.Page, q.PerPage, q.Sort
	return res, f.err
}

func (f *fakeRepo) GetByID(_ context.Context, id int64) (domain.Content, error) {
	if id != 1 {
		return domain.Content{}, domain.ErrNotFound
	}
	return domain.Content{ID: 1, Type: domain.ContentTypeVideo, Metrics: domain.Metrics{Views: 1000, Likes: 100}}, nil
}

// failingCache simulates an unreachable Redis.
type failingCache struct{ cache.Noop }

func (failingCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("connection refused")
}

func newService(repo ContentSearcher, c cache.Cache) *Service {
	scorer := scoring.New(scoring.DefaultConfig(), func() time.Time { return time.Unix(0, 0) })
	return NewService(repo, c, time.Minute, scorer, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func sampleResult() domain.SearchResult {
	return domain.SearchResult{
		Total: 1,
		Hits:  []domain.SearchHit{{Content: domain.Content{ID: 7, Title: "Go", Type: domain.ContentTypeVideo, Score: 41.3}, Relevance: 0.5}},
	}
}

func TestSearchCachesResults(t *testing.T) {
	repo := &fakeRepo{result: sampleResult()}
	svc := newService(repo, cache.NewMemory())
	ctx := context.Background()

	first, status, err := svc.Search(ctx, domain.SearchQuery{Keyword: "go"})
	require.NoError(t, err)
	assert.Equal(t, CacheMiss, status)

	second, status, err := svc.Search(ctx, domain.SearchQuery{Keyword: "  GO "})
	require.NoError(t, err)
	assert.Equal(t, CacheHit, status, "keyword is normalized and case-insensitive")
	assert.Equal(t, first.Hits[0].Content.Title, second.Hits[0].Content.Title)
	assert.InDelta(t, 41.3, second.Hits[0].Content.Score, 1e-9)
	assert.Equal(t, int32(1), repo.calls.Load())

	_, status, _ = svc.Search(ctx, domain.SearchQuery{Keyword: "go", Page: 2})
	assert.Equal(t, CacheMiss, status, "different page is a different key")
}

func TestInvalidateDropsCachedResults(t *testing.T) {
	repo := &fakeRepo{result: sampleResult()}
	svc := newService(repo, cache.NewMemory())
	ctx := context.Background()

	_, _, _ = svc.Search(ctx, domain.SearchQuery{})
	require.NoError(t, svc.Invalidate(ctx))
	_, status, err := svc.Search(ctx, domain.SearchQuery{})

	require.NoError(t, err)
	assert.Equal(t, CacheMiss, status)
	assert.Equal(t, int32(2), repo.calls.Load())
}

func TestSearchBypassesBrokenCache(t *testing.T) {
	repo := &fakeRepo{result: sampleResult()}
	svc := newService(repo, failingCache{})

	res, status, err := svc.Search(context.Background(), domain.SearchQuery{})

	require.NoError(t, err)
	assert.Equal(t, CacheBypass, status)
	assert.Equal(t, 1, res.Total)
}

func TestSearchValidatesQuery(t *testing.T) {
	repo := &fakeRepo{}
	svc := newService(repo, cache.NewMemory())

	_, _, err := svc.Search(context.Background(), domain.SearchQuery{Type: "podcast"})

	assert.ErrorIs(t, err, domain.ErrInvalidInput)
	assert.Equal(t, int32(0), repo.calls.Load())
}

func TestSearchDoesNotCacheErrors(t *testing.T) {
	repo := &fakeRepo{err: errors.New("db down")}
	svc := newService(repo, cache.NewMemory())
	ctx := context.Background()

	_, _, err := svc.Search(ctx, domain.SearchQuery{})
	require.Error(t, err)
	_, _, err = svc.Search(ctx, domain.SearchQuery{})
	require.Error(t, err)
	assert.Equal(t, int32(2), repo.calls.Load())
}

func TestConcurrentMissesShareOneQuery(t *testing.T) {
	repo := &fakeRepo{result: sampleResult(), delay: 50 * time.Millisecond}
	svc := newService(repo, cache.NewMemory())

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.Search(context.Background(), domain.SearchQuery{Keyword: "go"})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), repo.calls.Load())
}

func TestGetReturnsBreakdown(t *testing.T) {
	svc := newService(&fakeRepo{}, cache.NewMemory())

	c, b, err := svc.Get(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), c.ID)
	assert.InDelta(t, 2.0, b.Base, 1e-9)

	_, _, err = svc.Get(context.Background(), 2)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCacheKeyCoversEveryFilter(t *testing.T) {
	base := domain.SearchQuery{Sort: domain.SortPopularity, Page: 1, PerPage: 10}
	day := time.Date(2024, 3, 14, 0, 0, 0, 0, time.UTC)
	keys := map[string]string{}
	for name, q := range map[string]domain.SearchQuery{
		"base": base,
		"tag":  func() domain.SearchQuery { q := base; q.Tag = "go"; return q }(),
		"from": func() domain.SearchQuery { q := base; q.PublishedFrom = day; return q }(),
		"to":   func() domain.SearchQuery { q := base; q.PublishedTo = day; return q }(),
	} {
		key := buildKey("1", q)
		for other, k := range keys {
			assert.NotEqual(t, k, key, "%s and %s must not share a cache entry", name, other)
		}
		keys[name] = key
	}
}

func TestInvalidateWarmsTheCommonSearches(t *testing.T) {
	repo := &fakeRepo{result: sampleResult()}
	warm := []domain.SearchQuery{{PerPage: 10}, {Type: domain.ContentTypeVideo, PerPage: 10}}
	svc := newService(repo, cache.NewMemory()).WithWarmQueries(warm)
	ctx := context.Background()

	require.NoError(t, svc.Invalidate(ctx))
	assert.Equal(t, int32(2), repo.calls.Load(), "each warm query is computed once")

	for _, q := range warm {
		_, status, err := svc.Search(ctx, q)
		require.NoError(t, err)
		assert.Equal(t, CacheHit, status, "served from the warmed cache")
	}
	_, status, err := svc.Search(ctx, domain.SearchQuery{Keyword: "go"})
	require.NoError(t, err)
	assert.Equal(t, CacheMiss, status, "other searches are computed on demand")
	assert.Equal(t, int32(3), repo.calls.Load())
}

func TestWarmingFailuresDoNotFailInvalidation(t *testing.T) {
	repo := &fakeRepo{err: errors.New("db down")}
	svc := newService(repo, cache.NewMemory()).WithWarmQueries([]domain.SearchQuery{{}})

	assert.NoError(t, svc.Invalidate(context.Background()))
}

func (f *fakeRepo) ExplainRelevance(_ context.Context, _ int64, keyword string) (domain.RelevanceExplanation, error) {
	return domain.RelevanceExplanation{Query: keyword}, nil
}

func TestExplainRelevanceNormalizesTheKeyword(t *testing.T) {
	svc := newService(&fakeRepo{}, cache.NewMemory())

	e, err := svc.ExplainRelevance(context.Background(), 1, "  go   testing ")
	require.NoError(t, err)
	assert.Equal(t, "go testing", e.Query, "the same keyword the search ranked with")

	_, err = svc.ExplainRelevance(context.Background(), 1, "   ")
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}
