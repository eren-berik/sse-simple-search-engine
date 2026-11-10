package jwtauth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

type revokedSet map[string]bool

func (r revokedSet) IsRevoked(_ context.Context, sessionID string) bool { return r[sessionID] }

func TestSignedTokensCarryTheSession(t *testing.T) {
	key, err := jwtauth.GenerateKey(2048)
	require.NoError(t, err)
	s := jwtauth.NewSigner(jwtauth.SignerConfig{Key: key, Issuer: "iam", Audience: []string{"sse"}, TTL: time.Minute})
	v := jwtauth.NewVerifier(s.KeySource(), "iam", "sse").WithRevocations(revokedSet{"ended": true})

	active, _, err := s.Sign("user-1", "alice", jwtauth.RoleViewer, "active")
	require.NoError(t, err)
	claims, err := v.Verify(context.Background(), active)
	require.NoError(t, err)
	assert.Equal(t, "active", claims.SessionID)

	ended, _, err := s.Sign("user-1", "alice", jwtauth.RoleViewer, "ended")
	require.NoError(t, err)
	_, err = v.Verify(context.Background(), ended)
	assert.ErrorIs(t, err, jwtauth.ErrRevoked)
}
