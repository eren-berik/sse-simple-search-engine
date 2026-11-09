package jsonprovider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/internal/domain"
)

type fakeGetter struct {
	body []byte
	err  error
}

func (f fakeGetter) Get(context.Context, string) ([]byte, error) { return f.body, f.err }

func TestParseFixture(t *testing.T) {
	data, err := os.ReadFile("../../../test/testdata/provider1.json")
	require.NoError(t, err)

	contents, skipped, err := Parse("provider1", data)

	require.NoError(t, err)
	assert.Empty(t, skipped)
	require.Len(t, contents, 4)

	first := contents[0]
	assert.Equal(t, "provider1", first.Provider)
	assert.Equal(t, "v1", first.ExternalID)
	assert.Equal(t, "Go Programming Tutorial", first.Title)
	assert.Equal(t, domain.ContentTypeVideo, first.Type)
	assert.Equal(t, domain.Metrics{Views: 15000, Likes: 1200, DurationSec: 930}, first.Metrics)
	assert.Equal(t, []string{"programming", "tutorial"}, first.Tags)
	assert.True(t, time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC).Equal(first.PublishedAt))
	assert.JSONEq(t, `{"id":"v1","title":"Go Programming Tutorial","type":"video",
		"metrics":{"views":15000,"likes":1200,"duration":"15:30","reading_time":0,"reactions":0,"comments":0},
		"published_at":"2024-03-15T10:00:00Z","tags":["programming","tutorial"]}`, string(first.RawPayload))
}

func TestParseSkipsInvalidItems(t *testing.T) {
	data := []byte(`{"contents":[
		{"id":"ok","title":"Valid","type":"article","metrics":{"reading_time":5,"reactions":10},"published_at":"2024-01-01T00:00:00Z"},
		{"id":"bad-type","title":"X","type":"podcast","published_at":"2024-01-01T00:00:00Z"},
		{"id":"bad-date","title":"X","type":"video","published_at":"yesterday"},
		{"id":"","title":"X","type":"video","published_at":"2024-01-01T00:00:00Z"}
	]}`)

	contents, skipped, err := Parse("p", data)

	require.NoError(t, err)
	require.Len(t, contents, 1)
	assert.Equal(t, domain.ContentTypeText, contents[0].Type)
	assert.Len(t, skipped, 3)
}

func TestParseMalformedPayload(t *testing.T) {
	_, _, err := Parse("p", []byte(`{not json`))
	assert.Error(t, err)
}

func TestFetchPropagatesClientError(t *testing.T) {
	boom := errors.New("boom")
	p := New("p", "http://example", fakeGetter{err: boom}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := p.Fetch(context.Background())

	assert.ErrorIs(t, err, boom)
}
