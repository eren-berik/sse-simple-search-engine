package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/ingest"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/providers"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/service/search"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/jwtauth/jwtauthtest"
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
	c := domain.Content{ID: 1, Title: "Go", Type: domain.ContentTypeVideo, Metrics: domain.Metrics{Views: 15000, Likes: 1200},
		PublishedAt: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)}
	now := func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
	return c, scoring.New(scoring.DefaultConfig(), now).Score(c), nil
}

type fakeSyncer struct{ err error }

func (f fakeSyncer) Sync(context.Context) (ingest.Report, error) { return ingest.Report{}, f.err }
func (f fakeSyncer) SyncProvider(context.Context, int64) (ingest.Report, error) {
	return ingest.Report{}, f.err
}
func (f fakeSyncer) Status(context.Context) ([]domain.SyncRun, error) { return nil, nil }

type fakeProviders struct{}

func (fakeProviders) List(context.Context) ([]domain.Provider, error) {
	return []domain.Provider{{ID: 1, Name: "provider1", URL: "https://x.io", Format: domain.ProviderFormatJSON, Enabled: true}}, nil
}

func (fakeProviders) Create(_ context.Context, in providers.CreateInput) (domain.Provider, error) {
	if in.Name == "provider1" {
		return domain.Provider{}, domain.ErrConflict
	}
	if in.Name == "undetectable" {
		return domain.Provider{}, domain.ErrFormatNotDetected
	}
	return domain.Provider{ID: 2, Name: in.Name, URL: in.URL, Format: domain.ProviderFormat(in.Format), Enabled: true}, nil
}

func (fakeProviders) Update(_ context.Context, id int64, in providers.UpdateInput) (domain.Provider, error) {
	if id != 1 {
		return domain.Provider{}, domain.ErrNotFound
	}
	p := domain.Provider{ID: 1, Name: "provider1"}
	if in.Name != nil {
		p.Name = *in.Name
	}
	return p, nil
}

func (fakeProviders) Delete(_ context.Context, id int64) error {
	if id != 1 {
		return domain.ErrNotFound
	}
	return nil
}

type testApp struct {
	*fiber.App
	t      *testing.T
	viewer string
	admin  string
}

func newApp(t *testing.T, searcher *fakeSearcher, syncer fakeSyncer) *testApp {
	t.Helper()
	if searcher == nil {
		searcher = &fakeSearcher{}
	}
	iss := jwtauthtest.New(t)
	app := New(Deps{
		Contents:  handler.NewContentHandler(searcher),
		Sync:      handler.NewSyncHandler(syncer),
		Providers: handler.NewProviderHandler(fakeProviders{}),
		Verifier:  iss.Verifier(),
		Ready:     httpx.Ready(nil, nil),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &testApp{App: app, t: t, viewer: iss.Token(t, jwtauth.RoleViewer), admin: iss.Token(t, jwtauth.RoleAdmin)}
}

// call sends a request with the given bearer token (empty for none) and decodes the JSON body.
func (a *testApp) call(method, url, token, body string) (*http.Response, map[string]any) {
	a.t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.Test(req, -1)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	var parsed map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &parsed)
	return resp, parsed
}

func (a *testApp) get(url string) (*http.Response, map[string]any) {
	return a.call(http.MethodGet, url, a.viewer, "")
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestAuthorization(t *testing.T) {
	app := newApp(t, nil, fakeSyncer{})

	tests := []struct {
		method, path, body string
		anonymous          int
		viewer             int
		admin              int
	}{
		{http.MethodGet, "/api/v1/contents", "", 401, 200, 200},
		{http.MethodGet, "/api/v1/contents/1", "", 401, 200, 200},
		{http.MethodGet, "/api/v1/providers", "", 401, 200, 200},
		{http.MethodGet, "/api/v1/sync/runs", "", 401, 200, 200},
		{http.MethodPost, "/api/v1/providers", `{"name":"feed","url":"https://x.io","format":"json"}`, 401, 403, 201},
		{http.MethodPatch, "/api/v1/providers/1", `{"enabled":false}`, 401, 403, 200},
		{http.MethodDelete, "/api/v1/providers/1", "", 401, 403, 204},
		{http.MethodPost, "/api/v1/providers/1/sync", "", 401, 403, 200},
		{http.MethodPost, "/api/v1/sync", "", 401, 403, 200},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp, _ := app.call(tt.method, tt.path, "", tt.body)
			assert.Equal(t, tt.anonymous, resp.StatusCode, "anonymous")
			resp, _ = app.call(tt.method, tt.path, app.viewer, tt.body)
			assert.Equal(t, tt.viewer, resp.StatusCode, "viewer")
			resp, _ = app.call(tt.method, tt.path, app.admin, tt.body)
			assert.Equal(t, tt.admin, resp.StatusCode, "admin")
		})
	}

	resp, body := app.call(http.MethodGet, "/api/v1/contents", "forged.token.value", "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "invalid_token", errorCode(body))
}

func TestListContents(t *testing.T) {
	s := &fakeSearcher{}
	app := newApp(t, s, fakeSyncer{})

	resp, body := app.get("/api/v1/contents?q=go&type=video&provider=Provider1&page=2&per_page=10")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "HIT", resp.Header.Get("X-Cache"))
	assert.Equal(t, domain.SearchQuery{Keyword: "go", Type: domain.ContentTypeVideo, Provider: "provider1", Sort: domain.SortRelevance, Page: 2, PerPage: 10}, s.lastQuery)

	items := body["data"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	assert.Equal(t, 41.3, item["score"], "score is rounded to 2 decimals")
	assert.Equal(t, 0.1235, item["relevance"], "relevance is present when sorted by relevance")

	meta := body["meta"].(map[string]any)
	assert.Equal(t, float64(3), meta["total_pages"])
	assert.Equal(t, "relevance", meta["sort"])

	_, body = app.get("/api/v1/contents?sort=popularity")
	assert.NotContains(t, body["data"].([]any)[0].(map[string]any), "relevance")

	resp, _ = app.get("/api/v1/contents?tag=DevOps&published_from=2024-03-01&published_to=2024-03-31")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "devops", s.lastQuery.Tag)
	assert.Equal(t, time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), s.lastQuery.PublishedFrom)
	assert.Equal(t, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), s.lastQuery.PublishedTo)
}

