package registry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
)

func TestBuildFetchesWithFormatAdapter(t *testing.T) {
	jsonData, err := os.ReadFile("../../../test/testdata/provider1.json")
	require.NoError(t, err)
	xmlData, err := os.ReadFile("../../../test/testdata/provider2.xml")
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed.xml" {
			_, _ = w.Write(xmlData)
			return
		}
		_, _ = w.Write(jsonData)
	}))
	t.Cleanup(srv.Close)

	r := New(httpclient.Config{Timeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, tc := range []struct {
		cfg  domain.Provider
		want string
	}{
		{domain.Provider{ID: 1, Name: "custom-json", URL: srv.URL + "/feed.json", Format: domain.ProviderFormatJSON}, "Go Programming Tutorial"},
		{domain.Provider{ID: 2, Name: "custom-xml", URL: srv.URL + "/feed.xml", Format: domain.ProviderFormatXML}, "Introduction to Docker"},
	} {
		p, err := r.Build(tc.cfg)
		require.NoError(t, err)
		assert.Equal(t, tc.cfg.Name, p.Name())

		contents, err := p.Fetch(context.Background())
		require.NoError(t, err)
		require.NotEmpty(t, contents)
		assert.Equal(t, tc.want, contents[0].Title)
		assert.Equal(t, tc.cfg.Name, contents[0].Provider, "content is attributed to the configured name")
	}
}

func TestBuildUnknownFormat(t *testing.T) {
	r := New(httpclient.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := r.Build(domain.Provider{Name: "x", Format: "csv"})

	assert.Error(t, err)
}

func TestClientIsReusedPerProviderUntilURLChanges(t *testing.T) {
	r := New(httpclient.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := domain.Provider{ID: 1, Name: "p", URL: "http://a", Format: domain.ProviderFormatJSON}

	first := r.client(cfg)
	assert.Same(t, first, r.client(cfg))

	cfg.URL = "http://b"
	assert.NotSame(t, first, r.client(cfg))
}

func TestDetect(t *testing.T) {
	jsonData, err := os.ReadFile("../../../test/testdata/provider1.json")
	require.NoError(t, err)
	xmlData, err := os.ReadFile("../../../test/testdata/provider2.xml")
	require.NoError(t, err)

	bodies := map[string][]byte{
		"/json":       jsonData,
		"/xml":        xmlData,
		"/padded-xml": append([]byte("\xef\xbb\xbf\n  "), xmlData...),
		"/other-json": []byte(`{"items":[{"name":"not our schema"}]}`),
		"/html":       []byte(`<!doctype html><html><body>hello</body></html>`),
		"/empty-feed": []byte(`{"contents":[]}`),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	r := New(httpclient.Config{Timeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	for path, want := range map[string]domain.ProviderFormat{
		"/json":       domain.ProviderFormatJSON,
		"/xml":        domain.ProviderFormatXML,
		"/padded-xml": domain.ProviderFormatXML,
	} {
		got, err := r.Detect(ctx, srv.URL+path)
		require.NoError(t, err, path)
		assert.Equal(t, want, got, path)
	}

	for _, path := range []string{"/other-json", "/html", "/empty-feed"} {
		_, err := r.Detect(ctx, srv.URL+path)
		assert.ErrorIs(t, err, domain.ErrFormatNotDetected, path)
		assert.ErrorIs(t, err, domain.ErrInvalidInput, path)
	}
	_, err = r.Detect(ctx, srv.URL+"/missing")
	assert.ErrorIs(t, err, domain.ErrProviderUnreachable)
}

func TestBreakerPerProviderAndURL(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(down.Close)
	r := New(httpclient.Config{Timeout: time.Second, BreakerFailureThreshold: 1, BreakerOpenTimeout: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	fetch := func(cfg domain.Provider) error {
		p, err := r.Build(cfg)
		require.NoError(t, err)
		_, err = p.Fetch(context.Background())
		return err
	}
	broken := domain.Provider{ID: 1, Name: "broken", URL: down.URL, Format: domain.ProviderFormatJSON}
	other := domain.Provider{ID: 2, Name: "other", URL: down.URL + "/other", Format: domain.ProviderFormatJSON}

	require.Error(t, fetch(broken))
	assert.ErrorIs(t, fetch(broken), httpclient.ErrCircuitOpen, "the breaker survives across builds")
	assert.Equal(t, map[string]httpclient.BreakerState{"broken": httpclient.BreakerOpen}, r.BreakerStates())

	assert.NotErrorIs(t, fetch(other), httpclient.ErrCircuitOpen, "each provider has its own breaker")

	fixed := broken
	fixed.URL = down.URL + "/fixed"
	assert.NotErrorIs(t, fetch(fixed), httpclient.ErrCircuitOpen, "a new URL starts with a closed circuit")

	_, err := r.Detect(context.Background(), down.URL)
	assert.NotErrorIs(t, err, httpclient.ErrCircuitOpen, "detection never uses a breaker")
}
