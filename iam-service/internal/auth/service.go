// Package auth implements registration, login and token lifecycle.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

// Store persists users and refresh tokens.
type Store interface {
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	UserByUsername(ctx context.Context, username string) (domain.User, error)
	UserByID(ctx context.Context, id string) (domain.User, error)

	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
	// ConsumeRefreshToken atomically revokes the active token with the given hash and returns it.
	// It returns ErrRefreshTokenReused (with the token) if the token was already revoked,
	// and ErrInvalidRefreshToken if it is unknown or expired.
	ConsumeRefreshToken(ctx context.Context, hash []byte, now time.Time) (domain.RefreshToken, error)
	RevokeFamily(ctx context.Context, familyID string, now time.Time) error
	// RevokeFamilyByTokenHash revokes the family of the token and returns its id ("" if unknown).
	RevokeFamilyByTokenHash(ctx context.Context, hash []byte, now time.Time) (string, error)
}

// TokenSigner issues access tokens.
type TokenSigner interface {
	Sign(subject, username string, role jwtauth.Role, sessionID string) (string, time.Time, error)
}

// SessionRevoker makes a session's access tokens invalid before they expire. Refresh tokens
// are revoked in the store; access tokens are not stored, so verifiers consult this list.
type SessionRevoker interface {
	Revoke(ctx context.Context, sessionID string, ttl time.Duration) error
}

// Session is the result of a successful register, login or refresh.
type Session struct {
	User                  domain.User
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
}

type RegisterInput struct {
	Username string
	Password string
	Role     string // empty defaults to viewer
}

type Config struct {
	RefreshTokenTTL time.Duration
	Now             func() time.Time
	// Revoker is optional. Without it a revoked session's access tokens stay valid until they
	// expire; with it they are rejected immediately. RevocationTTL must cover the longest
	// remaining lifetime of an access token (its TTL plus the verifiers' clock leeway).
	Revoker       SessionRevoker
	RevocationTTL time.Duration
}

type Service struct {
	store  Store
	hasher PasswordHasher
	signer TokenSigner
	cfg    Config
	log    *slog.Logger
	// dummyHash is compared against when a username does not exist, so login takes the same
	// time either way and does not reveal which usernames are registered.
	dummyHash string
}

func NewService(store Store, hasher PasswordHasher, signer TokenSigner, cfg Config, log *slog.Logger) (*Service, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	dummy, err := hasher.Hash("timing-equalization-password")
	if err != nil {
		return nil, fmt.Errorf("prepare dummy hash: %w", err)
	}
	return &Service{store: store, hasher: hasher, signer: signer, cfg: cfg, log: log, dummyHash: dummy}, nil
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)

const (
	minPasswordLen = 8
	maxPasswordLen = 72 // bcrypt ignores bytes beyond 72
)

