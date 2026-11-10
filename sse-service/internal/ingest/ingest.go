// Package ingest pulls content from providers, scores it and stores it.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
)

// Store persists ingested content.
type Store interface {
	UpsertContents(ctx context.Context, contents []domain.Content) (int, error)
	RecalculateScores(ctx context.Context, before time.Time, score func(domain.Content) float64) (int, error)
	// MarkRemoved hides the provider's items that are not in keep.
	MarkRemoved(ctx context.Context, provider string, keep []string, now time.Time) (int, error)
	RecordSyncRun(ctx context.Context, run domain.SyncRun) error
	LatestSyncRuns(ctx context.Context) ([]domain.SyncRun, error)
}

// ProviderSource lists the configured providers.
type ProviderSource interface {
	ListEnabled(ctx context.Context) ([]domain.Provider, error)
	Get(ctx context.Context, id int64) (domain.Provider, error)
}

// Builder turns a provider configuration into a runnable provider.
type Builder interface {
	Build(cfg domain.Provider) (provider.Provider, error)
}

// Invalidator is notified when stored content changes so derived caches can be dropped.
type Invalidator interface {
	Invalidate(ctx context.Context) error
}

// changes counts the rows a sync wrote: inserted or changed items, removed items and rescored items.
func (r Report) changes() int {
	n := r.Rescored
	for _, run := range r.Runs {
		n += run.Upserted + run.Removed
	}
	return n
}

// Report summarizes a sync.
type Report struct {
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Runs       []domain.SyncRun `json:"runs"`
	Rescored   int              `json:"rescored"`
}

type Service struct {
	source      ProviderSource
	builder     Builder
	store       Store
	scorer      *scoring.Scorer
	invalidator Invalidator
	log         *slog.Logger
	now         func() time.Time
	running     sync.Mutex
}

func NewService(source ProviderSource, builder Builder, store Store, scorer *scoring.Scorer, inv Invalidator, log *slog.Logger) *Service {
	return &Service{source: source, builder: builder, store: store, scorer: scorer, invalidator: inv, log: log, now: time.Now}
}

// Run syncs immediately and then on every interval until ctx is cancelled.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if _, err := s.Sync(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("scheduled sync skipped", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sync fetches every enabled provider. Providers are read from storage on each run,
// so providers added or changed at runtime are picked up without a restart.
func (s *Service) Sync(ctx context.Context) (Report, error) {
	if !s.running.TryLock() {
		return Report{}, domain.ErrSyncInProgress
	}
	defer s.running.Unlock()

	configs, err := s.source.ListEnabled(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list providers: %w", err)
	}
	return s.sync(ctx, configs), nil
}

// SyncProvider fetches a single provider. Disabled providers are rejected.
func (s *Service) SyncProvider(ctx context.Context, id int64) (Report, error) {
	if !s.running.TryLock() {
		return Report{}, domain.ErrSyncInProgress
	}
	defer s.running.Unlock()

	cfg, err := s.source.Get(ctx, id)
	if err != nil {
		return Report{}, err
	}
	if !cfg.Enabled {
		return Report{}, fmt.Errorf("%w: provider %q is disabled", domain.ErrInvalidInput, cfg.Name)
	}
	return s.sync(ctx, []domain.Provider{cfg}), nil
}

// sync fetches providers concurrently. One failing provider does not prevent the others from being stored.
func (s *Service) sync(ctx context.Context, configs []domain.Provider) Report {
	report := Report{StartedAt: s.now(), Runs: make([]domain.SyncRun, len(configs))}

	var wg sync.WaitGroup
	for i, cfg := range configs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report.Runs[i] = s.syncProvider(ctx, cfg)
		}()
	}
	wg.Wait()

	// Recency depends on time, so refresh scores of items the providers did not return this round.
	rescored, err := s.store.RecalculateScores(ctx, report.StartedAt, s.scorer.Final)
	if err != nil {
		s.log.Error("recalculating scores failed", "error", err)
	}
	report.Rescored = rescored

	// Cached searches only go stale when this sync changed what they would return.
	if changed := report.changes(); changed > 0 {
		if err := s.invalidator.Invalidate(ctx); err != nil {
			s.log.Warn("cache invalidation failed; entries will expire via TTL", "error", err)
		}
	} else {
		s.log.Info("sync changed nothing; cached searches are kept")
	}

	report.FinishedAt = s.now()
	s.log.Info("sync finished", "providers", len(configs), "duration", report.FinishedAt.Sub(report.StartedAt), "rescored", rescored)
	return report
}

func (s *Service) syncProvider(ctx context.Context, cfg domain.Provider) domain.SyncRun {
	run := domain.SyncRun{Provider: cfg.Name, StartedAt: s.now(), Status: domain.SyncStatusSuccess}
	log := s.log.With("provider", cfg.Name)

	p, err := s.builder.Build(cfg)
	var feed provider.Feed
	if err == nil {
		feed, err = p.Fetch(ctx)
	}
	contents := feed.Contents
	if err == nil {
		run.Fetched = len(contents)
		scoredAt := s.now()
		for i := range contents {
			contents[i].Score = s.scorer.Final(contents[i])
			contents[i].ScoreUpdatedAt = scoredAt
		}
		run.Upserted, err = s.store.UpsertContents(ctx, contents)
	}
	if err == nil {
		run.Removed, err = s.removeVanished(ctx, cfg.Name, feed, log)
	}
	switch {
	case errors.Is(err, httpclient.ErrCircuitOpen):
		run.Status = domain.SyncStatusFailed
		run.Error = err.Error()
		log.Warn("provider skipped: its circuit is open after repeated failures", "error", err)
	case err != nil:
		run.Status = domain.SyncStatusFailed
		run.Error = err.Error()
		log.Error("provider sync failed", "error", err)
	default:
		log.Info("provider synced", "fetched", run.Fetched, "upserted", run.Upserted, "removed", run.Removed)
	}
	run.FinishedAt = s.now()

	// Record the run even if the request context was cancelled mid-sync.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.RecordSyncRun(recordCtx, run); err != nil {
		log.Error("recording sync run failed", "error", err)
	}
	return run
}

// removeVanished hides stored items the provider no longer returns. It only acts on a feed
// known to be whole: when pages are missing or items could not be read, an absent item may
// still exist at the provider. An empty feed is not trusted either, since it usually means a
// broken provider rather than one that deleted everything.
func (s *Service) removeVanished(ctx context.Context, providerName string, feed provider.Feed, log *slog.Logger) (int, error) {
	switch {
	case !feed.Complete:
		log.Info("feed is incomplete; keeping items missing from it")
		return 0, nil
	case feed.Skipped > 0:
		log.Info("some items could not be read; keeping items missing from the feed", "skipped", feed.Skipped)
		return 0, nil
	case len(feed.Contents) == 0:
		log.Warn("provider returned an empty feed; keeping its stored items")
		return 0, nil
	}
	keep := make([]string, len(feed.Contents))
	for i, c := range feed.Contents {
		keep[i] = c.ExternalID
	}
	removed, err := s.store.MarkRemoved(ctx, providerName, keep, s.now())
	if err != nil {
		return 0, err
	}
	if removed > 0 {
		log.Info("items no longer returned by the provider were removed", "removed", removed)
	}
	return removed, nil
}

// Status returns the latest sync run of each provider.
func (s *Service) Status(ctx context.Context) ([]domain.SyncRun, error) {
	return s.store.LatestSyncRuns(ctx)
}
