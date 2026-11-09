package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
)

const uniqueViolation = "23505"

// Store persists users and refresh tokens.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const userColumns = `id::text, username, password_hash, role, created_at`

func (s *Store) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)
		RETURNING `+userColumns, u.Username, u.PasswordHash, string(u.Role))
	created, err := scanUser(row)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.User{}, domain.ErrUsernameTaken
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (domain.User, error) {
	return s.oneUser(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
}

func (s *Store) UserByID(ctx context.Context, id string) (domain.User, error) {
	// Ids come from tokens; a malformed one must read as "not found", not as a database error.
	return s.oneUser(ctx, `SELECT `+userColumns+` FROM users WHERE id::text = $1`, id)
}

func (s *Store) oneUser(ctx context.Context, sql string, arg any) (domain.User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, sql, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	return u, err
}

func (s *Store) CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		t.UserID, t.FamilyID, t.TokenHash, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

// ConsumeRefreshToken revokes the token in a single conditional UPDATE, so two concurrent
// refreshes with the same token cannot both succeed.
func (s *Store) ConsumeRefreshToken(ctx context.Context, hash []byte, now time.Time) (domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := s.pool.QueryRow(ctx, `
		UPDATE refresh_tokens SET revoked_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
		RETURNING id::text, user_id::text, family_id::text, expires_at`, hash, now).
		Scan(&t.ID, &t.UserID, &t.FamilyID, &t.ExpiresAt)
	if err == nil {
		t.TokenHash = hash
		t.RevokedAt = &now
		return t, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, fmt.Errorf("consume refresh token: %w", err)
	}

	// Not consumable: find out whether it is unknown, expired or already used.
	err = s.pool.QueryRow(ctx, `
		SELECT id::text, user_id::text, family_id::text, expires_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, hash).
		Scan(&t.ID, &t.UserID, &t.FamilyID, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, domain.ErrInvalidRefreshToken
	}
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("look up refresh token: %w", err)
	}
	if t.RevokedAt != nil {
		return t, domain.ErrRefreshTokenReused
	}
	return domain.RefreshToken{}, domain.ErrInvalidRefreshToken
}

func (s *Store) RevokeFamily(ctx context.Context, familyID string, now time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id::text = $1 AND revoked_at IS NULL`, familyID, now)
	if err != nil {
		return fmt.Errorf("revoke token family: %w", err)
	}
	return nil
}

func (s *Store) RevokeFamilyByTokenHash(ctx context.Context, hash []byte, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = $2
		WHERE revoked_at IS NULL
		  AND family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)`, hash, now)
	if err != nil {
		return fmt.Errorf("revoke token family: %w", err)
	}
	return nil
}
