package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

const contentColumns = `id, provider, external_id, title, type, views, likes, duration_sec,
	reading_time, reactions, comments, tags, published_at, score, score_updated_at, raw_payload`

// enabledProviderCond restricts queries to contents whose provider is enabled.
const enabledProviderCond = `EXISTS (SELECT 1 FROM providers p WHERE p.name = contents.provider AND p.enabled)`

// listedCond is what users can see: items of enabled providers that the provider still returns.
const listedCond = `removed_at IS NULL AND ` + enabledProviderCond

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
	removed_at       = NULL, -- returned again, so listed again
	updated_at       = now()
-- Unchanged rows are left alone: no write, no dead tuple, and the caller can tell whether the
-- sync changed anything. score_updated_at is not compared; it only says when the score was computed.
WHERE (contents.title, contents.type, contents.views, contents.likes, contents.duration_sec,
       contents.reading_time, contents.reactions, contents.comments, contents.tags,
       contents.published_at, contents.score, contents.raw_payload, contents.removed_at)
   IS DISTINCT FROM
      (EXCLUDED.title, EXCLUDED.type, EXCLUDED.views, EXCLUDED.likes, EXCLUDED.duration_sec,
       EXCLUDED.reading_time, EXCLUDED.reactions, EXCLUDED.comments, EXCLUDED.tags,
       EXCLUDED.published_at, EXCLUDED.score, EXCLUDED.raw_payload, NULL::timestamptz)`

// UpsertContents inserts or updates items keyed by (provider, external_id) in a single transaction,
// so a provider's batch is either stored completely or not at all. Items identical to the stored
// row are skipped; the result is the number of rows inserted or changed.
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

	var changed int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		results := tx.SendBatch(ctx, batch)
		for range contents {
			tag, err := results.Exec()
			if err != nil {
				_ = results.Close()
				return err
			}
			changed += int(tag.RowsAffected())
		}
		return results.Close()
	})
	if err != nil {
		return 0, fmt.Errorf("upsert contents: %w", err)
	}
	return changed, nil
}

// MarkRemoved marks the provider's listed items whose external id is not in keep as removed,
// and returns how many it marked. Call it only with a provider's complete feed.
func (r *ContentRepository) MarkRemoved(ctx context.Context, provider string, keep []string, now time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE contents SET removed_at = $3, updated_at = now()
		WHERE provider = $1 AND removed_at IS NULL AND NOT (external_id = ANY($2))`,
		provider, keep, now)
	if err != nil {
		return 0, fmt.Errorf("mark removed contents: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RecalculateScores recomputes the score of every listed item whose score was computed before
// before: the items this sync did not write. Recency depends on the current time, so their
// scores can go stale (e.g. leaving the "within a week" tier). Only scores that actually changed
// are written; the result is how many.
func (r *ContentRepository) RecalculateScores(ctx context.Context, before time.Time, score func(domain.Content) float64) (int, error) {
	var updated int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+contentColumns+` FROM contents WHERE score_updated_at < $1 AND removed_at IS NULL FOR UPDATE`, before)
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
			if s := score(c); s != c.Score {
				batch.Queue(`UPDATE contents SET score = $2, score_updated_at = $3, updated_at = now() WHERE id = $1`, c.ID, s, now)
			}
		}
		updated = batch.Len()
		if updated == 0 {
			return nil
		}
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
	// Contents of disabled providers stay stored but are hidden until the provider is enabled again.
	conds := []string{listedCond}
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
	if q.Provider != "" {
		args["provider"] = q.Provider
		conds = append(conds, `provider = @provider`)
	}
	if q.Tag != "" {
		// Tags are stored lowercase; @> can use the GIN index on tags.
		args["tag"] = []string{q.Tag}
		conds = append(conds, `tags @> @tag::text[]`)
	}
	if !q.PublishedFrom.IsZero() {
		args["from"] = q.PublishedFrom
		conds = append(conds, `published_at >= @from`)
	}
	if !q.PublishedTo.IsZero() {
		// The end day is inclusive: everything before the start of the next day.
		args["to"] = q.PublishedTo.AddDate(0, 0, 1)
		conds = append(conds, `published_at < @to`)
	}

	where := "WHERE " + strings.Join(conds, " AND ")
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

// GetByID returns a single content item, or domain.ErrNotFound if it does not exist or its provider is disabled.
func (r *ContentRepository) GetByID(ctx context.Context, id int64) (domain.Content, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+contentColumns+` FROM contents WHERE id = $1 AND `+listedCond, id)
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
		INSERT INTO sync_runs (provider, started_at, finished_at, status, fetched, upserted, removed, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))`,
		run.Provider, run.StartedAt, run.FinishedAt, string(run.Status), run.Fetched, run.Upserted, run.Removed, run.Error)
	if err != nil {
		return fmt.Errorf("record sync run: %w", err)
	}
	return nil
}

// CountContents returns how many items are stored per provider and type.
func (r *ContentRepository) CountContents(ctx context.Context) ([]domain.ContentCount, error) {
	rows, err := r.pool.Query(ctx, `SELECT provider, type, count(*) FROM contents WHERE removed_at IS NULL GROUP BY provider, type`)
	if err != nil {
		return nil, fmt.Errorf("count contents: %w", err)
	}
	counts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ContentCount, error) {
		var c domain.ContentCount
		err := row.Scan(&c.Provider, &c.Type, &c.Count)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("count contents: %w", err)
	}
	return counts, nil
}

// LatestSyncRuns returns the most recent sync run of each provider.
func (r *ContentRepository) LatestSyncRuns(ctx context.Context) ([]domain.SyncRun, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (provider) provider, started_at, finished_at, status, fetched, upserted, removed, COALESCE(error, '')
		FROM sync_runs
		-- a run recorded while its provider was being deleted must not resurface
		WHERE provider IN (SELECT name FROM providers)
		ORDER BY provider, started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("latest sync runs: %w", err)
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SyncRun, error) {
		var s domain.SyncRun
		err := row.Scan(&s.Provider, &s.StartedAt, &s.FinishedAt, &s.Status, &s.Fetched, &s.Upserted, &s.Removed, &s.Error)
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
