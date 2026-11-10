// Package telemetry records sse-service's domain metrics: how searches are served, how syncs
// go, how much content is stored and whether provider circuits are open. It wraps the
// components it observes instead of changing them.
package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/search"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
)

type Metrics struct {
	searches     *prometheus.CounterVec
	syncRuns     *prometheus.CounterVec
	syncDuration *prometheus.HistogramVec
	upserted     *prometheus.CounterVec
}

func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		searches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "search_requests_total",
			Help: "Searches by how they were served: HIT (cache), MISS (database, then cached), BYPASS (cache unavailable).",
		}, []string{"cache"}),
		syncRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sync_runs_total",
			Help: "Provider sync runs by provider and status.",
		}, []string{"provider", "status"}),
		syncDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sync_duration_seconds",
			Help:    "Duration of a provider sync run.",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"provider"}),
		upserted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sync_items_upserted_total",
			Help: "Content items written by sync runs, by provider.",
		}, []string{"provider"}),
	}
	reg.MustRegister(m.searches, m.syncRuns, m.syncDuration, m.upserted)
	// Start at zero: a counter that first appears with value 1 shows no increase.
	for _, status := range []search.CacheStatus{search.CacheHit, search.CacheMiss, search.CacheBypass} {
		m.searches.WithLabelValues(string(status))
	}
	return m
}

// Searcher is the search use case as consumed by the HTTP handler.
type Searcher interface {
	Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, search.CacheStatus, error)
	Get(ctx context.Context, id int64) (domain.Content, scoring.Breakdown, error)
}

// InstrumentSearcher counts searches by cache status.
func (m *Metrics) InstrumentSearcher(s Searcher) Searcher {
	return &searcher{Searcher: s, m: m}
}

type searcher struct {
	Searcher
	m *Metrics
}

func (s *searcher) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, search.CacheStatus, error) {
	res, status, err := s.Searcher.Search(ctx, q)
	if err == nil {
		s.m.searches.WithLabelValues(string(status)).Inc()
	}
	return res, status, err
}

// SyncStore is the part of the ingest store that records runs.
type SyncStore interface {
	UpsertContents(ctx context.Context, contents []domain.Content) (int, error)
	RecalculateScores(ctx context.Context, before time.Time, score func(domain.Content) float64) (int, error)
	RecordSyncRun(ctx context.Context, run domain.SyncRun) error
	LatestSyncRuns(ctx context.Context) ([]domain.SyncRun, error)
}

// InstrumentSyncStore records every sync run as it is stored, whether it was scheduled or
// triggered by an admin.
func (m *Metrics) InstrumentSyncStore(s SyncStore) SyncStore {
	return &syncStore{SyncStore: s, m: m}
}

type syncStore struct {
	SyncStore
	m *Metrics
}

func (s *syncStore) RecordSyncRun(ctx context.Context, run domain.SyncRun) error {
	s.m.syncRuns.WithLabelValues(run.Provider, string(run.Status)).Inc()
	s.m.syncDuration.WithLabelValues(run.Provider).Observe(run.FinishedAt.Sub(run.StartedAt).Seconds())
	s.m.upserted.WithLabelValues(run.Provider).Add(float64(run.Upserted))
	return s.SyncStore.RecordSyncRun(ctx, run)
}

// StateSource reads the current state reported at scrape time.
type StateSource struct {
	Providers func(ctx context.Context) ([]domain.Provider, error)
	Contents  func(ctx context.Context) ([]domain.ContentCount, error)
	Breakers  func() map[string]httpclient.BreakerState
	Log       *slog.Logger
}

// RegisterState exports gauges that are read when Prometheus scrapes, so they are always
// current: stored contents per provider and type, and each provider's enabled flag and
// circuit state. Providers never called yet report a closed circuit.
func RegisterState(reg prometheus.Registerer, src StateSource) {
	reg.MustRegister(&stateCollector{src: src})
}

var (
	contentsDesc = prometheus.NewDesc("contents_stored",
		"Content items stored, by provider and type.", []string{"provider", "type"}, nil)
	enabledDesc = prometheus.NewDesc("provider_enabled",
		"1 if the provider is enabled, 0 if disabled.", []string{"provider"}, nil)
	circuitDesc = prometheus.NewDesc("provider_circuit_state",
		"Circuit breaker state per provider: 0 closed, 1 half-open, 2 open.", []string{"provider"}, nil)
)

type stateCollector struct{ src StateSource }

func (c *stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- contentsDesc
	ch <- enabledDesc
	ch <- circuitDesc
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if counts, err := c.src.Contents(ctx); err != nil {
		c.src.Log.Warn("metrics: counting contents failed", "error", err)
	} else {
		for _, n := range counts {
			ch <- prometheus.MustNewConstMetric(contentsDesc, prometheus.GaugeValue, float64(n.Count), n.Provider, string(n.Type))
		}
	}

	providers, err := c.src.Providers(ctx)
	if err != nil {
		c.src.Log.Warn("metrics: listing providers failed", "error", err)
		return
	}
	states := c.src.Breakers()
	for _, p := range providers {
		enabled := 0.0
		if p.Enabled {
			enabled = 1
		}
		ch <- prometheus.MustNewConstMetric(enabledDesc, prometheus.GaugeValue, enabled, p.Name)
		ch <- prometheus.MustNewConstMetric(circuitDesc, prometheus.GaugeValue, float64(states[p.Name]), p.Name)
	}
}
