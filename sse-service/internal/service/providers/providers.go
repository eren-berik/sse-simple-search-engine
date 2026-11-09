// Package providers manages the configured content providers.
package providers

import (
	"context"
	"log/slog"
	"strings"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

// Repository stores provider configurations.
type Repository interface {
	List(ctx context.Context) ([]domain.Provider, error)
	Get(ctx context.Context, id int64) (domain.Provider, error)
	Create(ctx context.Context, p domain.Provider) (domain.Provider, error)
	Update(ctx context.Context, id int64, patch domain.ProviderPatch) (domain.Provider, error)
	Delete(ctx context.Context, id int64) error
}

// Detector finds out which format a provider URL serves.
type Detector interface {
	Detect(ctx context.Context, url string) (domain.ProviderFormat, error)
}

// Invalidator drops derived caches; renaming, toggling or deleting a provider changes search results.
type Invalidator interface {
	Invalidate(ctx context.Context) error
}

type CreateInput struct {
	Name    string
	URL     string
	Format  string // empty or "auto" detects the format from the URL
	Enabled *bool  // defaults to true
}

type UpdateInput struct {
	Name    *string
	URL     *string
	Format  *string // "auto" detects; when only the URL changes the format is detected again
	Enabled *bool
}

// formatAuto asks for the format to be detected from the provider's response.
const formatAuto = "auto"

type Service struct {
	repo        Repository
	detector    Detector
	invalidator Invalidator
	log         *slog.Logger
}

func NewService(repo Repository, detector Detector, inv Invalidator, log *slog.Logger) *Service {
	return &Service{repo: repo, detector: detector, invalidator: inv, log: log}
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
		Name:    normalizeName(in.Name),
		URL:     strings.TrimSpace(in.URL),
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	if err := domain.ValidateProviderName(p.Name); err != nil {
		return domain.Provider{}, err
	}
	if err := domain.ValidateProviderURL(p.URL); err != nil {
		return domain.Provider{}, err
	}
	format, err := s.resolveFormat(ctx, in.Format, p.URL)
	if err != nil {
		return domain.Provider{}, err
	}
	p.Format = format
	return s.repo.Create(ctx, p)
}

// Update changes a provider's name, URL, format or enabled flag.
func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (domain.Provider, error) {
	var patch domain.ProviderPatch
	if in.Name != nil {
		name := normalizeName(*in.Name)
		if err := domain.ValidateProviderName(name); err != nil {
			return domain.Provider{}, err
		}
		patch.Name = &name
	}

	url := ""
	if in.URL != nil {
		url = strings.TrimSpace(*in.URL)
		if err := domain.ValidateProviderURL(url); err != nil {
			return domain.Provider{}, err
		}
		patch.URL = &url
	}

	// A new URL may serve a different format, so detect it unless one is given explicitly.
	switch {
	case in.Format != nil:
		if url == "" && isAuto(*in.Format) {
			current, err := s.repo.Get(ctx, id)
			if err != nil {
				return domain.Provider{}, err
			}
			url = current.URL
		}
		f, err := s.resolveFormat(ctx, *in.Format, url)
		if err != nil {
			return domain.Provider{}, err
		}
		patch.Format = &f
	case in.URL != nil:
		f, err := s.resolveFormat(ctx, formatAuto, url)
		if err != nil {
			return domain.Provider{}, err
		}
		patch.Format = &f
	}
	patch.Enabled = in.Enabled

	p, err := s.repo.Update(ctx, id, patch)
	if err != nil {
		return domain.Provider{}, err
	}
	// Search results show provider names and hide disabled providers, so cached pages are stale.
	// URL and format changes only take effect with the next sync.
	if in.Name != nil || in.Enabled != nil {
		s.invalidate(ctx)
	}
	return p, nil
}

// Delete removes a provider and everything ingested from it.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// resolveFormat parses an explicit format or, for an empty or "auto" value, detects it from url.
func (s *Service) resolveFormat(ctx context.Context, format, url string) (domain.ProviderFormat, error) {
	if isAuto(format) {
		return s.detector.Detect(ctx, url)
	}
	return domain.ParseProviderFormat(format)
}

func (s *Service) invalidate(ctx context.Context) {
	if err := s.invalidator.Invalidate(ctx); err != nil {
		s.log.WarnContext(ctx, "cache invalidation failed; entries will expire via TTL", "error", err)
	}
}

func isAuto(format string) bool {
	f := strings.ToLower(strings.TrimSpace(format))
	return f == "" || f == formatAuto
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
