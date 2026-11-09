// Package provider defines the contract for external content providers.
//
// Adding a new provider means implementing Provider (typically: a DTO for the
// wire format plus a mapper into domain.Content) and registering it in main.
package provider

import (
	"context"

	"github.com/eren-berik/sse-simple-search-engine/internal/domain"
)

// Provider fetches content from an external source and returns it in the standard format.
// Implementations must not set scores; scoring is applied by the ingest service.
type Provider interface {
	Name() string
	Fetch(ctx context.Context) ([]domain.Content, error)
}

// Getter fetches a URL. *httpclient.Client satisfies it; tests can substitute a fake.
type Getter interface {
	Get(ctx context.Context, url string) ([]byte, error)
}
