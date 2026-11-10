package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

// testPage is a minimal paginated wire format: item ids plus pagination metadata.
type testPage struct {
	Items   []string `json:"items"`
	Bad     int      `json:"bad"` // items that fail conversion
	Page    int      `json:"page"`
	PerPage int      `json:"per_page"`
	Total   int      `json:"total"`
}

func parseTestPage(data []byte) (Page, error) {
	var tp testPage
	if err := json.Unmarshal(data, &tp); err != nil {
		return Page{}, err
	}
	p := Page{Number: tp.Page, PerPage: tp.PerPage, Total: tp.Total}
	for _, id := range tp.Items {
		p.Contents = append(p.Contents, domain.Content{ExternalID: id})
	}
	for range tp.Bad {
		p.Skipped = append(p.Skipped, errors.New("bad item"))
	}
	return p, nil
}

// pagedGetter serves pages by their page query parameter (absent means 1) and records the URLs.
type pagedGetter struct {
	pages     map[int]testPage
	ignore    bool // answer every request with page 1, like a static file
	failPage  int
	requested []string
}

func (g *pagedGetter) Get(_ context.Context, raw string) ([]byte, error) {
	g.requested = append(g.requested, raw)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	n := 1
	if v := u.Query().Get("page"); v != "" && !g.ignore {
		_, _ = fmt.Sscan(v, &n)
	}
	if n == g.failPage {
		return nil, errors.New("connection reset")
	}
	return json.Marshal(g.pages[n])
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func ids(f Feed) []string {
	out := make([]string, len(f.Contents))
	for i, c := range f.Contents {
		out[i] = c.ExternalID
	}
	return out
}

func TestFetchPagesFollowsPagination(t *testing.T) {
	g := &pagedGetter{pages: map[int]testPage{
		1: {Items: []string{"a", "b"}, Page: 1, PerPage: 2, Total: 5},
		2: {Items: []string{"c", "d"}, Page: 2, PerPage: 2, Total: 5},
		3: {Items: []string{"e"}, Page: 3, PerPage: 2, Total: 5},
	}}

	feed, err := FetchPages(context.Background(), g, "https://feed.io/v2/items?lang=tr", parseTestPage, 10, discard)

	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, ids(feed))
	assert.True(t, feed.Complete)
	assert.Equal(t, []string{
		"https://feed.io/v2/items?lang=tr",
		"https://feed.io/v2/items?lang=tr&page=2",
		"https://feed.io/v2/items?lang=tr&page=3",
	}, g.requested, "the first page is the configured URL; later pages keep its query")
}

func TestFetchPagesDetectsIgnoredPageParameter(t *testing.T) {
	// A static file answers page=2 with page 1 again, as the case's mock providers do.
	g := &pagedGetter{ignore: true, pages: map[int]testPage{
		1: {Items: []string{"v1", "v2"}, Page: 1, PerPage: 10, Total: 150},
	}}

	feed, err := FetchPages(context.Background(), g, "https://feed.io/items", parseTestPage, 10, discard)

	require.NoError(t, err)
	assert.Equal(t, []string{"v1", "v2"}, ids(feed), "items are not duplicated")
	assert.False(t, feed.Complete, "150 items are reported, so the feed is not known to be whole")
	assert.Len(t, g.requested, 2, "stops after the first repeated page")
}

func TestFetchPagesDetectsRepeatsWithoutPageNumbers(t *testing.T) {
	g := &pagedGetter{ignore: true, pages: map[int]testPage{
		1: {Items: []string{"a", "b"}, PerPage: 2, Total: 10},
	}}

	feed, err := FetchPages(context.Background(), g, "https://feed.io/items", parseTestPage, 10, discard)

	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ids(feed))
	assert.False(t, feed.Complete)
}

func TestFetchPagesCompletion(t *testing.T) {
	tests := []struct {
		name     string
		pages    map[int]testPage
		maxPages int
		want     []string
		complete bool
		requests int
	}{
		{
			name:  "not paginated",
			pages: map[int]testPage{1: {Items: []string{"a", "b"}}},
			want:  []string{"a", "b"}, complete: true, requests: 1,
		},
		{
			name: "without a total, a short page is the last one",
			pages: map[int]testPage{
				1: {Items: []string{"a", "b"}, Page: 1, PerPage: 2},
				2: {Items: []string{"c"}, Page: 2, PerPage: 2},
			},
			want: []string{"a", "b", "c"}, complete: true, requests: 2,
		},
		{
			name: "without a total, an empty page ends the feed",
			pages: map[int]testPage{
				1: {Items: []string{"a", "b"}, Page: 1, PerPage: 2},
				2: {Page: 2, PerPage: 2},
			},
			want: []string{"a", "b"}, complete: true, requests: 2,
		},
		{
			name: "uneven pages are followed until the total",
			pages: map[int]testPage{
				1: {Items: []string{"a"}, Page: 1, PerPage: 2, Total: 3},
				2: {Items: []string{"b", "c"}, Page: 2, PerPage: 2, Total: 3},
			},
			want: []string{"a", "b", "c"}, complete: true, requests: 2,
		},
		{
			name: "running out before the reported total is incomplete",
			pages: map[int]testPage{
				1: {Items: []string{"a", "b"}, Page: 1, PerPage: 2, Total: 100},
				2: {Page: 2, PerPage: 2, Total: 100},
			},
			want: []string{"a", "b"}, complete: false, requests: 2,
		},
		{
			name: "skipped items count towards the total",
			pages: map[int]testPage{
				1: {Items: []string{"a"}, Bad: 1, Page: 1, PerPage: 2, Total: 2},
			},
			want: []string{"a"}, complete: true, requests: 1,
		},
		{
			name: "page limit",
			pages: map[int]testPage{
				1: {Items: []string{"a"}, Page: 1, PerPage: 1, Total: 5},
				2: {Items: []string{"b"}, Page: 2, PerPage: 1, Total: 5},
			},
			maxPages: 2,
			want:     []string{"a", "b"}, complete: false, requests: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &pagedGetter{pages: tt.pages}
			feed, err := FetchPages(context.Background(), g, "https://feed.io/items", parseTestPage, tt.maxPages, discard)
			require.NoError(t, err)
			assert.Equal(t, tt.want, ids(feed))
			assert.Equal(t, tt.complete, feed.Complete)
			assert.Len(t, g.requested, tt.requests)
		})
	}
}

func TestFetchPagesFailsOnAnyPage(t *testing.T) {
	g := &pagedGetter{failPage: 2, pages: map[int]testPage{
		1: {Items: []string{"a"}, Page: 1, PerPage: 1, Total: 3},
	}}

	_, err := FetchPages(context.Background(), g, "https://feed.io/items", parseTestPage, 10, discard)

	assert.ErrorContains(t, err, "page 2", "a partial feed is not stored")
}
