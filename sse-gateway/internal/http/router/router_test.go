package router

import (
	"encoding/json"
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

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth/jwtauthtest"
)

// recordedRequest is what an upstream saw.
type recordedRequest struct {
	Service   string `json:"service"`
	Method    string `json:"method"`
	URI       string `json:"uri"`
	Body      string `json:"body"`
	Auth      string `json:"auth"`
	Forwarded string `json:"forwarded"`
	RequestID string `json:"request_id"`
}

// echoUpstream answers with a description of the request it received.
func echoUpstream(t *testing.T, name string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_ = json.NewEncoder(w).Encode(recordedRequest{
			Service: name, Method: r.Method, URI: r.URL.RequestURI(), Body: string(body),
			Auth: r.Header.Get("Authorization"), Forwarded: r.Header.Get("X-Forwarded-For"),
			RequestID: r.Header.Get("X-Request-ID"),
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type options struct {
	iam, sse  string
	authLimit int
}

func newApp(t *testing.T, o options) *fiber.App {
	t.Helper()
	if o.authLimit == 0 {
		o.authLimit = 1000
	}
	return New(Deps{
		IAMURL: o.iam, SSEURL: o.sse, UpstreamTimeout: 2 * time.Second,
		Ready: httpx.Ready(nil, nil), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Verifier:     jwtauthtest.New(t).Verifier(),
		RateLimitMax: 1000, RateLimitWindow: time.Minute,
		AuthRateLimitMax: o.authLimit, AuthRateLimitWindow: time.Minute,
	})
}

// withToken returns req carrying an access token for role.
func withToken(t *testing.T, req *http.Request, role jwtauth.Role) *http.Request {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+jwtauthtest.New(t).Token(t, role))
	return req
}

func send(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, recordedRequest, map[string]any) {
	t.Helper()
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var rec recordedRequest
	var generic map[string]any
	_ = json.Unmarshal(raw, &rec)
	_ = json.Unmarshal(raw, &generic)
	return resp, rec, generic
}

func TestRoutesToOwningService(t *testing.T) {
	app := newApp(t, options{iam: echoUpstream(t, "iam"), sse: echoUpstream(t, "sse")})

	tests := []struct{ method, path, service string }{
		{http.MethodPost, "/api/v1/auth/login", "iam"},
		{http.MethodPost, "/api/v1/auth/register", "iam"},
		{http.MethodGet, "/api/v1/auth/me", "iam"},
		{http.MethodGet, "/api/v1/contents?q=go&type=video", "sse"},
		{http.MethodGet, "/api/v1/contents/12", "sse"},
		{http.MethodGet, "/api/v1/providers", "sse"},
		{http.MethodPatch, "/api/v1/providers/3", "sse"},
		{http.MethodPost, "/api/v1/providers/3/sync", "sse"},
		{http.MethodPost, "/api/v1/sync", "sse"},
		{http.MethodGet, "/api/v1/sync/runs", "sse"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp, rec, _ := send(t, app, withToken(t, httptest.NewRequest(tt.method, tt.path, nil), jwtauth.RoleAdmin))

			assert.Equal(t, http.StatusTeapot, resp.StatusCode, "upstream status is passed through")
			assert.Equal(t, tt.service, rec.Service)
			assert.Equal(t, tt.method, rec.Method)
			assert.Equal(t, tt.path, rec.URI, "path and query are preserved")
		})
	}
}

func TestForwardsBodyAndHeaders(t *testing.T) {
	app := newApp(t, options{iam: echoUpstream(t, "iam"), sse: echoUpstream(t, "sse")})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers", strings.NewReader(`{"name":"feed"}`))
	req.Header.Set("Content-Type", "application/json")
	token := jwtauthtest.New(t).Token(t, jwtauth.RoleAdmin)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Request-ID", "req-42")
	resp, rec, _ := send(t, app, req)

	assert.Equal(t, `{"name":"feed"}`, rec.Body)
	assert.Equal(t, "Bearer "+token, rec.Auth, "the token reaches the service, which verifies it again")
	assert.NotEqual(t, "6.6.6.6", rec.Forwarded, "client supplied X-Forwarded-For is overwritten")
	assert.Equal(t, "req-42", rec.RequestID, "request id is propagated")
	assert.Equal(t, "req-42", resp.Header.Get("X-Request-ID"))
	assert.Empty(t, resp.Header.Get("Server"))
}

func TestEdgeAuthorization(t *testing.T) {
	app := newApp(t, options{iam: echoUpstream(t, "iam"), sse: echoUpstream(t, "sse")})

	tests := []struct {
		method, path string
		role         jwtauth.Role // empty: no token
		want         int
	}{
		// Public endpoints: a client has no token yet.
		{http.MethodPost, "/api/v1/auth/login", "", http.StatusTeapot},
		{http.MethodPost, "/api/v1/auth/register", "", http.StatusTeapot},
		{http.MethodPost, "/api/v1/auth/refresh", "", http.StatusTeapot},
		{http.MethodPost, "/api/v1/auth/logout", "", http.StatusTeapot},
		// Everything else needs a valid token.
		{http.MethodGet, "/api/v1/auth/me", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/contents", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/providers", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/v1/sync", "", http.StatusUnauthorized},
		// Viewers read; writes are for admins.
		{http.MethodGet, "/api/v1/auth/me", jwtauth.RoleViewer, http.StatusTeapot},
		{http.MethodGet, "/api/v1/contents/1", jwtauth.RoleViewer, http.StatusTeapot},
		{http.MethodGet, "/api/v1/providers", jwtauth.RoleViewer, http.StatusTeapot},
		{http.MethodGet, "/api/v1/sync/runs", jwtauth.RoleViewer, http.StatusTeapot},
		{http.MethodPost, "/api/v1/providers", jwtauth.RoleViewer, http.StatusForbidden},
		{http.MethodPatch, "/api/v1/providers/1", jwtauth.RoleViewer, http.StatusForbidden},
		{http.MethodDelete, "/api/v1/providers/1", jwtauth.RoleViewer, http.StatusForbidden},
		{http.MethodPost, "/api/v1/providers/1/sync", jwtauth.RoleViewer, http.StatusForbidden},
		{http.MethodPost, "/api/v1/sync", jwtauth.RoleViewer, http.StatusForbidden},
		{http.MethodPost, "/api/v1/providers", jwtauth.RoleAdmin, http.StatusTeapot},
		{http.MethodDelete, "/api/v1/providers/1", jwtauth.RoleAdmin, http.StatusTeapot},
		{http.MethodPost, "/api/v1/sync", jwtauth.RoleAdmin, http.StatusTeapot},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path+" as "+string(tt.role), func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.role != "" {
				req = withToken(t, req, tt.role)
			}
			resp, rec, _ := send(t, app, req)

			assert.Equal(t, tt.want, resp.StatusCode)
			if tt.want != http.StatusTeapot {
				assert.Empty(t, rec.Service, "rejected requests never reach a service")
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/contents", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	resp, _, body := send(t, app, req)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "invalid_token", body["error"].(map[string]any)["code"])
}

func TestUnknownAPIPathIsNotForwarded(t *testing.T) {
	app := newApp(t, options{iam: echoUpstream(t, "iam"), sse: echoUpstream(t, "sse")})

	for _, path := range []string{"/api/v1/admin", "/api/v1/contentsx", "/api/v2/contents"} {
		resp, rec, _ := send(t, app, withToken(t, httptest.NewRequest(http.MethodGet, path, nil), jwtauth.RoleAdmin))
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
		assert.Empty(t, rec.Service, path)
	}
}

func TestUpstreamDown(t *testing.T) {
	app := newApp(t, options{iam: "http://127.0.0.1:1", sse: "http://127.0.0.1:1"})

	resp, _, body := send(t, app, withToken(t, httptest.NewRequest(http.MethodGet, "/api/v1/contents", nil), jwtauth.RoleViewer))

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, "bad_gateway", body["error"].(map[string]any)["code"])
}

func TestAuthRateLimit(t *testing.T) {
	app := newApp(t, options{iam: echoUpstream(t, "iam"), sse: echoUpstream(t, "sse"), authLimit: 2})

	login := func() int {
		resp, _, _ := send(t, app, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil))
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusTeapot, login())
	assert.Equal(t, http.StatusTeapot, login())
	assert.Equal(t, http.StatusTooManyRequests, login())

	resp, _, _ := send(t, app, withToken(t, httptest.NewRequest(http.MethodGet, "/api/v1/contents", nil), jwtauth.RoleViewer))
	assert.Equal(t, http.StatusTeapot, resp.StatusCode, "other endpoints use the general limit")
}

func TestServesDashboardAndDocs(t *testing.T) {
	app := newApp(t, options{iam: echoUpstream(t, "iam"), sse: echoUpstream(t, "sse")})

	for _, path := range []string{"/", "/app.js", "/openapi.yaml", "/healthz"} {
		resp, _, _ := send(t, app, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
}
