//go:build integration

package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

// newTestStore creates a throwaway database on the server in TEST_DATABASE_URL and drops it afterwards.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	name := fmt.Sprintf("iam_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	u, err := url.Parse(base)
	require.NoError(t, err)
	u.Path = "/" + name

	pool, err := Connect(ctx, u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
	})
	require.NoError(t, Migrate(ctx, pool))
	return NewStore(pool)
}

func TestUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, domain.User{Username: "alice", PasswordHash: "hash", Role: jwtauth.RoleAdmin})
	require.NoError(t, err)
	assert.Len(t, u.ID, 36)
	assert.False(t, u.CreatedAt.IsZero())

	_, err = s.CreateUser(ctx, domain.User{Username: "alice", PasswordHash: "x", Role: jwtauth.RoleViewer})
	assert.ErrorIs(t, err, domain.ErrUsernameTaken)

	byName, err := s.UserByUsername(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, u.ID, byName.ID)
	assert.Equal(t, jwtauth.RoleAdmin, byName.Role)

	byID, err := s.UserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice", byID.Username)

	_, err = s.UserByID(ctx, "not-a-uuid")
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = s.UserByUsername(ctx, "nobody")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestRefreshTokenLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	u, err := s.CreateUser(ctx, domain.User{Username: "alice", PasswordHash: "hash", Role: jwtauth.RoleViewer})
	require.NoError(t, err)

	family := "6f1d5c1e-0000-4000-8000-000000000001"
	first, second, expired := []byte("hash-1"), []byte("hash-2"), []byte("hash-expired")
	for _, tok := range []domain.RefreshToken{
		{UserID: u.ID, FamilyID: family, TokenHash: first, ExpiresAt: now.Add(time.Hour)},
		{UserID: u.ID, FamilyID: family, TokenHash: second, ExpiresAt: now.Add(time.Hour)},
		{UserID: u.ID, FamilyID: "6f1d5c1e-0000-4000-8000-000000000002", TokenHash: expired, ExpiresAt: now.Add(-time.Minute)},
	} {
		require.NoError(t, s.CreateRefreshToken(ctx, tok))
	}

	consumed, err := s.ConsumeRefreshToken(ctx, first, now)
	require.NoError(t, err)
	assert.Equal(t, u.ID, consumed.UserID)
	assert.Equal(t, family, consumed.FamilyID)

	reused, err := s.ConsumeRefreshToken(ctx, first, now)
	assert.ErrorIs(t, err, domain.ErrRefreshTokenReused)
	assert.Equal(t, family, reused.FamilyID)

	_, err = s.ConsumeRefreshToken(ctx, expired, now)
	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
	_, err = s.ConsumeRefreshToken(ctx, []byte("unknown"), now)
	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)

	require.NoError(t, s.RevokeFamilyByTokenHash(ctx, first, now))
	_, err = s.ConsumeRefreshToken(ctx, second, now)
	assert.ErrorIs(t, err, domain.ErrRefreshTokenReused, "whole family is revoked")
}
