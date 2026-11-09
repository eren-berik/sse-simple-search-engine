package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/internal/ingest"
	"github.com/eren-berik/sse-simple-search-engine/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/internal/service/search"
)

type fakeSearcher struct {
	lastQuery domain.SearchQuery
	err       error
}

func (f *fakeSearcher) Search(_ context.Context, q domain.SearchQuery) (domain.SearchResult, search.CacheStatus, error) {
	if err := q.Normalize(); err != nil {
		return domain.SearchResult{}, "", err
	}
	f.lastQuery = q
	if f.err != nil {
		return domain.SearchResult{}, "", f.err
	}
	return domain.SearchResult{
		Total: 21, Page: q.Page, PerPage: q.PerPage, Sort: q.Sort,
		Hits: []domain.SearchHit{{
			Content:   domain.Content{ID: 1, Provider: "provider1", ExternalID: "v1", Title: "Go", Type: domain.ContentTypeVideo, Score: 41.29999},
			Relevance: 0.123456,
		}},
	}, search.CacheHit, nil
}

func (f *fakeSearcher) Get(_ context.Context, id int64) (domain.Content, scoring.Breakdown, error) {
	if id != 1 {
		return domain.Content{}, scoring.Breakdown{}, domain.ErrNotFound
	}
	return domain.Content{ID: 1, Title: "Go", Type: domain.ContentTypeVideo}, scoring.Breakdown{Base: 27, TypeCoefficient: 1.5, Engagement: 0.8, Final: 41.3}, nil
}

type fakeSyncer struct{ err error }

func (f fakeSyncer) Sync(context.Context) (ingest.Report, error) { return ingest.Report{}, f.err }
func (f fakeSyncer) Status(context.Context) ([]domain.SyncRun, error) {
	return nil, nil
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type testDeps struct {
	searcher *fakeSearcher
	syncer   fakeSyncer
	db       fakePinger
	cache    fakePinger
	limit    int
}

func newApp(t *testing.T, d testDeps) *fiber.App {
	t.Helper()
	if d.searcher == nil {
		d.searcher = &fakeSearcher{}
	}
	if d.limit == 0 {
		d.limit = 1000
	}
	return New(Deps{
		Contents:        handler.NewContentHandler(d.searcher),
		Sync:            handler.NewSyncHandler(d.syncer),
		Health:          handler.NewHealthHandler(d.db, d.cache),
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminAPIKey:     "secret",
		RateLimitMax:    d.limit,
		RateLimitWindow: time.Minute,
	})
}

func do(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	return resp, body
}

func get(t *testing.T, app *fiber.App, url string) (*http.Response, map[string]any) {
	return do(t, app, httptest.NewRequest(http.MethodGet, url, nil))
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestListContents(t *testing.T) {
	s := &fakeSearcher{}
	app := newApp(t, testDeps{searcher: s})

	resp, body := get(t, app, "/api/v1/contents?q=go&type=video&page=2&per_page=10")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "HIT", resp.Header.Get("X-Cache"))
	assert.Equal(t, domain.SearchQuery{Keyword: "go", Type: domain.ContentTypeVideo, Sort: domain.SortRelevance, Page: 2, PerPage: 10}, s.lastQuery)

	items := body["data"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	assert.Equal(t, "Go", item["title"])
	assert.Equal(t, 41.3, item["score"], "score is rounded to 2 decimals")
	assert.Equal(t, 0.1235, item["relevance"], "relevance is present when sorted by relevance")

	meta := body["meta"].(map[string]any)
	assert.Equal(t, float64(21), meta["total"])
	assert.Equal(t, float64(3), meta["total_pages"])
	assert.Equal(t, "relevance", meta["sort"])
}

func TestListOmitsRelevanceWhenSortedByPopularity(t *testing.T) {
	app := newApp(t, testDeps{})

	_, body := get(t, app, "/api/v1/contents?sort=popularity")

	item := body["data"].([]any)[0].(map[string]any)
	assert.NotContains(t, item, "relevance")
}

func TestListValidation(t *testing.T) {
	app := newApp(t, testDeps{})

	for _, url := range []string{
		"/api/v1/contents?type=podcast",
		"/api/v1/contents?sort=date",
		"/api/v1/contents?page=abc",
		"/api/v1/contents?per_page=1000",
	} {
		resp, body := get(t, app, url)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, url)
		assert.Equal(t, "invalid_input", errorCode(body), url)
	}
}

func TestListHidesInternalErrors(t *testing.T) {
	app := newApp(t, testDeps{searcher: &fakeSearcher{err: errors.New("pq: connection refused")}})

	resp, body := get(t, app, "/api/v1/contents")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "internal_error", errorCode(body))
	assert.NotContains(t, body["error"].(map[string]any)["message"], "pq")
}

func TestGetContent(t *testing.T) {
	app := newApp(t, testDeps{})

	resp, body := get(t, app, "/api/v1/contents/1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	breakdown := body["data"].(map[string]any)["score_breakdown"].(map[string]any)
	assert.Equal(t, 41.3, breakdown["final"])

	resp, body = get(t, app, "/api/v1/contents/2")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "not_found", errorCode(body))

	resp, _ = get(t, app, "/api/v1/contents/abc")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdminSyncRequiresAPIKey(t *testing.T) {
	app := newApp(t, testDeps{})

	resp, body := do(t, app, httptest.NewRequest(http.MethodPost, "/api/v1/admin/sync", nil))
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "unauthorized", errorCode(body))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sync", nil)
	req.Header.Set("X-API-Key", "secret")
	resp, _ = do(t, app, req)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdminSyncConflict(t *testing.T) {
	app := newApp(t, testDeps{syncer: fakeSyncer{err: domain.ErrSyncInProgress}})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sync", nil)
	req.Header.Set("X-API-Key", "secret")
	resp, body := do(t, app, req)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "sync_in_progress", errorCode(body))
}

func TestRateLimit(t *testing.T) {
	app := newApp(t, testDeps{limit: 2})

	for range 2 {
		resp, _ := get(t, app, "/api/v1/contents")
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	resp, body := get(t, app, "/api/v1/contents")
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "rate_limited", errorCode(body))

	resp, _ = get(t, app, "/healthz")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "health checks are not rate limited")
}

func TestReadiness(t *testing.T) {
	resp, body := get(t, newApp(t, testDeps{}), "/readyz")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", body["status"])

	resp, body = get(t, newApp(t, testDeps{cache: fakePinger{err: errors.New("down")}}), "/readyz")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "cache outage keeps the service ready")
	assert.Equal(t, "degraded", body["status"])

	resp, _ = get(t, newApp(t, testDeps{db: fakePinger{err: errors.New("down")}}), "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestServesDashboardAndSpec(t *testing.T) {
	app := newApp(t, testDeps{})

	resp, _ := get(t, app, "/")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	resp, _ = get(t, app, "/openapi.yaml")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body := get(t, app, "/does-not-exist")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "not_found", errorCode(body))
}
