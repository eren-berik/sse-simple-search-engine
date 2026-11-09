// Package providers manages the configured content providers.
package providers

import (
	"context"
	"strings"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

// Repository stores provider configurations.
type Repository interface {
	List(ctx context.Context) ([]domain.Provider, error)
	Get(ctx context.Context, id int64) (domain.Provider, error)
	Create(ctx context.Context, p domain.Provider) (domain.Provider, error)
	Update(ctx context.Context, id int64, patch domain.ProviderPatch) (domain.Provider, error)
}

type CreateInput struct {
	Name    string
	URL     string
	Format  string
	Enabled *bool // defaults to true
}

type UpdateInput struct {
	URL     *string
	Format  *string
	Enabled *bool
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]domain.Provider, error) {
	return s.repo.List(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (domain.Provider, error) {
	return s.repo.Get(ctx, id)
}

// Create validates and stores a new provider. Its content is fetched on the next sync.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Provider, error) {
	p := domain.Provider{
		Name:    strings.ToLower(strings.TrimSpace(in.Name)),
		URL:     strings.TrimSpace(in.URL),
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	if err := domain.ValidateProviderName(p.Name); err != nil {
		return domain.Provider{}, err
	}
	if err := domain.ValidateProviderURL(p.URL); err != nil {
		return domain.Provider{}, err
	}
	format, err := domain.ParseProviderFormat(in.Format)
	if err != nil {
		return domain.Provider{}, err
	}
	p.Format = format
	return s.repo.Create(ctx, p)
}

// Update changes a provider's URL, format or enabled flag.
func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (domain.Provider, error) {
	var patch domain.ProviderPatch
	if in.URL != nil {
		u := strings.TrimSpace(*in.URL)
		if err := domain.ValidateProviderURL(u); err != nil {
			return domain.Provider{}, err
		}
		patch.URL = &u
	}
	if in.Format != nil {
		f, err := domain.ParseProviderFormat(*in.Format)
		if err != nil {
			return domain.Provider{}, err
		}
		patch.Format = &f
	}
	patch.Enabled = in.Enabled
	return s.repo.Update(ctx, id, patch)
}
