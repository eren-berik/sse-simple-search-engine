// Package authtest provides an in-memory auth.Store for tests.
package authtest

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
)

// MemoryStore mirrors the PostgreSQL store's semantics in memory.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int
	users  map[string]domain.User
	tokens []*domain.RefreshToken
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: map[string]domain.User{}}
}

func (m *MemoryStore) CreateUser(_ context.Context, u domain.User) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if existing.Username == u.Username {
			return domain.User{}, domain.ErrUsernameTaken
		}
	}
	m.seq++
	u.ID = fmt.Sprintf("user-%d", m.seq)
	u.CreatedAt = time.Now()
	m.users[u.ID] = u
	return u, nil
}

func (m *MemoryStore) UserByUsername(_ context.Context, username string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

func (m *MemoryStore) UserByID(_ context.Context, id string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (m *MemoryStore) CreateRefreshToken(_ context.Context, t domain.RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	t.ID = fmt.Sprintf("token-%d", m.seq)
	m.tokens = append(m.tokens, &t)
	return nil
}

func (m *MemoryStore) ConsumeRefreshToken(_ context.Context, hash []byte, now time.Time) (domain.RefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if !bytes.Equal(t.TokenHash, hash) {
			continue
		}
		if t.RevokedAt != nil {
			return *t, domain.ErrRefreshTokenReused
		}
		if !now.Before(t.ExpiresAt) {
			return domain.RefreshToken{}, domain.ErrInvalidRefreshToken
		}
		revoked := now
		t.RevokedAt = &revoked
		return *t, nil
	}
	return domain.RefreshToken{}, domain.ErrInvalidRefreshToken
}

func (m *MemoryStore) RevokeFamily(_ context.Context, familyID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			revoked := now
			t.RevokedAt = &revoked
		}
	}
	return nil
}

func (m *MemoryStore) RevokeFamilyByTokenHash(ctx context.Context, hash []byte, now time.Time) (string, error) {
	m.mu.Lock()
	family := ""
	for _, t := range m.tokens {
		if bytes.Equal(t.TokenHash, hash) {
			family = t.FamilyID
		}
	}
	m.mu.Unlock()
	if family == "" {
		return "", nil
	}
	return family, m.RevokeFamily(ctx, family, now)
}

// ActiveTokens counts unrevoked tokens, for assertions.
func (m *MemoryStore) ActiveTokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.tokens {
		if t.RevokedAt == nil {
			n++
		}
	}
	return n
}
