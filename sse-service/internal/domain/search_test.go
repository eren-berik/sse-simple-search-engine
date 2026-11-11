package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchQueryNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      SearchQuery
		want    SearchQuery
		wantErr bool
	}{
		{
			name: "defaults without keyword",
			in:   SearchQuery{},
			want: SearchQuery{Sort: SortPopularity, Page: 1, PerPage: DefaultPerPage},
		},
		{
			name: "defaults to popularity with keyword and collapses whitespace",
			in:   SearchQuery{Keyword: "  go   concurrency "},
			want: SearchQuery{Keyword: "go concurrency", Sort: SortPopularity, Page: 1, PerPage: DefaultPerPage},
		},
		{
			name: "relevance with keyword",
			in:   SearchQuery{Keyword: "go", Sort: SortRelevance},
			want: SearchQuery{Keyword: "go", Sort: SortRelevance, Page: 1, PerPage: DefaultPerPage},
		},
		{
			name: "relevance without keyword falls back to popularity",
			in:   SearchQuery{Sort: SortRelevance},
			want: SearchQuery{Sort: SortPopularity, Page: 1, PerPage: DefaultPerPage},
		},
		{
			name: "type is case-insensitive",
			in:   SearchQuery{Type: "VIDEO", Page: 2, PerPage: 5},
			want: SearchQuery{Type: ContentTypeVideo, Sort: SortPopularity, Page: 2, PerPage: 5},
		},
		{
			name: "tag is trimmed and lowercased",
			in:   SearchQuery{Tag: "  DevOps "},
			want: SearchQuery{Tag: "devops", Sort: SortPopularity, Page: 1, PerPage: DefaultPerPage},
		},
		{
			name: "single-day date range",
			in:   SearchQuery{PublishedFrom: day("2024-03-14"), PublishedTo: day("2024-03-14")},
			want: SearchQuery{PublishedFrom: day("2024-03-14"), PublishedTo: day("2024-03-14"), Sort: SortPopularity, Page: 1, PerPage: DefaultPerPage},
		},
		{name: "date range reversed", in: SearchQuery{PublishedFrom: day("2024-03-15"), PublishedTo: day("2024-03-14")}, wantErr: true},
		{name: "tag too long", in: SearchQuery{Tag: strings.Repeat("a", MaxTagLength+1)}, wantErr: true},
		{name: "unknown type", in: SearchQuery{Type: "podcast"}, wantErr: true},
		{name: "unknown sort", in: SearchQuery{Sort: "date"}, wantErr: true},
		{name: "negative page", in: SearchQuery{Page: -1}, wantErr: true},
		{name: "per_page too large", in: SearchQuery{PerPage: MaxPerPage + 1}, wantErr: true},
		{name: "keyword too long", in: SearchQuery{Keyword: strings.Repeat("a", MaxKeywordLength+1)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.in
			err := q.Normalize()
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidInput)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, q)
		})
	}
}

func TestSearchResultTotalPages(t *testing.T) {
	assert.Equal(t, 0, SearchResult{Total: 0, PerPage: 10}.TotalPages())
	assert.Equal(t, 1, SearchResult{Total: 10, PerPage: 10}.TotalPages())
	assert.Equal(t, 2, SearchResult{Total: 11, PerPage: 10}.TotalPages())
}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRankStepsReproduceTsRank(t *testing.T) {
	// Values PostgreSQL returns for ts_rank('{0.1, 0.2, 0.8, 1.0}', ...) on a single word.
	tests := []struct {
		name  string
		term  TermMatch
		want  float64
		steps []RankStep
	}{
		{"title only", TermMatch{InTitle: 1}, 0.6079, []RankStep{{"title", 1, 1, 1}}},
		{"tags only", TermMatch{InTags: 1}, 0.4863, []RankStep{{"tags", 0.8, 1, 0.8}}},
		{"title and tags", TermMatch{InTitle: 1, InTags: 1}, 0.7295, []RankStep{{"title", 1, 1, 1}, {"tags", 0.8, 2, 0.2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps, rank := RankSteps(tt.term, 1, 0.8)
			assert.InDelta(t, tt.want, rank, 1e-4)
			assert.Equal(t, len(tt.steps), len(steps))
			for i := range tt.steps {
				assert.Equal(t, tt.steps[i].Field, steps[i].Field)
				assert.Equal(t, tt.steps[i].Position, steps[i].Position)
				assert.InDelta(t, tt.steps[i].Value, steps[i].Value, 1e-9)
			}
		})
	}
}
