package providers

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

type fakeRepo struct {
	created domain.Provider
	patch   domain.ProviderPatch
}

func (f *fakeRepo) List(context.Context) ([]domain.Provider, error) { return nil, nil }
func (f *fakeRepo) Get(context.Context, int64) (domain.Provider, error) {
	return domain.Provider{}, nil
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

type countingInvalidator struct{ n int }

func (c *countingInvalidator) Invalidate(context.Context) error { c.n++; return nil }

func newService(repo Repository) (*Service, *countingInvalidator) {
	inv := &countingInvalidator{}
	return NewService(repo, inv, slog.New(slog.NewTextHandler(io.Discard, nil))), inv
}

func ptr[T any](v T) *T { return &v }

func TestCreate(t *testing.T) {
	repo := &fakeRepo{}
	svc, _ := newService(repo)

	p, err := svc.Create(context.Background(), CreateInput{Name: " My-Feed ", URL: " https://example.com/feed ", Format: "XML"})

	require.NoError(t, err)
	assert.Equal(t, int64(3), p.ID)
	assert.Equal(t, domain.Provider{Name: "my-feed", URL: "https://example.com/feed", Format: domain.ProviderFormatXML, Enabled: true}, repo.created)

	_, err = svc.Create(context.Background(), CreateInput{Name: "off", URL: "http://x.io", Format: "json", Enabled: ptr(false)})
	require.NoError(t, err)
	assert.False(t, repo.created.Enabled)
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newService(&fakeRepo{})

	for name, in := range map[string]CreateInput{
		"empty name":       {Name: "", URL: "https://x.io", Format: "json"},
		"name with spaces": {Name: "my feed", URL: "https://x.io", Format: "json"},
		"relative url":     {Name: "feed", URL: "/feed.json", Format: "json"},
		"ftp url":          {Name: "feed", URL: "ftp://x.io/feed", Format: "json"},
		"unknown format":   {Name: "feed", URL: "https://x.io", Format: "csv"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), in)
			assert.ErrorIs(t, err, domain.ErrInvalidInput)
		})
	}
}

func TestUpdate(t *testing.T) {
	repo := &fakeRepo{}
	svc, inv := newService(repo)

	_, err := svc.Update(context.Background(), 1, UpdateInput{Enabled: ptr(false), Format: ptr("JSON")})
	require.NoError(t, err)
	assert.Nil(t, repo.patch.URL)
	assert.Equal(t, domain.ProviderFormatJSON, *repo.patch.Format)
	assert.False(t, *repo.patch.Enabled)
	assert.Equal(t, 1, inv.n, "toggling a provider invalidates cached searches")

	_, err = svc.Update(context.Background(), 1, UpdateInput{URL: ptr("https://y.io")})
	require.NoError(t, err)
	assert.Equal(t, 1, inv.n, "a URL change does not affect search results until the next sync")

	_, err = svc.Update(context.Background(), 1, UpdateInput{URL: ptr("not a url")})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}
