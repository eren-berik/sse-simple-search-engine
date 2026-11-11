//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/jsonprovider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/xmlprovider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/scoring"
)

// newTestRepo creates a throwaway database on the server in TEST_DATABASE_URL, migrates it
// and drops it when the test ends, so tests never touch application data.
func newTestRepo(t *testing.T) (*ContentRepository, *pgxpool.Pool) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close(context.Background()) })

	name := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	u, err := url.Parse(base)
	require.NoError(t, err)
	u.Path = "/" + name

	pool, err := Connect(ctx, u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
	})

	require.NoError(t, Migrate(ctx, pool))
	return NewContentRepository(pool), pool
}

// seed stores both provider fixtures, scored as of fixedNow.
// seed stores both fixtures and returns how many rows were inserted or changed.
func seed(t *testing.T, repo *ContentRepository) int {
	t.Helper()
	scorer := scoring.New(scoring.DefaultConfig(), func() time.Time { return fixedNow })

	jsonData, err := os.ReadFile("../../../test/testdata/provider1.json")
	require.NoError(t, err)
	xmlData, err := os.ReadFile("../../../test/testdata/provider2.xml")
	require.NoError(t, err)

	p1, _, err := jsonprovider.Parse("provider1", jsonData)
	require.NoError(t, err)
	p2, _, err := xmlprovider.Parse("provider2", xmlData)
	require.NoError(t, err)

	all := append(p1, p2...)
	for i := range all {
		all[i].Score = scorer.Final(all[i])
		all[i].ScoreUpdatedAt = fixedNow
	}
	n, err := repo.UpsertContents(context.Background(), all)
	require.NoError(t, err)
	return n
}

var fixedNow = time.Date(2024, 3, 16, 0, 0, 0, 0, time.UTC)

func titles(res domain.SearchResult) []string {
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, h.Content.Title)
	}
	return out
}

func search(t *testing.T, repo *ContentRepository, q domain.SearchQuery) domain.SearchResult {
	t.Helper()
	require.NoError(t, q.Normalize())
	res, err := repo.Search(context.Background(), q)
	require.NoError(t, err)
	return res
}

func TestUpsertIsIdempotentAndKeysByProvider(t *testing.T) {
	repo, pool := newTestRepo(t)
	ctx := context.Background()
	assert.Equal(t, 8, seed(t, repo))
	assert.Zero(t, seed(t, repo), "identical items are not written again")

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM contents`).Scan(&count))
	assert.Equal(t, 8, count, "v1..v3 exist in both providers and must be stored separately")

	// An update changes metrics in place.
	changed, err := repo.UpsertContents(ctx, []domain.Content{{
		Provider: "provider1", ExternalID: "v1", Title: "Go Programming Tutorial (updated)", Type: domain.ContentTypeVideo,
		Metrics: domain.Metrics{Views: 1}, PublishedAt: fixedNow, Score: 1, ScoreUpdatedAt: fixedNow,
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	var title string
	var views int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT title, views FROM contents WHERE provider = 'provider1' AND external_id = 'v1'`).Scan(&title, &views))
	assert.Equal(t, "Go Programming Tutorial (updated)", title)
	assert.Equal(t, int64(1), views)
}

func TestSearchPopularityOrderAndPagination(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)

	all := search(t, repo, domain.SearchQuery{PerPage: 100})
	require.Equal(t, 8, all.Total)
	for i := 1; i < len(all.Hits); i++ {
		assert.GreaterOrEqual(t, all.Hits[i-1].Content.Score, all.Hits[i].Content.Score, "sorted by score desc")
	}
	assert.Equal(t, "Clean Architecture in Go", all.Hits[0].Content.Title, "the article's engagement score dominates")

	page := search(t, repo, domain.SearchQuery{Page: 3, PerPage: 3})
	assert.Equal(t, 8, page.Total)
	assert.Len(t, page.Hits, 2)
	assert.Equal(t, 3, page.TotalPages())

	beyond := search(t, repo, domain.SearchQuery{Page: 10, PerPage: 3})
	assert.Empty(t, beyond.Hits)
	assert.Equal(t, 8, beyond.Total)
}

