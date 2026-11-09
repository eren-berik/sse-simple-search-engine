// Package registry turns stored provider configurations into runnable providers.
package registry

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/jsonprovider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/provider/xmlprovider"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpclient"
)

// Factory builds a provider adapter for one wire format.
type Factory func(name, url string, client provider.Getter, log *slog.Logger) provider.Provider

// Registry maps formats to adapters. Supporting a new format means writing an adapter
// and registering it here; providers using it can then be added at runtime.
type Registry struct {
	factories map[domain.ProviderFormat]Factory
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
		factories: map[domain.ProviderFormat]Factory{},
		clientCfg: clientCfg,
		log:       log,
		clients:   map[int64]cachedClient{},
	}
	r.Register(domain.ProviderFormatJSON, func(name, url string, c provider.Getter, log *slog.Logger) provider.Provider {
		return jsonprovider.New(name, url, c, log)
	})
	r.Register(domain.ProviderFormatXML, func(name, url string, c provider.Getter, log *slog.Logger) provider.Provider {
		return xmlprovider.New(name, url, c, log)
	})
	return r
}

func (r *Registry) Register(format domain.ProviderFormat, f Factory) {
	r.factories[format] = f
}

// Build returns a provider for cfg.
func (r *Registry) Build(cfg domain.Provider) (provider.Provider, error) {
	factory, ok := r.factories[cfg.Format]
	if !ok {
		return nil, fmt.Errorf("no adapter for provider format %q", cfg.Format)
	}
	return factory(cfg.Name, cfg.URL, r.client(cfg), r.log), nil
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
