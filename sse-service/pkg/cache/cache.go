// Package cache provides a small key/value cache abstraction with Redis and in-memory implementations.
package cache

import (
	"context"
	"time"
)

// Cache is a byte-oriented key/value store with TTLs and atomic counters.
// A miss is reported as (nil, false, nil); errors are reserved for backend failures.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Incr atomically increments the integer stored at key and returns the new value.
	Incr(ctx context.Context, key string) (int64, error)
	Ping(ctx context.Context) error
}

// Noop is a Cache that stores nothing; useful to disable caching.
type Noop struct{}

func (Noop) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, nil }
func (Noop) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (Noop) Incr(context.Context, string) (int64, error)              { return 0, nil }
func (Noop) Ping(context.Context) error                               { return nil }
