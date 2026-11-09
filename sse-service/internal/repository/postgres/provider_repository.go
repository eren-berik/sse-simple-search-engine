package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

const (
	providerColumns = `id, name, url, format, enabled, created_at, updated_at`
	uniqueViolation = "23505"
)

// ProviderRepository stores provider configurations.
type ProviderRepository struct {
	pool *pgxpool.Pool
}

func NewProviderRepository(pool *pgxpool.Pool) *ProviderRepository {
	return &ProviderRepository{pool: pool}
}

func (r *ProviderRepository) List(ctx context.Context) ([]domain.Provider, error) {
	return r.list(ctx, `SELECT `+providerColumns+` FROM providers ORDER BY id`)
}

func (r *ProviderRepository) ListEnabled(ctx context.Context) ([]domain.Provider, error) {
	return r.list(ctx, `SELECT `+providerColumns+` FROM providers WHERE enabled ORDER BY id`)
}

func (r *ProviderRepository) list(ctx context.Context, sql string) ([]domain.Provider, error) {
	rows, err := r.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	providers, err := pgx.CollectRows(rows, scanProvider)
	if err != nil {
		return nil, fmt.Errorf("scan providers: %w", err)
	}
	return providers, nil
}

func (r *ProviderRepository) Get(ctx context.Context, id int64) (domain.Provider, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+providerColumns+` FROM providers WHERE id = $1`, id)
	if err != nil {
		return domain.Provider{}, fmt.Errorf("get provider: %w", err)
	}
	return oneProvider(rows)
}

// Create inserts a provider; a duplicate name yields domain.ErrConflict.
func (r *ProviderRepository) Create(ctx context.Context, p domain.Provider) (domain.Provider, error) {
	rows, err := r.pool.Query(ctx, `
		INSERT INTO providers (name, url, format, enabled) VALUES ($1, $2, $3, $4)
		RETURNING `+providerColumns, p.Name, p.URL, string(p.Format), p.Enabled)
	if err != nil {
		return domain.Provider{}, fmt.Errorf("create provider: %w", err)
	}
	created, err := oneProvider(rows)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.Provider{}, fmt.Errorf("%w: a provider named %q already exists", domain.ErrConflict, p.Name)
	}
	return created, err
}

// Update applies the non-nil fields of patch.
func (r *ProviderRepository) Update(ctx context.Context, id int64, patch domain.ProviderPatch) (domain.Provider, error) {
	var format *string
	if patch.Format != nil {
		f := string(*patch.Format)
		format = &f
	}
	rows, err := r.pool.Query(ctx, `
		UPDATE providers SET
			url        = COALESCE($2, url),
			format     = COALESCE($3, format),
			enabled    = COALESCE($4, enabled),
			updated_at = now()
		WHERE id = $1
		RETURNING `+providerColumns, id, patch.URL, format, patch.Enabled)
	if err != nil {
		return domain.Provider{}, fmt.Errorf("update provider: %w", err)
	}
	return oneProvider(rows)
}

func oneProvider(rows pgx.Rows) (domain.Provider, error) {
	p, err := pgx.CollectExactlyOneRow(rows, scanProvider)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Provider{}, domain.ErrNotFound
	}
	return p, err
}

func scanProvider(row pgx.CollectableRow) (domain.Provider, error) {
	var p domain.Provider
	err := row.Scan(&p.ID, &p.Name, &p.URL, &p.Format, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}
