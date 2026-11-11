//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

func TestProviderRepository(t *testing.T) {
	_, pool := newTestRepo(t)
	repo := NewProviderRepository(pool)
	ctx := context.Background()

	seeded, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, seeded, 2, "the case study providers are seeded by migration")
	assert.Equal(t, "provider1", seeded[0].Name)
	assert.Equal(t, domain.ProviderFormatJSON, seeded[0].Format)
	assert.Equal(t, domain.ProviderFormatXML, seeded[1].Format)

	created, err := repo.Create(ctx, domain.Provider{Name: "feed", URL: "https://x.io/feed", Format: domain.ProviderFormatXML, Enabled: true})
	require.NoError(t, err)
	assert.Positive(t, created.ID)
	assert.False(t, created.CreatedAt.IsZero())

	_, err = repo.Create(ctx, domain.Provider{Name: "feed", URL: "https://y.io", Format: domain.ProviderFormatJSON})
	assert.ErrorIs(t, err, domain.ErrConflict)

	disabled := false
	updated, err := repo.Update(ctx, created.ID, domain.ProviderPatch{Enabled: &disabled})
	require.NoError(t, err)
	assert.False(t, updated.Enabled)
	assert.Equal(t, "https://x.io/feed", updated.URL, "unset fields are unchanged")

	enabled, err := repo.ListEnabled(ctx)
	require.NoError(t, err)
	assert.Len(t, enabled, 2)

	_, err = repo.Update(ctx, 999, domain.ProviderPatch{Enabled: &disabled})
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repo.Get(ctx, 999)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestContentRequiresKnownProvider(t *testing.T) {
	repo, _ := newTestRepo(t)

	_, err := repo.UpsertContents(context.Background(), []domain.Content{{
		Provider: "ghost", ExternalID: "x", Title: "t", Type: domain.ContentTypeText,
		PublishedAt: fixedNow, ScoreUpdatedAt: time.Now(),
	}})

	assert.Error(t, err, "contents reference providers by name")
}

func TestSearchByProvider(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)

	res := search(t, repo, domain.SearchQuery{Provider: "provider2", PerPage: 100})

	assert.Equal(t, 4, res.Total)
	for _, h := range res.Hits {
		assert.Equal(t, "provider2", h.Content.Provider)
	}

	combined := search(t, repo, domain.SearchQuery{Provider: "provider1", Keyword: "docker", Sort: domain.SortRelevance})
	assert.Equal(t, 0, combined.Total, "filters combine with the keyword")
}

func TestDisabledProviderContentIsHidden(t *testing.T) {
	repo, pool := newTestRepo(t)
	providers := NewProviderRepository(pool)
	seed(t, repo)
	ctx := context.Background()

	var id int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM contents WHERE provider = 'provider2' LIMIT 1`).Scan(&id))
	set := func(enabled bool) {
		_, err := providers.Update(ctx, 2, domain.ProviderPatch{Enabled: &enabled})
		require.NoError(t, err)
	}

	set(false)
	all := search(t, repo, domain.SearchQuery{PerPage: 100})
	assert.Equal(t, 4, all.Total, "only provider1 contents remain")
	for _, h := range all.Hits {
		assert.Equal(t, "provider1", h.Content.Provider)
	}
	assert.Equal(t, 0, search(t, repo, domain.SearchQuery{Provider: "provider2"}).Total)
	_, err := repo.GetByID(ctx, id)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	set(true)
	assert.Equal(t, 8, search(t, repo, domain.SearchQuery{PerPage: 100}).Total, "contents come back when re-enabled")
	_, err = repo.GetByID(ctx, id)
	assert.NoError(t, err)
}

func TestRenameProviderCarriesContentsAndRuns(t *testing.T) {
	repo, pool := newTestRepo(t)
	providers := NewProviderRepository(pool)
	seed(t, repo)
	ctx := context.Background()
	require.NoError(t, repo.RecordSyncRun(ctx, domain.SyncRun{Provider: "provider2", StartedAt: fixedNow, FinishedAt: fixedNow, Status: domain.SyncStatusSuccess, Upserted: 4}))

	name := "renamed"
	p, err := providers.Update(ctx, 2, domain.ProviderPatch{Name: &name})
	require.NoError(t, err)
	assert.Equal(t, "renamed", p.Name)

	assert.Equal(t, 4, search(t, repo, domain.SearchQuery{Provider: "renamed"}).Total, "contents follow the rename")
	assert.Equal(t, 0, search(t, repo, domain.SearchQuery{Provider: "provider2"}).Total)
	runs, err := repo.LatestSyncRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "renamed", runs[0].Provider)

	taken := "provider1"
	_, err = providers.Update(ctx, 2, domain.ProviderPatch{Name: &taken})
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestDeleteProviderRemovesContentsAndRuns(t *testing.T) {
	repo, pool := newTestRepo(t)
	providers := NewProviderRepository(pool)
	seed(t, repo)
	ctx := context.Background()
	require.NoError(t, repo.RecordSyncRun(ctx, domain.SyncRun{Provider: "provider2", StartedAt: fixedNow, FinishedAt: fixedNow, Status: domain.SyncStatusSuccess}))

	require.NoError(t, providers.Delete(ctx, 2))

	var contents, runs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM contents WHERE provider = 'provider2'`).Scan(&contents))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sync_runs WHERE provider = 'provider2'`).Scan(&runs))
	assert.Zero(t, contents)
	assert.Zero(t, runs)
	assert.Equal(t, 4, search(t, repo, domain.SearchQuery{PerPage: 100}).Total, "other providers are untouched")

	assert.ErrorIs(t, providers.Delete(ctx, 2), domain.ErrNotFound)
}
