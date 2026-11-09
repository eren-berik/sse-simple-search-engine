package cache

import (
	"context"
	"strconv"
	"sync"
	"time"
)

type memoryEntry struct {
	value     []byte
	expiresAt time.Time // zero means no expiry
}

// Memory is an in-process Cache, used when Redis is not configured and in tests.
// Expired entries are evicted lazily on read.
type Memory struct {
	mu    sync.Mutex
	items map[string]memoryEntry
	now   func() time.Time
}

func NewMemory() *Memory {
	return &Memory{items: make(map[string]memoryEntry), now: time.Now}
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.items[key]
	if !ok {
		return nil, false, nil
	}
	if !e.expiresAt.IsZero() && !m.now().Before(e.expiresAt) {
		delete(m.items, key)
		return nil, false, nil
	}
	return e.value, true, nil
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := memoryEntry{value: value}
	if ttl > 0 {
		e.expiresAt = m.now().Add(ttl)
	}
	m.items[key] = e
	return nil
}

func (m *Memory) Incr(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var n int64
	if e, ok := m.items[key]; ok {
		parsed, err := strconv.ParseInt(string(e.value), 10, 64)
		if err != nil {
			return 0, err
		}
		n = parsed
	}
	n++
	m.items[key] = memoryEntry{value: []byte(strconv.FormatInt(n, 10))}
	return n, nil
}

func (m *Memory) Ping(context.Context) error { return nil }
