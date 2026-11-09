package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryGetSetExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(0, 0)
	m := NewMemory()
	m.now = func() time.Time { return now }

	require.NoError(t, m.Set(ctx, "k", []byte("v"), time.Minute))
	v, ok, err := m.Get(ctx, "k")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "v", string(v))

	now = now.Add(time.Minute)
	_, ok, err = m.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, ok, "entry must expire after its TTL")
}

func TestMemoryIncr(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	n, err := m.Incr(ctx, "counter")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = m.Incr(ctx, "counter")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	v, ok, _ := m.Get(ctx, "counter")
	assert.True(t, ok)
	assert.Equal(t, "2", string(v))
}
