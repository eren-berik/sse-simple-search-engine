package jwtauth

import (
	"context"
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/httpx"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		testKey, err = GenerateKey(2048)
		require.NoError(t, err)
	})
	return testKey
}

func newSigner(t *testing.T, now time.Time) *Signer {
	return NewSigner(SignerConfig{
		Key: key(t), Issuer: "iam", Audience: []string{"sse"}, TTL: 15 * time.Minute,
		Now: func() time.Time { return now },
	})
}

func TestSignAndVerify(t *testing.T) {
	s := newSigner(t, time.Now())
	token, exp, err := s.Sign("user-1", "alice", RoleAdmin)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), exp, time.Second)

	claims, err := NewVerifier(s.KeySource(), "iam", "sse").Verify(context.Background(), token)

	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.Subject)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, RoleAdmin, claims.Role)
	assert.NotEmpty(t, claims.ID)
}

func TestVerifyRejects(t *testing.T) {
	s := newSigner(t, time.Now())
	valid, _, err := s.Sign("user-1", "alice", RoleViewer)
	require.NoError(t, err)

	expired, _, err := newSigner(t, time.Now().Add(-time.Hour)).Sign("user-1", "alice", RoleViewer)
	require.NoError(t, err)

	otherKey, err := GenerateKey(2048)
	require.NoError(t, err)
	foreign, _, err := NewSigner(SignerConfig{Key: otherKey, Issuer: "iam", Audience: []string{"sse"}, TTL: time.Minute}).
		Sign("user-1", "alice", RoleAdmin)
	require.NoError(t, err)

	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{Role: RoleAdmin, RegisteredClaims: jwt.RegisteredClaims{
		Subject: "x", Issuer: "iam", Audience: []string{"sse"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}})
	hs.Header["kid"] = KeyID(&key(t).PublicKey)
	hsToken, err := hs.SignedString([]byte("secret"))
	require.NoError(t, err)

	tests := map[string]struct {
		token    string
		verifier *Verifier
	}{
		"expired":         {expired, NewVerifier(s.KeySource(), "iam", "sse")},
		"wrong audience":  {valid, NewVerifier(s.KeySource(), "iam", "other")},
		"wrong issuer":    {valid, NewVerifier(s.KeySource(), "evil", "sse")},
		"unknown key":     {foreign, NewVerifier(s.KeySource(), "iam", "sse")},
		"HS256 algorithm": {hsToken, NewVerifier(s.KeySource(), "iam", "sse")},
		"garbage":         {"not-a-jwt", NewVerifier(s.KeySource(), "iam", "sse")},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := tt.verifier.Verify(context.Background(), tt.token)
			assert.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}

func TestJWKRoundTrip(t *testing.T) {
	s := newSigner(t, time.Now())
	set := s.JWKS()
	require.Len(t, set.Keys, 1)

	pub, err := set.Keys[0].PublicKey()
	require.NoError(t, err)
	assert.True(t, key(t).PublicKey.Equal(pub))
	assert.Equal(t, KeyID(&key(t).PublicKey), set.Keys[0].Kid)
}

func TestParseRole(t *testing.T) {
	r, err := ParseRole(" Admin ")
	require.NoError(t, err)
	assert.Equal(t, RoleAdmin, r)

	_, err = ParseRole("root")
	assert.Error(t, err)
}

func TestMiddleware(t *testing.T) {
	s := newSigner(t, time.Now())
	app := httpx.NewApp(httpx.AppConfig{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	auth := Authenticate(NewVerifier(s.KeySource(), "iam", "sse"))
	app.Get("/any", auth, func(c *fiber.Ctx) error {
		claims, _ := ClaimsFrom(c)
		return c.SendString(claims.Username)
	})
	app.Get("/admin", auth, RequireRole(RoleAdmin), func(c *fiber.Ctx) error { return c.SendString("ok") })

	admin, _, _ := s.Sign("1", "root", RoleAdmin)
	viewer, _, _ := s.Sign("2", "bob", RoleViewer)

	call := func(path, header string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusUnauthorized, call("/any", ""))
	assert.Equal(t, http.StatusUnauthorized, call("/any", "Basic abc"))
	assert.Equal(t, http.StatusUnauthorized, call("/any", "Bearer nope"))
	assert.Equal(t, http.StatusOK, call("/any", "Bearer "+viewer))
	assert.Equal(t, http.StatusOK, call("/any", "bearer "+viewer), "scheme is case-insensitive")
	assert.Equal(t, http.StatusForbidden, call("/admin", "Bearer "+viewer))
	assert.Equal(t, http.StatusOK, call("/admin", "Bearer "+admin))
}