// Register creates a user with the requested role and starts a session.
func (s *Service) Register(ctx context.Context, in RegisterInput) (Session, error) {
	username := normalizeUsername(in.Username)
	if !usernamePattern.MatchString(username) {
		return Session{}, fmt.Errorf("%w: username must be 3-32 characters of letters, digits, '.', '_' or '-'", domain.ErrInvalidInput)
	}
	if n := len(in.Password); n < minPasswordLen || n > maxPasswordLen {
		return Session{}, fmt.Errorf("%w: password must be %d-%d bytes long", domain.ErrInvalidInput, minPasswordLen, maxPasswordLen)
	}
	role := jwtauth.RoleViewer
	if in.Role != "" {
		r, err := jwtauth.ParseRole(in.Role)
		if err != nil {
			return Session{}, fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
		}
		role = r
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.store.CreateUser(ctx, domain.User{Username: username, PasswordHash: hash, Role: role})
	if err != nil {
		return Session{}, err
	}
	s.log.InfoContext(ctx, "user registered", "user_id", user.ID, "role", user.Role)
	return s.startSession(ctx, user, "")
}

// Login checks credentials and starts a new session (a new refresh token family).
func (s *Service) Login(ctx context.Context, username, password string) (Session, error) {
	user, err := s.store.UserByUsername(ctx, normalizeUsername(username))
	if errors.Is(err, domain.ErrNotFound) {
		_ = s.hasher.Compare(s.dummyHash, password)
		return Session{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if err := s.hasher.Compare(user.PasswordHash, password); err != nil {
		if errors.Is(err, errMismatch) {
			return Session{}, domain.ErrInvalidCredentials
		}
		return Session{}, fmt.Errorf("compare password: %w", err)
	}
	return s.startSession(ctx, user, "")
}

// Refresh rotates a refresh token: the presented token is revoked and a new pair is issued in the
// same family. Presenting an already rotated token revokes the entire family.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	now := s.cfg.Now()
	stored, err := s.store.ConsumeRefreshToken(ctx, hashToken(refreshToken), now)
	if errors.Is(err, domain.ErrRefreshTokenReused) {
		s.log.WarnContext(ctx, "refresh token reuse detected; revoking family", "user_id", stored.UserID, "family_id", stored.FamilyID)
		if rerr := s.store.RevokeFamily(ctx, stored.FamilyID, now); rerr != nil {
			return Session{}, fmt.Errorf("revoke token family: %w", rerr)
		}
		// The thief may also hold an access token of this session.
		s.revokeSession(ctx, stored.FamilyID)
		return Session{}, domain.ErrInvalidRefreshToken
	}
	if err != nil {
		return Session{}, err
	}

	// Load the user again so role changes take effect on refresh.
	user, err := s.store.UserByID(ctx, stored.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return Session{}, domain.ErrInvalidRefreshToken
	}
	if err != nil {
		return Session{}, err
	}
	return s.startSession(ctx, user, stored.FamilyID)
}

// Logout revokes the session the refresh token belongs to: its refresh tokens and the access
// tokens issued for it. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	familyID, err := s.store.RevokeFamilyByTokenHash(ctx, hashToken(refreshToken), s.cfg.Now())
	if err != nil {
		return err
	}
	if familyID != "" {
		s.revokeSession(ctx, familyID)
	}
	return nil
}

// revokeSession rejects the session's access tokens. A failure is logged, not returned: the
// refresh tokens are already revoked, and the access tokens expire on their own.
func (s *Service) revokeSession(ctx context.Context, sessionID string) {
	if s.cfg.Revoker == nil {
		return
	}
	if err := s.cfg.Revoker.Revoke(ctx, sessionID, s.cfg.RevocationTTL); err != nil {
		s.log.WarnContext(ctx, "revoking session access tokens failed; they stay valid until they expire",
			"session_id", sessionID, "error", err)
	}
}

// User returns a user by id.
func (s *Service) User(ctx context.Context, id string) (domain.User, error) {
	return s.store.UserByID(ctx, id)
}

// startSession issues an access token and a refresh token. An empty familyID starts a new family.
// The family id is the session id carried by the access token.
func (s *Service) startSession(ctx context.Context, user domain.User, familyID string) (Session, error) {
	if familyID == "" {
		var err error
		if familyID, err = newID(); err != nil {
			return Session{}, err
		}
	}
	access, accessExp, err := s.signer.Sign(user.ID, user.Username, user.Role, familyID)
	if err != nil {
		return Session{}, err
	}

	refresh, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	refreshExp := s.cfg.Now().Add(s.cfg.RefreshTokenTTL)
	if err := s.store.CreateRefreshToken(ctx, domain.RefreshToken{
		UserID:    user.ID,
		FamilyID:  familyID,
		TokenHash: hashToken(refresh),
		ExpiresAt: refreshExp,
	}); err != nil {
		return Session{}, err
	}

	return Session{
		User:                  user,
		AccessToken:           access,
		AccessTokenExpiresAt:  accessExp,
		RefreshToken:          refresh,
		RefreshTokenExpiresAt: refreshExp,
	}, nil
}

func normalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// newRefreshToken returns 256 random bits, URL-safe encoded. It is opaque: unlike the access
// token it carries no claims and is only meaningful to this service.
func newRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the SHA-256 of a refresh token. A fast hash is fine here because the token
// has 256 bits of entropy; a database leak still does not reveal usable tokens.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// newID returns a random UUIDv4 string.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
