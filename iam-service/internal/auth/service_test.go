package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth/authtest"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

type fakeSigner struct{}

func (fakeSigner) Sign(subject, username string, role jwtauth.Role, sessionID string) (string, time.Time, error) {
	return "access:" + subject + ":" + string(role), time.Now().Add(time.Minute), nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newService(t *testing.T) (*Service, *authtest.MemoryStore, *clock) {
	t.Helper()
	store := authtest.NewMemoryStore()
	clk := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc, err := NewService(store, BcryptHasher{Cost: bcrypt.MinCost}, fakeSigner{},
		Config{RefreshTokenTTL: time.Hour, Now: clk.now}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	return svc, store, clk
}

func register(t *testing.T, svc *Service, username, role string) Session {
	t.Helper()
	s, err := svc.Register(context.Background(), RegisterInput{Username: username, Password: "s3cret-pass", Role: role})
	require.NoError(t, err)
	return s
}

func TestRegister(t *testing.T) {
	svc, store, _ := newService(t)

	s := register(t, svc, "  Alice ", "admin")

	assert.Equal(t, "alice", s.User.Username, "username is normalized")
	assert.Equal(t, jwtauth.RoleAdmin, s.User.Role)
	assert.Equal(t, "access:"+s.User.ID+":admin", s.AccessToken)
	assert.NotEmpty(t, s.RefreshToken)
	assert.NotEqual(t, "s3cret-pass", s.User.PasswordHash, "password is stored hashed")
	assert.Equal(t, 1, store.ActiveTokens())

	viewer := register(t, svc, "bob", "")
	assert.Equal(t, jwtauth.RoleViewer, viewer.User.Role, "role defaults to viewer")
}

func TestRegisterValidation(t *testing.T) {
	svc, _, _ := newService(t)
	register(t, svc, "alice", "viewer")

	tests := map[string]RegisterInput{
		"short username": {Username: "ab", Password: "s3cret-pass"},
		"invalid chars":  {Username: "al ice", Password: "s3cret-pass"},
		"short password": {Username: "carol", Password: "short"},
		"too long":       {Username: "carol", Password: strings.Repeat("x", 73)},
		"unknown role":   {Username: "carol", Password: "s3cret-pass", Role: "root"},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Register(context.Background(), in)
			assert.ErrorIs(t, err, domain.ErrInvalidInput)
		})
	}

	_, err := svc.Register(context.Background(), RegisterInput{Username: "ALICE", Password: "s3cret-pass"})
	assert.ErrorIs(t, err, domain.ErrUsernameTaken)
}

func TestLogin(t *testing.T) {
	svc, _, _ := newService(t)
	reg := register(t, svc, "alice", "viewer")
	ctx := context.Background()

	s, err := svc.Login(ctx, "Alice", "s3cret-pass")
	require.NoError(t, err)
	assert.Equal(t, reg.User.ID, s.User.ID)
	assert.NotEqual(t, reg.RefreshToken, s.RefreshToken)

	_, err = svc.Login(ctx, "alice", "wrong-password")
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials)

	_, err = svc.Login(ctx, "nobody", "s3cret-pass")
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials, "unknown user gives the same error")
}

func TestRefreshRotatesToken(t *testing.T) {
	svc, store, _ := newService(t)
	reg := register(t, svc, "alice", "viewer")
	ctx := context.Background()

	next, err := svc.Refresh(ctx, reg.RefreshToken)
	require.NoError(t, err)
	assert.NotEqual(t, reg.RefreshToken, next.RefreshToken)
	assert.Equal(t, reg.User.ID, next.User.ID)
	assert.Equal(t, 1, store.ActiveTokens(), "old token is revoked, new one is active")

	_, err = svc.Refresh(ctx, "unknown-token")
	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
}

func TestRefreshReuseRevokesFamily(t *testing.T) {
	svc, store, _ := newService(t)
	reg := register(t, svc, "alice", "viewer")
	ctx := context.Background()

	next, err := svc.Refresh(ctx, reg.RefreshToken)
	require.NoError(t, err)

	// An attacker replays the already rotated token.
	_, err = svc.Refresh(ctx, reg.RefreshToken)
	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
	assert.Equal(t, 0, store.ActiveTokens())

	// The legitimate client's current token was revoked with the family.
	_, err = svc.Refresh(ctx, next.RefreshToken)
	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
}

