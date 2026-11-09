package providers

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

type fakeRepo struct {
	created domain.Provider
	patch   domain.ProviderPatch
	deleted int64
}

func (f *fakeRepo) List(context.Context) ([]domain.Provider, error) { return nil, nil }

func (f *fakeRepo) Get(_ context.Context, id int64) (domain.Provider, error) {
	return domain.Provider{ID: id, Name: "current", URL: "https://current.io/feed"}, nil
}

func (f *fakeRepo) Create(_ context.Context, p domain.Provider) (domain.Provider, error) {
	f.created = p
	p.ID = 3
	return p, nil
}

func (f *fakeRepo) Update(_ context.Context, id int64, patch domain.ProviderPatch) (domain.Provider, error) {
	f.patch = patch
	return domain.Provider{ID: id}, nil
}

func (f *fakeRepo) Delete(_ context.Context, id int64) error {
	if id != 1 {
		return domain.ErrNotFound
	}
	f.deleted = id
	return nil
}

// fakeDetector reports XML for URLs containing "xml" and fails for "broken".
type fakeDetector struct{ calls []string }

func (d *fakeDetector) Detect(_ context.Context, url string) (domain.ProviderFormat, error) {
	d.calls = append(d.calls, url)
	switch {
	case strings.Contains(url, "broken"):
		return "", domain.ErrInvalidInput
	case strings.Contains(url, "xml"):
		return domain.ProviderFormatXML, nil
	default:
		return domain.ProviderFormatJSON, nil
	}
}

type countingInvalidator struct{ n int }

func (c *countingInvalidator) Invalidate(context.Context) error { c.n++; return nil }

type fixture struct {
	svc      *Service
	repo     *fakeRepo
	detector *fakeDetector
	inv      *countingInvalidator
}

func newFixture() fixture {
	f := fixture{repo: &fakeRepo{}, detector: &fakeDetector{}, inv: &countingInvalidator{}}
	f.svc = NewService(f.repo, f.detector, f.inv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func ptr[T any](v T) *T { return &v }

func TestCreate(t *testing.T) {
	f := newFixture()

	p, err := f.svc.Create(context.Background(), CreateInput{Name: " My-Feed ", URL: " https://example.com/feed ", Format: "XML"})

	require.NoError(t, err)
	assert.Equal(t, int64(3), p.ID)
	assert.Equal(t, domain.Provider{Name: "my-feed", URL: "https://example.com/feed", Format: domain.ProviderFormatXML, Enabled: true}, f.repo.created)
	assert.Empty(t, f.detector.calls, "an explicit format is not detected")

	_, err = f.svc.Create(context.Background(), CreateInput{Name: "off", URL: "http://x.io", Format: "json", Enabled: ptr(false)})
	require.NoError(t, err)
	assert.False(t, f.repo.created.Enabled)
}

func TestCreateDetectsFormat(t *testing.T) {
	for _, format := range []string{"", "auto", " AUTO "} {
		f := newFixture()

		_, err := f.svc.Create(context.Background(), CreateInput{Name: "feed", URL: "https://x.io/feed.xml", Format: format})

		require.NoError(t, err, format)
		assert.Equal(t, domain.ProviderFormatXML, f.repo.created.Format, format)
		assert.Equal(t, []string{"https://x.io/feed.xml"}, f.detector.calls)
	}

	f := newFixture()
	_, err := f.svc.Create(context.Background(), CreateInput{Name: "feed", URL: "https://broken.io"})
	assert.ErrorIs(t, err, domain.ErrInvalidInput, "undetectable formats are rejected")
}

func TestCreateValidation(t *testing.T) {
	f := newFixture()

	for name, in := range map[string]CreateInput{
		"empty name":       {Name: "", URL: "https://x.io", Format: "json"},
		"name with spaces": {Name: "my feed", URL: "https://x.io", Format: "json"},
		"relative url":     {Name: "feed", URL: "/feed.json", Format: "json"},
		"ftp url":          {Name: "feed", URL: "ftp://x.io/feed", Format: "json"},
		"unknown format":   {Name: "feed", URL: "https://x.io", Format: "csv"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.svc.Create(context.Background(), in)
			assert.ErrorIs(t, err, domain.ErrInvalidInput)
		})
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("toggle invalidates cached searches", func(t *testing.T) {
		f := newFixture()
		_, err := f.svc.Update(ctx, 1, UpdateInput{Enabled: ptr(false)})
		require.NoError(t, err)
		assert.False(t, *f.repo.patch.Enabled)
		assert.Equal(t, 1, f.inv.n)
	})

	t.Run("rename is normalized, validated and invalidates", func(t *testing.T) {
		f := newFixture()
		_, err := f.svc.Update(ctx, 1, UpdateInput{Name: ptr(" New-Name ")})
		require.NoError(t, err)
		assert.Equal(t, "new-name", *f.repo.patch.Name)
		assert.Equal(t, 1, f.inv.n)

		_, err = f.svc.Update(ctx, 1, UpdateInput{Name: ptr("bad name")})
		assert.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("new url without format detects the format", func(t *testing.T) {
		f := newFixture()
		_, err := f.svc.Update(ctx, 1, UpdateInput{URL: ptr("https://y.io/feed.xml")})
		require.NoError(t, err)
		assert.Equal(t, "https://y.io/feed.xml", *f.repo.patch.URL)
		assert.Equal(t, domain.ProviderFormatXML, *f.repo.patch.Format)
		assert.Equal(t, 0, f.inv.n, "url changes take effect with the next sync")
	})

	t.Run("explicit format is kept", func(t *testing.T) {
		f := newFixture()
		_, err := f.svc.Update(ctx, 1, UpdateInput{URL: ptr("https://y.io/feed.xml"), Format: ptr("json")})
		require.NoError(t, err)
		assert.Equal(t, domain.ProviderFormatJSON, *f.repo.patch.Format)
		assert.Empty(t, f.detector.calls)
	})

	t.Run("auto format alone re-detects the current url", func(t *testing.T) {
		f := newFixture()
		_, err := f.svc.Update(ctx, 1, UpdateInput{Format: ptr("auto")})
		require.NoError(t, err)
		assert.Equal(t, []string{"https://current.io/feed"}, f.detector.calls)
		assert.Nil(t, f.repo.patch.URL)
	})

	t.Run("invalid url", func(t *testing.T) {
		f := newFixture()
		_, err := f.svc.Update(ctx, 1, UpdateInput{URL: ptr("not a url")})
		assert.ErrorIs(t, err, domain.ErrInvalidInput)
	})
}

func TestDelete(t *testing.T) {
	f := newFixture()

	require.NoError(t, f.svc.Delete(context.Background(), 1))
	assert.Equal(t, int64(1), f.repo.deleted)
	assert.Equal(t, 1, f.inv.n)

	assert.ErrorIs(t, f.svc.Delete(context.Background(), 9), domain.ErrNotFound)
	assert.Equal(t, 1, f.inv.n, "nothing to invalidate when nothing was deleted")
}
