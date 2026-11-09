// Package jsonprovider integrates the JSON content provider.
package jsonprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
)

type response struct {
	Contents   []item `json:"contents"`
	Pagination struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	} `json:"pagination"`
}

type item struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Metrics struct {
		Views       int64  `json:"views"`
		Likes       int64  `json:"likes"`
		Duration    string `json:"duration"`
		ReadingTime int    `json:"reading_time"`
		Reactions   int64  `json:"reactions"`
		Comments    int64  `json:"comments"`
	} `json:"metrics"`
	PublishedAt string   `json:"published_at"`
	Tags        []string `json:"tags"`
}

// Provider fetches content from a JSON endpoint.
type Provider struct {
	name   string
	url    string
	client provider.Getter
	log    *slog.Logger
}

func New(name, url string, client provider.Getter, log *slog.Logger) *Provider {
	return &Provider{name: name, url: url, client: client, log: log.With("provider", name)}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Fetch(ctx context.Context) ([]domain.Content, error) {
	body, err := p.client.Get(ctx, p.url)
	if err != nil {
		return nil, err
	}
	contents, skipped, err := Parse(p.name, body)
	if err != nil {
		return nil, err
	}
	for _, e := range skipped {
		p.log.Warn("skipping invalid item", "error", e)
	}
	return contents, nil
}

// Parse decodes a provider payload. Invalid items are skipped and reported in skipped
// so a single bad record does not block the whole feed.
func Parse(providerName string, data []byte) (contents []domain.Content, skipped []error, err error) {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode json payload: %w", err)
	}

	contents = make([]domain.Content, 0, len(resp.Contents))
	for _, it := range resp.Contents {
		c, err := it.toDomain(providerName)
		if err != nil {
			skipped = append(skipped, err)
			continue
		}
		contents = append(contents, c)
	}
	return contents, skipped, nil
}

func (it item) toDomain(providerName string) (domain.Content, error) {
	typ, err := provider.ParseType(it.Type)
	if err != nil {
		return domain.Content{}, fmt.Errorf("item %q: %w", it.ID, err)
	}
	published, err := provider.ParseDate(it.PublishedAt)
	if err != nil {
		return domain.Content{}, fmt.Errorf("item %q: %w", it.ID, err)
	}
	duration, err := provider.ParseDuration(it.Metrics.Duration)
	if err != nil {
		return domain.Content{}, fmt.Errorf("item %q: %w", it.ID, err)
	}
	raw, err := json.Marshal(it)
	if err != nil {
		return domain.Content{}, fmt.Errorf("item %q: encode raw payload: %w", it.ID, err)
	}

	c := domain.Content{
		Provider:   providerName,
		ExternalID: it.ID,
		Title:      it.Title,
		Type:       typ,
		Metrics: domain.Metrics{
			Views:       it.Metrics.Views,
			Likes:       it.Metrics.Likes,
			DurationSec: duration,
			ReadingTime: it.Metrics.ReadingTime,
			Reactions:   it.Metrics.Reactions,
			Comments:    it.Metrics.Comments,
		},
		Tags:        provider.CleanTags(it.Tags),
		PublishedAt: published,
		RawPayload:  raw,
	}
	return c, provider.Validate(c)
}
