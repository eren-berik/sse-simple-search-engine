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

	combined := search(t, repo, domain.SearchQuery{Provider: "provider1", Keyword: "docker"})
	assert.Equal(t, 0, combined.Total, "filters combine with the keyword")
}
