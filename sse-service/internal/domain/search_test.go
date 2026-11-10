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
			name: "defaults to relevance with keyword and collapses whitespace",
			in:   SearchQuery{Keyword: "  go   concurrency "},
			want: SearchQuery{Keyword: "go concurrency", Sort: SortRelevance, Page: 1, PerPage: DefaultPerPage},
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
