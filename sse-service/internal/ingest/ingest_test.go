package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
)

type fakeProvider struct {
	name     string
	contents []domain.Content
	err      error
	started  chan struct{}
	block    chan struct{}
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Fetch(context.Context) ([]domain.Content, error) {
	if f.block != nil {
		close(f.started)
		<-f.block
	}
	return f.contents, f.err
}

type fakeStore struct {
	mu       sync.Mutex
	upserted []domain.Content
	runs     []domain.SyncRun
}

func (f *fakeStore) UpsertContents(_ context.Context, cs []domain.Content) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, cs...)
	return len(cs), nil
}

func (f *fakeStore) RecalculateScores(context.Context, time.Time, func(domain.Content) float64) (int, error) {
	return 0, nil
}

func (f *fakeStore) RecordSyncRun(_ context.Context, run domain.SyncRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, run)
	return nil
}

func (f *fakeStore) LatestSyncRuns(context.Context) ([]domain.SyncRun, error) { return f.runs, nil }

type countingInvalidator struct{ n int }

func (c *countingInvalidator) Invalidate(context.Context) error { c.n++; return nil }

func newService(store Store, inv Invalidator, providers ...provider.Provider) *Service {
	scorer := scoring.New(scoring.DefaultConfig(), func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })
	return NewService(providers, store, scorer, inv, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSyncScoresAndStoresContent(t *testing.T) {
	video := domain.Content{Provider: "p1", ExternalID: "v1", Title: "Go", Type: domain.ContentTypeVideo,
		Metrics: domain.Metrics{Views: 15000, Likes: 1200}, PublishedAt: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)}
	store := &fakeStore{}
	inv := &countingInvalidator{}
	svc := newService(store, inv, &fakeProvider{name: "p1", contents: []domain.Content{video}})

	report, err := svc.Sync(context.Background())

	require.NoError(t, err)
	require.Len(t, store.upserted, 1)
	assert.InDelta(t, 41.3, store.upserted[0].Score, 1e-9)
	assert.False(t, store.upserted[0].ScoreUpdatedAt.IsZero())
	require.Len(t, report.Runs, 1)
	assert.Equal(t, domain.SyncStatusSuccess, report.Runs[0].Status)
	assert.Equal(t, 1, report.Runs[0].Upserted)
	assert.Equal(t, 1, inv.n, "cache is invalidated after sync")
}

func TestSyncIsolatesProviderFailures(t *testing.T) {
	ok := &fakeProvider{name: "ok", contents: []domain.Content{{ExternalID: "a", Type: domain.ContentTypeText}}}
	broken := &fakeProvider{name: "broken", err: errors.New("timeout")}
	store := &fakeStore{}
	svc := newService(store, &countingInvalidator{}, broken, ok)

	report, err := svc.Sync(context.Background())

	require.NoError(t, err)
	assert.Len(t, store.upserted, 1)
	assert.Equal(t, domain.SyncStatusFailed, report.Runs[0].Status)
	assert.Equal(t, "timeout", report.Runs[0].Error)
	assert.Equal(t, domain.SyncStatusSuccess, report.Runs[1].Status)
	assert.Len(t, store.runs, 2, "both runs are recorded")
}

func TestSyncRejectsConcurrentRuns(t *testing.T) {
	started, block := make(chan struct{}), make(chan struct{})
	svc := newService(&fakeStore{}, &countingInvalidator{}, &fakeProvider{name: "slow", started: started, block: block})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.Sync(context.Background())
	}()

	<-started
	_, err := svc.Sync(context.Background())
	assert.ErrorIs(t, err, domain.ErrSyncInProgress)

	close(block)
	<-done
}