func TestSearchKeywordAndType(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)

	goRes := search(t, repo, domain.SearchQuery{Keyword: "go"})
	assert.Equal(t, domain.SortRelevance, goRes.Sort)
	assert.ElementsMatch(t, []string{
		"Go Programming Tutorial", "Advanced Go Concurrency Patterns", "Building RESTful APIs with Go",
		"Go Testing Best Practices", "Clean Architecture in Go",
	}, titles(goRes))
	for i := 1; i < len(goRes.Hits); i++ {
		assert.GreaterOrEqual(t, goRes.Hits[i-1].Relevance, goRes.Hits[i].Relevance, "sorted by relevance desc")
	}

	text := search(t, repo, domain.SearchQuery{Keyword: "go", Type: domain.ContentTypeText})
	assert.Equal(t, []string{"Clean Architecture in Go"}, titles(text))

	byTag := search(t, repo, domain.SearchQuery{Keyword: "devops"})
	assert.Len(t, byTag.Hits, 3, "tags are searchable")

	partial := search(t, repo, domain.SearchQuery{Keyword: "kubern"})
	assert.Equal(t, []string{"Kubernetes for Beginners"}, titles(partial), "substring match on title")

	wildcard := search(t, repo, domain.SearchQuery{Keyword: "%"})
	assert.Empty(t, wildcard.Hits, "LIKE wildcards in user input are escaped")

	none := search(t, repo, domain.SearchQuery{Keyword: "rust"})
	assert.Equal(t, 0, none.Total)
}

func TestGetByID(t *testing.T) {
	repo, pool := newTestRepo(t)
	seed(t, repo)
	ctx := context.Background()

	var id int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM contents WHERE provider = 'provider2' AND external_id = 'a1'`).Scan(&id))

	c, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Clean Architecture in Go", c.Title)
	assert.Equal(t, domain.ContentTypeText, c.Type)
	assert.Equal(t, domain.Metrics{ReadingTime: 8, Reactions: 450, Comments: 25}, c.Metrics)
	assert.Equal(t, []string{"programming", "architecture"}, c.Tags)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(c.RawPayload, &raw))
	assert.Equal(t, "Clean Architecture in Go", raw["headline"], "original provider payload is preserved")

	_, err = repo.GetByID(ctx, 999999)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestRecalculateScoresOnlyTouchesStaleRows(t *testing.T) {
	repo, pool := newTestRepo(t)
	seed(t, repo)
	ctx := context.Background()

	n, err := repo.RecalculateScores(ctx, fixedNow, func(domain.Content) float64 { return 0 })
	require.NoError(t, err)
	assert.Equal(t, 0, n, "rows scored at fixedNow are not stale")

	n, err = repo.RecalculateScores(ctx, fixedNow.Add(time.Second), func(domain.Content) float64 { return 42 })
	require.NoError(t, err)
	assert.Equal(t, 8, n)

	var distinct int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(DISTINCT score) FROM contents WHERE score = 42`).Scan(&distinct))
	assert.Equal(t, 1, distinct)
}

func TestSyncRuns(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()

	older := domain.SyncRun{Provider: "provider1", StartedAt: fixedNow, FinishedAt: fixedNow, Status: domain.SyncStatusFailed, Error: "timeout"}
	newer := domain.SyncRun{Provider: "provider1", StartedAt: fixedNow.Add(time.Hour), FinishedAt: fixedNow.Add(time.Hour), Status: domain.SyncStatusSuccess, Fetched: 4, Upserted: 4}
	other := domain.SyncRun{Provider: "provider2", StartedAt: fixedNow, FinishedAt: fixedNow, Status: domain.SyncStatusSuccess, Fetched: 4, Upserted: 4}
	for _, r := range []domain.SyncRun{older, newer, other} {
		require.NoError(t, repo.RecordSyncRun(ctx, r))
	}

	runs, err := repo.LatestSyncRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, "provider1", runs[0].Provider)
	assert.Equal(t, domain.SyncStatusSuccess, runs[0].Status)
	assert.Empty(t, runs[0].Error)
	assert.Equal(t, "provider2", runs[1].Provider)
}

