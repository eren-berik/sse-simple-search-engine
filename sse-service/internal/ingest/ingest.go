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

	if err := s.invalidator.Invalidate(ctx); err != nil {
		s.log.Warn("cache invalidation failed; entries will expire via TTL", "error", err)
	}

	report.FinishedAt = s.now()
	s.log.Info("sync finished", "providers", len(configs), "duration", report.FinishedAt.Sub(report.StartedAt), "rescored", rescored)
	return report
}

func (s *Service) syncProvider(ctx context.Context, cfg domain.Provider) domain.SyncRun {
	run := domain.SyncRun{Provider: cfg.Name, StartedAt: s.now(), Status: domain.SyncStatusSuccess}
	log := s.log.With("provider", cfg.Name)

	p, err := s.builder.Build(cfg)
	var contents []domain.Content
	if err == nil {
		var feed provider.Feed
		feed, err = p.Fetch(ctx)
		contents = feed.Contents
	}
	if err == nil {
		run.Fetched = len(contents)
		scoredAt := s.now()
		for i := range contents {
			contents[i].Score = s.scorer.Final(contents[i])
			contents[i].ScoreUpdatedAt = scoredAt
		}
		run.Upserted, err = s.store.UpsertContents(ctx, contents)
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
		log.Info("provider synced", "fetched", run.Fetched, "upserted", run.Upserted)
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

// Status returns the latest sync run of each provider.
func (s *Service) Status(ctx context.Context) ([]domain.SyncRun, error) {
	return s.store.LatestSyncRuns(ctx)
}
