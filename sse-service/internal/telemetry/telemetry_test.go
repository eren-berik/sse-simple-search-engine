package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/search"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeSearcher struct {
	status search.CacheStatus
	err    error
}

func (f fakeSearcher) Search(context.Context, domain.SearchQuery) (domain.SearchResult, search.CacheStatus, error) {
	return domain.SearchResult{}, f.status, f.err
}

func (fakeSearcher) Get(context.Context, int64) (domain.Content, scoring.Breakdown, error) {
	return domain.Content{}, scoring.Breakdown{}, nil
}

func TestSearchesByCacheStatus(t *testing.T) {
	m := New(prometheus.NewRegistry())

	for _, status := range []search.CacheStatus{search.CacheHit, search.CacheHit, search.CacheMiss} {
		_, _, err := m.InstrumentSearcher(fakeSearcher{status: status}).Search(context.Background(), domain.SearchQuery{})
		require.NoError(t, err)
	}
	_, _, _ = m.InstrumentSearcher(fakeSearcher{err: errors.New("boom")}).Search(context.Background(), domain.SearchQuery{})

	assert.Equal(t, 2.0, testutil.ToFloat64(m.searches.WithLabelValues("HIT")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.searches.WithLabelValues("MISS")))
	assert.Equal(t, 0.0, testutil.ToFloat64(m.searches.WithLabelValues("BYPASS")), "failed searches are not counted")
}

type fakeStore struct {
	SyncStore
	recorded int
}

func (f *fakeStore) RecordSyncRun(context.Context, domain.SyncRun) error { f.recorded++; return nil }

func TestSyncRuns(t *testing.T) {
	m := New(prometheus.NewRegistry())
	inner := &fakeStore{}
	store := m.InstrumentSyncStore(inner)
	start := time.Now()

	require.NoError(t, store.RecordSyncRun(context.Background(), domain.SyncRun{
		Provider: "feed", Status: domain.SyncStatusSuccess, Upserted: 4, StartedAt: start, FinishedAt: start.Add(2 * time.Second),
	}))
	require.NoError(t, store.RecordSyncRun(context.Background(), domain.SyncRun{
		Provider: "feed", Status: domain.SyncStatusFailed, StartedAt: start, FinishedAt: start,
	}))

	assert.Equal(t, 2, inner.recorded, "runs are still stored")
	assert.Equal(t, 1.0, testutil.ToFloat64(m.syncRuns.WithLabelValues("feed", "success")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.syncRuns.WithLabelValues("feed", "failed")))
	assert.Equal(t, 4.0, testutil.ToFloat64(m.upserted.WithLabelValues("feed")))
}

func TestStateCollector(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterState(reg, StateSource{
		Providers: func(context.Context) ([]domain.Provider, error) {
			return []domain.Provider{{Name: "feed", Enabled: true}, {Name: "fresh", Enabled: false}}, nil
		},
		Contents: func(context.Context) ([]domain.ContentCount, error) {
			return []domain.ContentCount{{Provider: "feed", Type: domain.ContentTypeVideo, Count: 3}}, nil
		},
		Breakers: func() map[string]httpclient.BreakerState {
			return map[string]httpclient.BreakerState{"feed": httpclient.BreakerOpen}
		},
		Log: discard,
	})

	expected := `
# HELP contents_stored Content items stored, by provider and type.
# TYPE contents_stored gauge
contents_stored{provider="feed",type="video"} 3
# HELP provider_circuit_state Circuit breaker state per provider: 0 closed, 1 half-open, 2 open.
# TYPE provider_circuit_state gauge
provider_circuit_state{provider="feed"} 2
provider_circuit_state{provider="fresh"} 0
# HELP provider_enabled 1 if the provider is enabled, 0 if disabled.
# TYPE provider_enabled gauge
provider_enabled{provider="feed"} 1
provider_enabled{provider="fresh"} 0
`
	assert.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected)))
}

func TestStateCollectorSurvivesDatabaseErrors(t *testing.T) {
	reg := prometheus.NewRegistry()
	fail := errors.New("db down")
	RegisterState(reg, StateSource{
		Providers: func(context.Context) ([]domain.Provider, error) { return nil, fail },
		Contents:  func(context.Context) ([]domain.ContentCount, error) { return nil, fail },
		Breakers:  func() map[string]httpclient.BreakerState { return nil },
		Log:       discard,
	})

	_, err := reg.Gather()
	assert.NoError(t, err, "a scrape still succeeds; the gauges are just missing")
}

func (fakeSearcher) ExplainRelevance(context.Context, int64, string) (domain.RelevanceExplanation, error) {
	return domain.RelevanceExplanation{}, nil
}

func (fakeSearcher) Tags(context.Context) ([]domain.TagCount, search.CacheStatus, error) {
	return nil, search.CacheMiss, nil
}
