// Package ingest pulls content from providers, scores it and stores it.
package ingest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
)

// Store persists ingested content.
type Store interface {
	UpsertContents(ctx context.Context, contents []domain.Content) (int, error)
	RecalculateScores(ctx context.Context, before time.Time, score func(domain.Content) float64) (int, error)
	RecordSyncRun(ctx context.Context, run domain.SyncRun) error
	LatestSyncRuns(ctx context.Context) ([]domain.SyncRun, error)
}

// Invalidator is notified when stored content changes so derived caches can be dropped.
type Invalidator interface {
	Invalidate(ctx context.Context) error
}

// Report summarizes a sync across all providers.
type Report struct {
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Runs       []domain.SyncRun `json:"runs"`
	Rescored   int              `json:"rescored"`
}

type Service struct {
	providers   []provider.Provider
	store       Store
	scorer      *scoring.Scorer
	invalidator Invalidator
	log         *slog.Logger
	now         func() time.Time
	running     sync.Mutex
}

func NewService(providers []provider.Provider, store Store, scorer *scoring.Scorer, inv Invalidator, log *slog.Logger) *Service {
	return &Service{providers: providers, store: store, scorer: scorer, invalidator: inv, log: log, now: time.Now}
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

// Sync fetches all providers concurrently and stores their content.
// Providers are isolated: one failing does not prevent the others from being stored.
// Only one sync runs at a time; a concurrent call returns domain.ErrSyncInProgress.
func (s *Service) Sync(ctx context.Context) (Report, error) {
	if !s.running.TryLock() {
		return Report{}, domain.ErrSyncInProgress
	}
	defer s.running.Unlock()

	report := Report{StartedAt: s.now(), Runs: make([]domain.SyncRun, len(s.providers))}

	var wg sync.WaitGroup
	for i, p := range s.providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report.Runs[i] = s.syncProvider(ctx, p)
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
	s.log.Info("sync finished", "duration", report.FinishedAt.Sub(report.StartedAt), "rescored", rescored)
	return report, nil
}

func (s *Service) syncProvider(ctx context.Context, p provider.Provider) domain.SyncRun {
	run := domain.SyncRun{Provider: p.Name(), StartedAt: s.now(), Status: domain.SyncStatusSuccess}
	log := s.log.With("provider", p.Name())

	contents, err := p.Fetch(ctx)
	if err == nil {
		run.Fetched = len(contents)
		scoredAt := s.now()
		for i := range contents {
			contents[i].Score = s.scorer.Final(contents[i])
			contents[i].ScoreUpdatedAt = scoredAt
		}
		run.Upserted, err = s.store.UpsertContents(ctx, contents)
	}
	if err != nil {
		run.Status = domain.SyncStatusFailed
		run.Error = err.Error()
		log.Error("provider sync failed", "error", err)
	} else {
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
