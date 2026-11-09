//go:build integration

package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedis(t *testing.T) {
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(raw)
	require.NoError(t, err)
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	prefix := "test:" + time.Now().Format(time.RFC3339Nano) + ":"
	t.Cleanup(func() {
		keys, _ := client.Keys(context.Background(), prefix+"*").Result()
		if len(keys) > 0 {
			client.Del(context.Background(), keys...)
		}
	})
	c := NewRedis(client)

	require.NoError(t, c.Ping(ctx))

	_, ok, err := c.Get(ctx, prefix+"missing")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, c.Set(ctx, prefix+"k", []byte("v"), time.Minute))
	v, ok, err := c.Get(ctx, prefix+"k")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "v", string(v))

	ttl, err := client.TTL(ctx, prefix+"k").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 50*time.Second)

	n, err := c.Incr(ctx, prefix+"version")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}
