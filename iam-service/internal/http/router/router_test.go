package router

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth/authtest"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

func newApp(t *testing.T) (*fiber.App, *jwtauth.Verifier) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	key, err := jwtauth.GenerateKey(2048)
	require.NoError(t, err)
	signer := jwtauth.NewSigner(jwtauth.SignerConfig{Key: key, Issuer: "iam", Audience: []string{"sse"}, TTL: 15 * time.Minute})
	verifier := jwtauth.NewVerifier(signer.KeySource(), "iam", "sse")

	svc, err := auth.NewService(authtest.NewMemoryStore(), auth.BcryptHasher{Cost: bcrypt.MinCost}, signer,
		auth.Config{RefreshTokenTTL: time.Hour}, log)
	require.NoError(t, err)

	return New(Deps{
		Auth: handler.NewAuthHandler(svc), Signer: signer, Verifier: verifier,
		Ready: httpx.Ready(nil, nil), Log: log,
	}), verifier
}

type response struct {
	status int
	body   map[string]any
}

func (r response) data() map[string]any { d, _ := r.body["data"].(map[string]any); return d }

func (r response) errorCode() string {
	e, _ := r.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func call(t *testing.T, app *fiber.App, method, path string, body any, token string) response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	return response{status: resp.StatusCode, body: parsed}
}

func TestRegisterLoginRefreshLogout(t *testing.T) {
	app, verifier := newApp(t)

	reg := call(t, app, http.MethodPost, "/api/v1/auth/register",
		map[string]string{"username": "alice", "password": "s3cret-pass", "role": "admin"}, "")
	require.Equal(t, http.StatusCreated, reg.status)
	session := reg.data()
	assert.Equal(t, "Bearer", session["token_type"])
	assert.Equal(t, float64(900), session["expires_in"])
	assert.NotEmpty(t, session["refresh_token"])
	user := session["user"].(map[string]any)
	assert.Equal(t, "admin", user["role"])
	assert.NotContains(t, user, "password_hash")

	claims, err := verifier.Verify(t.Context(), session["access_token"].(string))
	require.NoError(t, err)
	assert.Equal(t, jwtauth.RoleAdmin, claims.Role, "access token carries the role")

	login := call(t, app, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "alice", "password": "s3cret-pass"}, "")
	require.Equal(t, http.StatusOK, login.status)

	me := call(t, app, http.MethodGet, "/api/v1/auth/me", nil, login.data()["access_token"].(string))
	require.Equal(t, http.StatusOK, me.status)
	assert.Equal(t, "alice", me.data()["username"])

	refreshToken := login.data()["refresh_token"].(string)
	refreshed := call(t, app, http.MethodPost, "/api/v1/auth/refresh", map[string]string{"refresh_token": refreshToken}, "")
	require.Equal(t, http.StatusOK, refreshed.status)
	assert.NotEqual(t, refreshToken, refreshed.data()["refresh_token"])

	out := call(t, app, http.MethodPost, "/api/v1/auth/logout", map[string]string{"refresh_token": refreshed.data()["refresh_token"].(string)}, "")
	assert.Equal(t, http.StatusNoContent, out.status)

	again := call(t, app, http.MethodPost, "/api/v1/auth/refresh", map[string]string{"refresh_token": refreshed.data()["refresh_token"].(string)}, "")
	assert.Equal(t, http.StatusUnauthorized, again.status)
	assert.Equal(t, "invalid_refresh_token", again.errorCode())
}

func TestAuthErrors(t *testing.T) {
	app, _ := newApp(t)
	call(t, app, http.MethodPost, "/api/v1/auth/register", map[string]string{"username": "alice", "password": "s3cret-pass"}, "")

	tests := []struct {
		name   string
		path   string
		body   any
		status int
		code   string
	}{
		{"duplicate username", "/api/v1/auth/register", map[string]string{"username": "alice", "password": "s3cret-pass"}, 409, "username_taken"},
		{"weak password", "/api/v1/auth/register", map[string]string{"username": "bob", "password": "123"}, 400, "invalid_input"},
		{"unknown role", "/api/v1/auth/register", map[string]string{"username": "bob", "password": "s3cret-pass", "role": "root"}, 400, "invalid_input"},
		{"bad json", "/api/v1/auth/login", "not-an-object", 400, "invalid_input"},
		{"wrong password", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "nope-nope"}, 401, "invalid_credentials"},
		{"missing refresh token", "/api/v1/auth/refresh", map[string]string{}, 400, "invalid_input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := call(t, app, http.MethodPost, tt.path, tt.body, "")
			assert.Equal(t, tt.status, r.status)
			assert.Equal(t, tt.code, r.errorCode())
		})
	}

	assert.Equal(t, http.StatusUnauthorized, call(t, app, http.MethodGet, "/api/v1/auth/me", nil, "").status)
	assert.Equal(t, http.StatusUnauthorized, call(t, app, http.MethodGet, "/api/v1/auth/me", nil, "garbage").status)
}

func TestJWKS(t *testing.T) {
	app, _ := newApp(t)

	r := call(t, app, http.MethodGet, "/.well-known/jwks.json", nil, "")

	require.Equal(t, http.StatusOK, r.status)
	keys := r.body["keys"].([]any)
	require.Len(t, keys, 1)
	k := keys[0].(map[string]any)
	assert.Equal(t, "RSA", k["kty"])
	assert.Equal(t, "RS256", k["alg"])
	assert.NotEmpty(t, k["kid"])
	assert.NotContains(t, k, "d", "private exponent is never published")
}