func TestCountContents(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)

	counts, err := repo.CountContents(context.Background())

	require.NoError(t, err)
	assert.ElementsMatch(t, []domain.ContentCount{
		{Provider: "provider1", Type: domain.ContentTypeVideo, Count: 4},
		{Provider: "provider2", Type: domain.ContentTypeVideo, Count: 3},
		{Provider: "provider2", Type: domain.ContentTypeText, Count: 1},
	}, counts)
}

func TestSearchByTagAndDate(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		require.NoError(t, err)
		return d
	}

	res := search(t, repo, domain.SearchQuery{Tag: "devops"})
	assert.ElementsMatch(t, []string{"Introduction to Docker", "Kubernetes for Beginners", "CI/CD Pipeline Implementation"}, titles(res))

	// Both ends are inclusive days: 14:30 on the 14th is in, the 15th is out.
	res = search(t, repo, domain.SearchQuery{PublishedFrom: day("2024-03-14"), PublishedTo: day("2024-03-14")})
	assert.ElementsMatch(t, []string{"Advanced Go Concurrency Patterns", "Kubernetes for Beginners", "Clean Architecture in Go"}, titles(res))

	res = search(t, repo, domain.SearchQuery{Tag: "programming", PublishedFrom: day("2024-03-14")})
	assert.ElementsMatch(t, []string{"Go Programming Tutorial", "Advanced Go Concurrency Patterns", "Clean Architecture in Go"}, titles(res),
		"filters combine; an open end is unbounded")

	res = search(t, repo, domain.SearchQuery{Keyword: "go", Tag: "testing"})
	assert.Equal(t, []string{"Go Testing Best Practices"}, titles(res))

	assert.Empty(t, titles(search(t, repo, domain.SearchQuery{Tag: "nope"})))
}

func TestMarkRemovedHidesAndRestores(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)
	ctx := context.Background()
	docker := func() []string {
		return titles(search(t, repo, domain.SearchQuery{Keyword: "docker"}))
	}
	require.Equal(t, []string{"Introduction to Docker"}, docker())

	// provider2 now returns everything except the Docker video (external id v1).
	removed, err := repo.MarkRemoved(ctx, "provider2", []string{"v2", "v3", "a1"}, fixedNow)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	assert.Empty(t, docker(), "a removed item is not searchable")
	all := search(t, repo, domain.SearchQuery{PerPage: 100})
	assert.Equal(t, 7, all.Total)
	counts, err := repo.CountContents(ctx)
	require.NoError(t, err)
	var provider2 int64
	for _, c := range counts {
		if c.Provider == "provider2" {
			provider2 += c.Count
		}
	}
	assert.Equal(t, int64(3), provider2, "removed items are not counted")

	again, err := repo.MarkRemoved(ctx, "provider2", []string{"v2", "v3", "a1"}, fixedNow)
	require.NoError(t, err)
	assert.Zero(t, again, "already removed items are not counted twice")
	other, err := repo.MarkRemoved(ctx, "provider1", []string{"v1", "v2", "v3", "v4"}, fixedNow)
	require.NoError(t, err)
	assert.Zero(t, other, "only the given provider is affected")

	// The provider returns it again: the upsert lists it again (and counts it as a change).
	assert.Equal(t, 1, seed(t, repo))
	assert.Equal(t, []string{"Introduction to Docker"}, docker())
}

