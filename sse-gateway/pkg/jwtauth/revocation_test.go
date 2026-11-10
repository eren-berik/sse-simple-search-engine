package jwtauth_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth/jwtauthtest"
)

type revokedSet map[string]bool

func (r revokedSet) IsRevoked(_ context.Context, sessionID string) bool { return r[sessionID] }

func TestVerifierRejectsRevokedSessions(t *testing.T) {
	iss := jwtauthtest.New(t)
	v := iss.Verifier().WithRevocations(revokedSet{"ended": true})
	ctx := context.Background()

	_, err := v.Verify(ctx, iss.TokenForSession(t, jwtauth.RoleViewer, "ended"))
	assert.ErrorIs(t, err, jwtauth.ErrRevoked)
	assert.ErrorIs(t, err, jwtauth.ErrInvalidToken, "a revoked token is an invalid token to callers")

	claims, err := v.Verify(ctx, iss.TokenForSession(t, jwtauth.RoleViewer, "active"))
	require.NoError(t, err)
	assert.Equal(t, "active", claims.SessionID)

	_, err = v.Verify(ctx, iss.Token(t, jwtauth.RoleViewer))
	assert.NoError(t, err, "tokens without a session (issued before sessions existed) are accepted")
}

func TestSessionRevocationsFailOpen(t *testing.T) {
	down := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = down.Close() })
	r := jwtauth.NewSessionRevocations(down, slog.New(slog.NewTextHandler(io.Discard, nil)))

	assert.False(t, r.IsRevoked(context.Background(), "any"), "an unreachable list must not lock everyone out")
}
