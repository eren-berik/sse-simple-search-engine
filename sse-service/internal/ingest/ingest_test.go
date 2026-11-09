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

// fakeSource serves provider configs; fakeBuilder maps them back to fake providers by name.
type fakeSource struct{ configs []domain.Provider }

func (f fakeSource) ListEnabled(context.Context) ([]domain.Provider, error) {
	var out []domain.Provider
	for _, c := range f.configs {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f fakeSource) Get(_ context.Context, id int64) (domain.Provider, error) {
	for _, c := range f.configs {
		if c.ID == id {
			return c, nil
		}
	}
	return domain.Provider{}, domain.ErrNotFound
}

type fakeBuilder map[string]provider.Provider

func (f fakeBuilder) Build(cfg domain.Provider) (provider.Provider, error) {
	p, ok := f[cfg.Name]
	if !ok {
		return nil, errors.New("no adapter")
	}
	return p, nil
}

func newServiceWith(store Store, inv Invalidator, configs []domain.Provider, builder fakeBuilder) *Service {
	scorer := scoring.New(scoring.DefaultConfig(), func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })
	return NewService(fakeSource{configs: configs}, builder, store, scorer, inv, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newService registers every provider as an enabled config with ids starting at 1.
func newService(store Store, inv Invalidator, providers ...*fakeProvider) *Service {
	builder := fakeBuilder{}
	configs := make([]domain.Provider, 0, len(providers))
	for i, p := range providers {
		builder[p.name] = p
		configs = append(configs, domain.Provider{ID: int64(i + 1), Name: p.name, Enabled: true})
	}
	return newServiceWith(store, inv, configs, builder)
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

func TestSyncSkipsDisabledProviders(t *testing.T) {
	store := &fakeStore{}
	builder := fakeBuilder{
		"on":  &fakeProvider{name: "on", contents: []domain.Content{{ExternalID: "a"}}},
		"off": &fakeProvider{name: "off", contents: []domain.Content{{ExternalID: "b"}}},
	}
	configs := []domain.Provider{{ID: 1, Name: "on", Enabled: true}, {ID: 2, Name: "off", Enabled: false}}
	svc := newServiceWith(store, &countingInvalidator{}, configs, builder)

	report, err := svc.Sync(context.Background())

	require.NoError(t, err)
	require.Len(t, report.Runs, 1)
	assert.Equal(t, "on", report.Runs[0].Provider)
}

func TestSyncProvider(t *testing.T) {
	store := &fakeStore{}
	builder := fakeBuilder{
		"a": &fakeProvider{name: "a", contents: []domain.Content{{ExternalID: "1"}}},
		"b": &fakeProvider{name: "b", contents: []domain.Content{{ExternalID: "2"}}},
	}
	configs := []domain.Provider{{ID: 1, Name: "a", Enabled: true}, {ID: 2, Name: "b", Enabled: true}, {ID: 3, Name: "off", Enabled: false}}
	svc := newServiceWith(store, &countingInvalidator{}, configs, builder)
	ctx := context.Background()

	report, err := svc.SyncProvider(ctx, 2)
	require.NoError(t, err)
	require.Len(t, report.Runs, 1)
	assert.Equal(t, "b", report.Runs[0].Provider)
	assert.Len(t, store.upserted, 1)

	_, err = svc.SyncProvider(ctx, 3)
	assert.ErrorIs(t, err, domain.ErrInvalidInput, "disabled providers cannot be synced")

	_, err = svc.SyncProvider(ctx, 99)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSyncRecordsBuildFailures(t *testing.T) {
	store := &fakeStore{}
	svc := newServiceWith(store, &countingInvalidator{}, []domain.Provider{{ID: 1, Name: "unknown", Enabled: true}}, fakeBuilder{})

	report, err := svc.Sync(context.Background())

	require.NoError(t, err)
	assert.Equal(t, domain.SyncStatusFailed, report.Runs[0].Status)
	assert.Len(t, store.runs, 1)
}