func TestListDateValidation(t *testing.T) {
	app := newApp(t, &fakeSearcher{}, fakeSyncer{})

	for _, query := range []string{
		"published_from=14.03.2024",
		"published_to=2024-02-30",
		"published_from=2024-03-15&published_to=2024-03-14",
	} {
		resp, body := app.get("/api/v1/contents?" + query)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, query)
		assert.Equal(t, "invalid_input", errorCode(body), query)
	}
}

func TestListValidation(t *testing.T) {
	app := newApp(t, nil, fakeSyncer{})

	for _, url := range []string{
		"/api/v1/contents?type=podcast",
		"/api/v1/contents?sort=date",
		"/api/v1/contents?page=abc",
		"/api/v1/contents?per_page=1000",
	} {
		resp, body := app.get(url)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, url)
		assert.Equal(t, "invalid_input", errorCode(body), url)
	}
}

func TestListHidesInternalErrors(t *testing.T) {
	app := newApp(t, &fakeSearcher{err: errors.New("pq: connection refused")}, fakeSyncer{})

	resp, body := app.get("/api/v1/contents")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "internal_error", errorCode(body))
	assert.NotContains(t, body["error"].(map[string]any)["message"], "pq")
}

func TestGetContent(t *testing.T) {
	app := newApp(t, nil, fakeSyncer{})

	resp, body := app.get("/api/v1/contents/1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	breakdown := body["data"].(map[string]any)["score_breakdown"].(map[string]any)
	assert.Equal(t, 41.3, breakdown["final"])
	// The explanation survives the handler's rounding.
	explanation := breakdown["explanation"].(map[string]any)
	terms := explanation["base_terms"].([]any)
	require.Len(t, terms, 2)
	assert.Equal(t, map[string]any{"metric": "views", "value": 15000.0, "divisor": 1000.0, "result": 15.0}, terms[0])
	assert.Equal(t, 0.8, explanation["engagement"].(map[string]any)["result"])
	assert.Equal(t, -1.0, explanation["recency"].(map[string]any)["matched_tier"])

	resp, body = app.get("/api/v1/contents/2")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "not_found", errorCode(body))

	resp, _ = app.get("/api/v1/contents/abc")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestProviderManagement(t *testing.T) {
	app := newApp(t, nil, fakeSyncer{})

	resp, body := app.call(http.MethodPost, "/api/v1/providers", app.admin, `{"name":"feed","url":"https://x.io","format":"json"}`)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "feed", body["data"].(map[string]any)["name"])

	resp, body = app.call(http.MethodPost, "/api/v1/providers", app.admin, `{"name":"provider1","url":"https://x.io","format":"json"}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "conflict", errorCode(body))

	resp, body = app.call(http.MethodPost, "/api/v1/providers", app.admin, `{"name":"undetectable","url":"https://x.io"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "format_not_detected", errorCode(body))

	resp, _ = app.call(http.MethodPost, "/api/v1/providers", app.admin, `not json`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp, _ = app.call(http.MethodPatch, "/api/v1/providers/9", app.admin, `{"enabled":false}`)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, body = app.call(http.MethodPatch, "/api/v1/providers/1", app.admin, `{"name":"renamed","url":"https://y.io"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "renamed", body["data"].(map[string]any)["name"])

	resp, _ = app.call(http.MethodDelete, "/api/v1/providers/9", app.admin, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestSyncConflict(t *testing.T) {
	app := newApp(t, nil, fakeSyncer{err: domain.ErrSyncInProgress})

	resp, body := app.call(http.MethodPost, "/api/v1/sync", app.admin, "")

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "sync_in_progress", errorCode(body))
}

func TestPublicEndpoints(t *testing.T) {
	app := newApp(t, nil, fakeSyncer{})

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, _ := app.call(http.MethodGet, path, "", "")
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}

	resp, body := app.call(http.MethodGet, "/does-not-exist", "", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "not_found", errorCode(body))
}