func TestSyncRunRecordsRemovals(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.RecordSyncRun(ctx, domain.SyncRun{
		Provider: "provider2", StartedAt: fixedNow, FinishedAt: fixedNow, Status: domain.SyncStatusSuccess, Fetched: 3, Upserted: 3, Removed: 1,
	}))

	runs, err := repo.LatestSyncRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, 1, runs[0].Removed)
}

func TestUnchangedItemsAreNotRewritten(t *testing.T) {
	repo, pool := newTestRepo(t)
	ctx := context.Background()
	require.Equal(t, 8, seed(t, repo))
	updatedAt := func() time.Time {
		var ts time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT updated_at FROM contents WHERE provider = 'provider2' AND external_id = 'v1'`).Scan(&ts))
		return ts
	}
	before := updatedAt()

	assert.Zero(t, seed(t, repo))
	assert.Equal(t, before, updatedAt(), "the row was not touched")
}

func TestRecalculateScoresWritesOnlyChangedScores(t *testing.T) {
	repo, pool := newTestRepo(t)
	ctx := context.Background()
	require.Equal(t, 8, seed(t, repo))
	later := fixedNow.Add(time.Hour)

	// Nothing about the scores changes: nothing is written.
	n, err := repo.RecalculateScores(ctx, later, func(c domain.Content) float64 { return c.Score })
	require.NoError(t, err)
	assert.Zero(t, n)

	var original float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT score FROM contents WHERE provider = 'provider1' AND external_id = 'v1'`).Scan(&original))

	// One item's score changes (e.g. it left the "within a week" tier).
	n, err = repo.RecalculateScores(ctx, later, func(c domain.Content) float64 {
		if c.Provider == "provider1" && c.ExternalID == "v1" {
			return c.Score - 5
		}
		return c.Score
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var score float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT score FROM contents WHERE provider = 'provider1' AND external_id = 'v1'`).Scan(&score))
	assert.InDelta(t, original-5, score, 1e-9)
}

func TestRelevanceMatchIsRelativeToTheBestResult(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)

	// "programming" is in one title (and its tags) and in four other items' tags only.
	all := search(t, repo, domain.SearchQuery{Keyword: "programming", PerPage: 100})
	require.Len(t, all.Hits, 5)
	assert.Equal(t, "Go Programming Tutorial", all.Hits[0].Content.Title)
	assert.InDelta(t, 1.0, all.Hits[0].Match, 1e-9, "the best result is the reference")
	for i, h := range all.Hits {
		assert.InDelta(t, all.Hits[0].Relevance*h.Match, h.Relevance, 1e-9, "match = relevance / best")
		if i > 0 {
			assert.LessOrEqual(t, h.Match, all.Hits[i-1].Match)
		}
	}

	// The reference is the whole search, not the page: page 2 does not start at 100%.
	second := search(t, repo, domain.SearchQuery{Keyword: "programming", Page: 2, PerPage: 2})
	require.NotEmpty(t, second.Hits)
	assert.InDelta(t, all.Hits[2].Match, second.Hits[0].Match, 1e-9)
	assert.Less(t, second.Hits[0].Match, 1.0)

	popular := search(t, repo, domain.SearchQuery{Keyword: "programming", Sort: domain.SortPopularity})
	assert.Zero(t, popular.Hits[0].Match, "only relevance sorting computes it")
}

func TestRelevanceRewardsRealMatchesOnly(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)
	matches := func(q string) map[string]float64 {
		out := map[string]float64{}
		for _, h := range search(t, repo, domain.SearchQuery{Keyword: q, PerPage: 100}).Hits {
			out[h.Content.Title] = h.Match
		}
		return out
	}

	// Tag-only matches are equal, whatever letters their titles happen to share with the query,
	// and rank below the title match but well above a third of it.
	p := matches("programming")
	tagOnly := p["Go Testing Best Practices"]
	for _, title := range []string{"Clean Architecture in Go", "Advanced Go Concurrency Patterns", "Building RESTful APIs with Go"} {
		assert.InDelta(t, tagOnly, p[title], 1e-9, title)
	}
	assert.Greater(t, tagOnly, 0.6)
	assert.Less(t, tagOnly, 1.0)

	// Same kind of match, same percentage; ties are ordered by popularity.
	res := search(t, repo, domain.SearchQuery{Keyword: "go", PerPage: 100})
	for _, h := range res.Hits {
		assert.InDelta(t, 1.0, h.Match, 1e-9, h.Content.Title)
	}
	assert.Equal(t, "Clean Architecture in Go", res.Hits[0].Content.Title, "the most popular of equal matches comes first")

	// A match on part of a word only still ranks below whole-word matches.
	mixed := search(t, repo, domain.SearchQuery{Keyword: "test", PerPage: 100})
	require.NotEmpty(t, mixed.Hits)
	assert.Equal(t, "Go Testing Best Practices", mixed.Hits[0].Content.Title)
}

func TestExplainRelevanceMatchesTheRanking(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)
	ctx := context.Background()
	ranked := map[string]domain.SearchHit{}
	for _, h := range search(t, repo, domain.SearchQuery{Keyword: "programming", PerPage: 100}).Hits {
		ranked[h.Content.Title] = h
	}

	for title, want := range map[string]domain.TermMatch{
		"Go Programming Tutorial":   {Term: "programming", InTitle: 1, InTags: 1},
		"Go Testing Best Practices": {Term: "programming", InTags: 1},
	} {
		hit := ranked[title]
		e, err := repo.ExplainRelevance(ctx, hit.Content.ID, "programming")
		require.NoError(t, err)
		assert.True(t, e.Matches, title)
		assert.True(t, e.FullText, title)
		assert.Equal(t, []domain.TermMatch{want}, e.Terms, title)
		assert.InDelta(t, hit.Relevance, e.Relevance, 1e-9, "%s: the explanation is the ranking's relevance", title)

		// The steps reproduce PostgreSQL's ts_rank.
		var sum float64
		for _, s := range e.Steps {
			sum += s.Value
		}
		assert.InDelta(t, e.Rank, sum/domain.RankNormalizer, 1e-6, title)
	}

	// A partial match is explained by word similarity.
	kube := search(t, repo, domain.SearchQuery{Keyword: "kuberne"}).Hits[0]
	e, err := repo.ExplainRelevance(ctx, kube.Content.ID, "kuberne")
	require.NoError(t, err)
	assert.True(t, e.Matches)
	assert.False(t, e.FullText)
	assert.Empty(t, e.Steps)
	assert.InDelta(t, kube.Relevance, e.PartialFactor*e.WordSimilarity, 1e-6)

	// Several words: where each is found, no single-word steps.
	gt := search(t, repo, domain.SearchQuery{Keyword: "go testing"}).Hits[0]
	e, err = repo.ExplainRelevance(ctx, gt.Content.ID, "go testing")
	require.NoError(t, err)
	assert.Equal(t, []string{"go", "testing"}, []string{e.Terms[0].Term, e.Terms[1].Term}, "in query order")
	assert.Empty(t, e.Steps)

	// An item that does not match the search.
	docker := search(t, repo, domain.SearchQuery{Keyword: "docker"}).Hits[0]
	e, err = repo.ExplainRelevance(ctx, docker.Content.ID, "programming")
	require.NoError(t, err)
	assert.False(t, e.Matches)

	_, err = repo.ExplainRelevance(ctx, 999999, "go")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSearchReportsTheBestRelevance(t *testing.T) {
	repo, _ := newTestRepo(t)
	seed(t, repo)

	first := search(t, repo, domain.SearchQuery{Keyword: "programming", PerPage: 2})
	second := search(t, repo, domain.SearchQuery{Keyword: "programming", Page: 2, PerPage: 2})
	assert.InDelta(t, first.Hits[0].Relevance, first.BestRelevance, 1e-9)
	assert.InDelta(t, first.BestRelevance, second.BestRelevance, 1e-9, "the same reference on every page")
}