func TestRefreshExpired(t *testing.T) {
	svc, _, clk := newService(t)
	reg := register(t, svc, "alice", "viewer")

	clk.t = clk.t.Add(time.Hour)
	_, err := svc.Refresh(context.Background(), reg.RefreshToken)

	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
}

func TestLogout(t *testing.T) {
	svc, store, _ := newService(t)
	reg := register(t, svc, "alice", "viewer")
	other, err := svc.Login(context.Background(), "alice", "s3cret-pass")
	require.NoError(t, err)

	require.NoError(t, svc.Logout(context.Background(), reg.RefreshToken))

	_, err = svc.Refresh(context.Background(), reg.RefreshToken)
	assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
	assert.Equal(t, 1, store.ActiveTokens(), "other sessions stay active")
	_, err = svc.Refresh(context.Background(), other.RefreshToken)
	assert.NoError(t, err)

	assert.NoError(t, svc.Logout(context.Background(), "unknown"), "logout is idempotent")
}

// sessionSigner returns the session id as the access token, so tests can see which session a
// token belongs to.
type sessionSigner struct{}

func (sessionSigner) Sign(_, _ string, _ jwtauth.Role, sessionID string) (string, time.Time, error) {
	return sessionID, time.Now().Add(time.Minute), nil
}

type recordingRevoker struct {
	sessions []string
	ttl      time.Duration
	err      error
}

func (r *recordingRevoker) Revoke(_ context.Context, sessionID string, ttl time.Duration) error {
	r.sessions = append(r.sessions, sessionID)
	r.ttl = ttl
	return r.err
}

func newRevokingService(t *testing.T, revoker *recordingRevoker) *Service {
	t.Helper()
	svc, err := NewService(authtest.NewMemoryStore(), BcryptHasher{Cost: bcrypt.MinCost}, sessionSigner{},
		Config{RefreshTokenTTL: time.Hour, Revoker: revoker, RevocationTTL: 16 * time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	return svc
}

func TestSessionsAndRevocation(t *testing.T) {
	ctx := context.Background()

	t.Run("tokens carry their session; refresh keeps it, login starts a new one", func(t *testing.T) {
		svc := newRevokingService(t, &recordingRevoker{})
		first := register(t, svc, "alice", "viewer")
		require.NotEmpty(t, first.AccessToken)

		refreshed, err := svc.Refresh(ctx, first.RefreshToken)
		require.NoError(t, err)
		assert.Equal(t, first.AccessToken, refreshed.AccessToken, "same session id")

		second, err := svc.Login(ctx, "alice", "s3cret-pass")
		require.NoError(t, err)
		assert.NotEqual(t, first.AccessToken, second.AccessToken)
	})

	t.Run("logout revokes the session's access tokens", func(t *testing.T) {
		revoker := &recordingRevoker{}
		svc := newRevokingService(t, revoker)
		s := register(t, svc, "alice", "viewer")

		require.NoError(t, svc.Logout(ctx, s.RefreshToken))
		assert.Equal(t, []string{s.AccessToken}, revoker.sessions)
		assert.Equal(t, 16*time.Minute, revoker.ttl)

		require.NoError(t, svc.Logout(ctx, "unknown-token"))
		assert.Len(t, revoker.sessions, 1, "unknown tokens revoke nothing")
	})

	t.Run("refresh token reuse revokes the session", func(t *testing.T) {
		revoker := &recordingRevoker{}
		svc := newRevokingService(t, revoker)
		s := register(t, svc, "alice", "viewer")
		_, err := svc.Refresh(ctx, s.RefreshToken)
		require.NoError(t, err)

		_, err = svc.Refresh(ctx, s.RefreshToken)
		assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
		assert.Equal(t, []string{s.AccessToken}, revoker.sessions)
	})

	t.Run("a failing revocation list does not fail logout", func(t *testing.T) {
		svc := newRevokingService(t, &recordingRevoker{err: errors.New("redis down")})
		s := register(t, svc, "alice", "viewer")
		assert.NoError(t, svc.Logout(ctx, s.RefreshToken))
	})
}
