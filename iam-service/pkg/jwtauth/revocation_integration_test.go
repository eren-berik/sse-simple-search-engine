//go:build integration

package jwtauth

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRevocationsRoundTrip(t *testing.T) {
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(raw)
	require.NoError(t, err)
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	session := "test-" + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() { client.Del(context.Background(), revokedSessionPrefix+session) })
	r := NewSessionRevocations(client, slog.New(slog.NewTextHandler(io.Discard, nil)))

	assert.False(t, r.IsRevoked(ctx, session))
	require.NoError(t, r.Revoke(ctx, session, time.Minute))
	assert.True(t, r.IsRevoked(ctx, session))
	assert.False(t, r.IsRevoked(ctx, session+"-other"))

	ttl, err := client.TTL(ctx, revokedSessionPrefix+session).Result()
	require.NoError(t, err)
	assert.InDelta(t, time.Minute.Seconds(), ttl.Seconds(), 2, "entries expire with the access tokens they cover")
}
