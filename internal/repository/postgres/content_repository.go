package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eren-berik/sse-simple-search-engine/internal/domain"
)

const contentColumns = `id, provider, external_id, title, type, views, likes, duration_sec,
	reading_time, reactions, comments, tags, published_at, score, score_updated_at, raw_payload`

// ContentRepository stores content items and serves searches using PostgreSQL full-text search.
type ContentRepository struct {
	pool *pgxpool.Pool
}

func NewContentRepository(pool *pgxpool.Pool) *ContentRepository {
	return &ContentRepository{pool: pool}
}

const upsertContentSQL = `
INSERT INTO contents (provider, external_id, title, type, views, likes, duration_sec,
	reading_time, reactions, comments, tags, published_at, score, score_updated_at, raw_payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (provider, external_id) DO UPDATE SET
	title            = EXCLUDED.title,
	type             = EXCLUDED.type,
	views            = EXCLUDED.views,
	likes            = EXCLUDED.likes,
	duration_sec     = EXCLUDED.duration_sec,
	reading_time     = EXCLUDED.reading_time,
	reactions        = EXCLUDED.reactions,
	comments         = EXCLUDED.comments,
	tags             = EXCLUDED.tags,
	published_at     = EXCLUDED.published_at,
	score            = EXCLUDED.score,
	score_updated_at = EXCLUDED.score_updated_at,
	raw_payload      = EXCLUDED.raw_payload,
	updated_at       = now()`

// UpsertContents inserts or updates items keyed by (provider, external_id) in a single transaction,
// so a provider's batch is either stored completely or not at all.
func (r *ContentRepository) UpsertContents(ctx context.Context, contents []domain.Content) (int, error) {
	if len(contents) == 0 {
		return 0, nil
	}

	batch := &pgx.Batch{}
	for _, c := range contents {
		tags := c.Tags
		if tags == nil {
			tags = []string{}
		}
		m := c.Metrics
		batch.Queue(upsertContentSQL,
			c.Provider, c.ExternalID, c.Title, string(c.Type), m.Views, m.Likes, m.DurationSec,
			m.ReadingTime, m.Reactions, m.Comments, tags, c.PublishedAt, c.Score, c.ScoreUpdatedAt, c.RawPayload)
	}

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		return 0, fmt.Errorf("upsert contents: %w", err)
	}
	return len(contents), nil
}

// RecalculateScores recomputes the score of every item whose score is older than before.
// Recency depends on the current time, so items no longer returned by providers would otherwise keep stale scores.
func (r *ContentRepository) RecalculateScores(ctx context.Context, before time.Time, score func(domain.Content) float64) (int, error) {
	var updated int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+contentColumns+` FROM contents WHERE score_updated_at < $1 FOR UPDATE`, before)
		if err != nil {
			return err
		}
		stale, err := pgx.CollectRows(rows, scanContent)
		if err != nil {
			return err
		}
		if len(stale) == 0 {
			return nil
		}

		now := time.Now()
		batch := &pgx.Batch{}
		for _, c := range stale {
			batch.Queue(`UPDATE contents SET score = $2, score_updated_at = $3 WHERE id = $1`, c.ID, score(c), now)
		}
		updated = len(stale)
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		return 0, fmt.Errorf("recalculate scores: %w", err)
	}
	return updated, nil
}

// Search returns a page of contents matching q.
// Keyword matching combines full-text search on title and tags with a substring match on the title;
// relevance is ts_rank plus trigram similarity of the title.
func (r *ContentRepository) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	args := pgx.NamedArgs{"limit": q.PerPage, "offset": q.Offset()}
	var conds []string
	relevance := "0::float8"

	if q.Keyword != "" {
		args["q"] = q.Keyword
		args["like"] = "%" + escapeLike(q.Keyword) + "%"
		conds = append(conds, `(search_vector @@ websearch_to_tsquery('simple', @q) OR title ILIKE @like)`)
		relevance = `(ts_rank(search_vector, websearch_to_tsquery('simple', @q)) + similarity(title, @q))::float8`
	}
	if q.Type != "" {
		args["type"] = string(q.Type)
		conds = append(conds, `type = @type`)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	orderBy := "score DESC, id ASC"
	if q.Sort == domain.SortRelevance {
		orderBy = "relevance DESC, score DESC, id ASC"
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM contents `+where, args).Scan(&total); err != nil {
		return domain.SearchResult{}, fmt.Errorf("count contents: %w", err)
	}

	result := domain.SearchResult{Total: total, Page: q.Page, PerPage: q.PerPage, Sort: q.Sort, Hits: []domain.SearchHit{}}
	if total == 0 || q.Offset() >= total {
		return result, nil
	}

	sql := fmt.Sprintf(`SELECT %s, %s AS relevance FROM contents %s ORDER BY %s LIMIT @limit OFFSET @offset`,
		contentColumns, relevance, where, orderBy)
	rows, err := r.pool.Query(ctx, sql, args)
	if err != nil {
		return domain.SearchResult{}, fmt.Errorf("search contents: %w", err)
	}
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SearchHit, error) {
		var h domain.SearchHit
		err := row.Scan(append(contentDest(&h.Content), &h.Relevance)...)
		return h, err
	})
	if err != nil {
		return domain.SearchResult{}, fmt.Errorf("scan contents: %w", err)
	}
	result.Hits = hits
	return result, nil
}

// GetByID returns a single content item or domain.ErrNotFound.
func (r *ContentRepository) GetByID(ctx context.Context, id int64) (domain.Content, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+contentColumns+` FROM contents WHERE id = $1`, id)
	if err != nil {
		return domain.Content{}, fmt.Errorf("get content: %w", err)
	}
	c, err := pgx.CollectExactlyOneRow(rows, scanContent)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Content{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Content{}, fmt.Errorf("get content: %w", err)
	}
	return c, nil
}

// RecordSyncRun persists the outcome of a provider sync.
func (r *ContentRepository) RecordSyncRun(ctx context.Context, run domain.SyncRun) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sync_runs (provider, started_at, finished_at, status, fetched, upserted, error)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))`,
		run.Provider, run.StartedAt, run.FinishedAt, string(run.Status), run.Fetched, run.Upserted, run.Error)
	if err != nil {
		return fmt.Errorf("record sync run: %w", err)
	}
	return nil
}

// LatestSyncRuns returns the most recent sync run of each provider.
func (r *ContentRepository) LatestSyncRuns(ctx context.Context) ([]domain.SyncRun, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (provider) provider, started_at, finished_at, status, fetched, upserted, COALESCE(error, '')
		FROM sync_runs
		ORDER BY provider, started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("latest sync runs: %w", err)
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SyncRun, error) {
		var s domain.SyncRun
		err := row.Scan(&s.Provider, &s.StartedAt, &s.FinishedAt, &s.Status, &s.Fetched, &s.Upserted, &s.Error)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan sync runs: %w", err)
	}
	return runs, nil
}

func contentDest(c *domain.Content) []any {
	m := &c.Metrics
	return []any{
		&c.ID, &c.Provider, &c.ExternalID, &c.Title, &c.Type, &m.Views, &m.Likes, &m.DurationSec,
		&m.ReadingTime, &m.Reactions, &m.Comments, &c.Tags, &c.PublishedAt, &c.Score, &c.ScoreUpdatedAt, &c.RawPayload,
	}
}

func scanContent(row pgx.CollectableRow) (domain.Content, error) {
	var c domain.Content
	err := row.Scan(contentDest(&c)...)
	return c, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike escapes LIKE wildcards so user input is matched literally.
func escapeLike(s string) string {
	return likeEscaper.Replace(s)
}
