// Package registry turns stored provider configurations into runnable providers.
package registry

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/jsonprovider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/xmlprovider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
)

// Factory builds a provider adapter for one wire format.
type Factory func(name, url string, client provider.Getter, log *slog.Logger) provider.Provider

// Parser decodes a payload of one wire format; it is used to detect a provider's format.
type Parser func(providerName string, data []byte) (contents []domain.Content, skipped []error, err error)

type adapter struct {
	factory Factory
	parse   Parser
}

// Registry maps formats to adapters. Supporting a new format means writing an adapter
// and registering it here; providers using it can then be added at runtime.
type Registry struct {
	adapters  map[domain.ProviderFormat]adapter
	clientCfg httpclient.Config
	log       *slog.Logger

	mu sync.Mutex
	// clients are kept per provider so rate limits persist across syncs.
	clients map[int64]cachedClient
}

type cachedClient struct {
	url    string
	client *httpclient.Client
}

// New returns a registry with the JSON and XML adapters registered.
func New(clientCfg httpclient.Config, log *slog.Logger) *Registry {
	r := &Registry{
		adapters:  map[domain.ProviderFormat]adapter{},
		clientCfg: clientCfg,
		log:       log,
		clients:   map[int64]cachedClient{},
	}
	r.Register(domain.ProviderFormatJSON, func(name, url string, c provider.Getter, log *slog.Logger) provider.Provider {
		return jsonprovider.New(name, url, c, log)
	}, jsonprovider.Parse)
	r.Register(domain.ProviderFormatXML, func(name, url string, c provider.Getter, log *slog.Logger) provider.Provider {
		return xmlprovider.New(name, url, c, log)
	}, xmlprovider.Parse)
	return r
}

func (r *Registry) Register(format domain.ProviderFormat, f Factory, p Parser) {
	r.adapters[format] = adapter{factory: f, parse: p}
}

// Build returns a provider for cfg.
func (r *Registry) Build(cfg domain.Provider) (provider.Provider, error) {
	a, ok := r.adapters[cfg.Format]
	if !ok {
		return nil, fmt.Errorf("no adapter for provider format %q", cfg.Format)
	}
	return a.factory(cfg.Name, cfg.URL, r.client(cfg), r.log), nil
}

// Detect fetches url and returns the format whose adapter reads at least one valid item from it.
// The payload is sniffed first ('{' or '[' suggests json, '<' xml) so the likely adapter is tried
// first, but a format is only accepted if its parser actually understands the content.
func (r *Registry) Detect(ctx context.Context, url string) (domain.ProviderFormat, error) {
	cfg := r.clientCfg
	cfg.MaxRetries = 0 // an admin is waiting for the answer
	body, err := httpclient.New(cfg).Get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrProviderUnreachable, err)
	}

	for _, format := range r.candidates(body) {
		contents, _, err := r.adapters[format].parse("detect", body)
		if err == nil && len(contents) > 0 {
			return format, nil
		}
	}
	return "", domain.ErrFormatNotDetected
}

// candidates orders the registered formats so the one suggested by the payload comes first.
func (r *Registry) candidates(body []byte) []domain.ProviderFormat {
	formats := make([]domain.ProviderFormat, 0, len(r.adapters))
	for f := range r.adapters {
		formats = append(formats, f)
	}
	slices.Sort(formats)

	trimmed := bytes.TrimLeft(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), " \t\r\n")
	var hint domain.ProviderFormat
	switch {
	case bytes.HasPrefix(trimmed, []byte("{")), bytes.HasPrefix(trimmed, []byte("[")):
		hint = domain.ProviderFormatJSON
	case bytes.HasPrefix(trimmed, []byte("<")):
		hint = domain.ProviderFormatXML
	}
	if i := slices.Index(formats, hint); i > 0 {
		formats[0], formats[i] = formats[i], formats[0]
	}
	return formats
}

func (r *Registry) client(cfg domain.Provider) *httpclient.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[cfg.ID]; ok && c.url == cfg.URL {
		return c.client
	}
	c := httpclient.New(r.clientCfg)
	r.clients[cfg.ID] = cachedClient{url: cfg.URL, client: c}
	return c
}
