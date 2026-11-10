package xmlprovider

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

type fakeGetter struct{ body []byte }

func (f fakeGetter) Get(context.Context, string) ([]byte, error) { return f.body, nil }

func TestFetchFixture(t *testing.T) {
	data, err := os.ReadFile("../../../test/testdata/provider2.xml")
	require.NoError(t, err)
	p := New("provider2", "http://example", fakeGetter{body: data}, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))

	feed, err := p.Fetch(context.Background())
	contents := feed.Contents

	require.NoError(t, err)
	require.Len(t, contents, 4)

	video := contents[0]
	assert.Equal(t, "v1", video.ExternalID)
	assert.Equal(t, "Introduction to Docker", video.Title)
	assert.Equal(t, domain.ContentTypeVideo, video.Type)
	assert.Equal(t, domain.Metrics{Views: 22000, Likes: 1800, DurationSec: 1515}, video.Metrics)
	assert.Equal(t, []string{"devops", "containers"}, video.Tags)
	assert.True(t, time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC).Equal(video.PublishedAt))

	article := contents[3]
	assert.Equal(t, "a1", article.ExternalID)
	assert.Equal(t, domain.ContentTypeText, article.Type, "article must map to text")
	assert.Equal(t, domain.Metrics{ReadingTime: 8, Reactions: 450, Comments: 25}, article.Metrics)
	assert.NotEmpty(t, article.RawPayload)
}

func TestParseSkipsInvalidItems(t *testing.T) {
	data := []byte(`<feed><items>
		<item><id>ok</id><headline>Valid</headline><type>video</type><stats><views>1</views><duration>1:00</duration></stats><publication_date>2024-01-01</publication_date></item>
		<item><id>bad</id><headline>X</headline><type>video</type><stats><duration>abc</duration></stats><publication_date>2024-01-01</publication_date></item>
		<item><id>nodate</id><headline>X</headline><type>video</type></item>
	</items></feed>`)

	contents, skipped, err := Parse("p", data)

	require.NoError(t, err)
	require.Len(t, contents, 1)
	assert.Equal(t, "ok", contents[0].ExternalID)
	assert.Len(t, skipped, 2)
}

func TestParseMalformedPayload(t *testing.T) {
	_, _, err := Parse("p", []byte(`<feed><items>`))
	assert.Error(t, err)
}
