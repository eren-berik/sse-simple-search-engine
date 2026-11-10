// Package xmlprovider integrates the XML content provider.
package xmlprovider

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
)

type feed struct {
	XMLName xml.Name `xml:"feed"`
	Items   []item   `xml:"items>item"`
	Meta    struct {
		TotalCount   int `xml:"total_count"`
		CurrentPage  int `xml:"current_page"`
		ItemsPerPage int `xml:"items_per_page"`
	} `xml:"meta"`
}

type item struct {
	ID       string `xml:"id" json:"id"`
	Headline string `xml:"headline" json:"headline"`
	Type     string `xml:"type" json:"type"`
	Stats    struct {
		Views       int64  `xml:"views" json:"views,omitempty"`
		Likes       int64  `xml:"likes" json:"likes,omitempty"`
		Duration    string `xml:"duration" json:"duration,omitempty"`
		ReadingTime int    `xml:"reading_time" json:"reading_time,omitempty"`
		Reactions   int64  `xml:"reactions" json:"reactions,omitempty"`
		Comments    int64  `xml:"comments" json:"comments,omitempty"`
	} `xml:"stats" json:"stats"`
	PublicationDate string   `xml:"publication_date" json:"publication_date"`
	Categories      []string `xml:"categories>category" json:"categories"`
}

// Provider fetches content from an XML endpoint.
type Provider struct {
	name     string
	url      string
	client   provider.Getter
	maxPages int
	log      *slog.Logger
}

func New(name, url string, client provider.Getter, maxPages int, log *slog.Logger) *Provider {
	return &Provider{name: name, url: url, client: client, maxPages: maxPages, log: log.With("provider", name)}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Fetch(ctx context.Context) (provider.Feed, error) {
	return provider.FetchPages(ctx, p.client, p.url, func(data []byte) (provider.Page, error) {
		return ParsePage(p.name, data)
	}, p.maxPages, p.log)
}

// Parse decodes a provider payload. Invalid items are skipped and reported in skipped
// so a single bad record does not block the whole feed.
func Parse(providerName string, data []byte) (contents []domain.Content, skipped []error, err error) {
	page, err := ParsePage(providerName, data)
	return page.Contents, page.Skipped, err
}

// ParsePage decodes one page of the feed, with its pagination metadata.
func ParsePage(providerName string, data []byte) (provider.Page, error) {
	var f feed
	if err := xml.Unmarshal(data, &f); err != nil {
		return provider.Page{}, fmt.Errorf("decode xml payload: %w", err)
	}

	page := provider.Page{
		Contents: make([]domain.Content, 0, len(f.Items)),
		Number:   f.Meta.CurrentPage,
		PerPage:  f.Meta.ItemsPerPage,
		Total:    f.Meta.TotalCount,
	}
	for _, it := range f.Items {
		c, err := it.toDomain(providerName)
		if err != nil {
			page.Skipped = append(page.Skipped, err)
			continue
		}
		page.Contents = append(page.Contents, c)
	}
	return page, nil
}

func (it item) toDomain(providerName string) (domain.Content, error) {
	typ, err := provider.ParseType(it.Type)
	if err != nil {
		return domain.Content{}, fmt.Errorf("item %q: %w", it.ID, err)
	}
	published, err := provider.ParseDate(it.PublicationDate)
	if err != nil {
		return domain.Content{}, fmt.Errorf("item %q: %w", it.ID, err)
	}
	duration, err := provider.ParseDuration(it.Stats.Duration)
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
		Title:      it.Headline,
		Type:       typ,
		Metrics: domain.Metrics{
			Views:       it.Stats.Views,
			Likes:       it.Stats.Likes,
			DurationSec: duration,
			ReadingTime: it.Stats.ReadingTime,
			Reactions:   it.Stats.Reactions,
			Comments:    it.Stats.Comments,
		},
		Tags:        provider.CleanTags(it.Categories),
		PublishedAt: published,
		RawPayload:  raw,
	}
	return c, provider.Validate(c)
}
