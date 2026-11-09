package jwtauth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// RemoteKeySource fetches signing keys from a JWKS endpoint and caches them.
// An unknown kid triggers a refetch (which picks up key rotation), but refetches are
// rate limited so tokens with random kids cannot be used to hammer the issuer.
type RemoteKeySource struct {
	url    string
	client *http.Client
	// refreshInterval is the minimum time between fetches after a success; retryInterval after a failure.
	refreshInterval time.Duration
	retryInterval   time.Duration
	now             func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	lastErr   error
}

func NewRemoteKeySource(url string, timeout time.Duration) *RemoteKeySource {
	return &RemoteKeySource{
		url:             url,
		client:          &http.Client{Timeout: timeout},
		refreshInterval: 30 * time.Second,
		retryInterval:   2 * time.Second,
		now:             time.Now,
		keys:            map[string]*rsa.PublicKey{},
	}
}

// Key returns the public key for kid, fetching the key set when kid is unknown.
func (r *RemoteKeySource) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if k, ok := r.keys[kid]; ok {
		return k, nil
	}
	if !r.fetchedAt.IsZero() {
		wait := r.refreshInterval
		if r.lastErr != nil {
			wait = r.retryInterval
		}
		if r.now().Sub(r.fetchedAt) < wait {
			if r.lastErr != nil {
				return nil, fmt.Errorf("%w: %w", ErrKeysUnavailable, r.lastErr)
			}
			return nil, ErrUnknownKey
		}
	}

	keys, err := r.fetch(ctx)
	r.fetchedAt = r.now()
	r.lastErr = err
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeysUnavailable, err)
	}
	r.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, ErrUnknownKey
}

// Ping checks that the key set can be fetched; used for readiness.
func (r *RemoteKeySource) Ping(ctx context.Context) error {
	_, err := r.fetch(ctx)
	return err
}

func (r *RemoteKeySource) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch jwks: unexpected status %d", resp.StatusCode)
	}

	var set JWKSet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := k.PublicKey()
		if err != nil {
			return nil, fmt.Errorf("decode jwk %q: %w", k.Kid, err)
		}
		keys[k.Kid] = pub
	}
	return keys, nil
}
