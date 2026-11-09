package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ProviderFormat selects the adapter used to read a provider's payload.
// A new provider must follow the schema of an existing format.
type ProviderFormat string

const (
	ProviderFormatJSON ProviderFormat = "json"
	ProviderFormatXML  ProviderFormat = "xml"
)

// ParseProviderFormat validates a format name (case-insensitive).
func ParseProviderFormat(s string) (ProviderFormat, error) {
	switch f := ProviderFormat(strings.ToLower(strings.TrimSpace(s))); f {
	case ProviderFormatJSON, ProviderFormatXML:
		return f, nil
	default:
		return "", fmt.Errorf("%w: unknown provider format %q (expected json or xml)", ErrInvalidInput, s)
	}
}

// Provider is a configured content source. Its Name is stored on every content item it produced.
type Provider struct {
	ID        int64
	Name      string
	URL       string
	Format    ProviderFormat
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProviderPatch holds the mutable fields of a provider; nil fields are left unchanged.
// Renaming also renames the provider on its stored contents and sync runs.
type ProviderPatch struct {
	Name    *string
	URL     *string
	Format  *ProviderFormat
	Enabled *bool
}

var providerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,49}$`)

// ValidateProviderName checks that name is a lowercase slug of 2-50 characters.
func ValidateProviderName(name string) error {
	if !providerNamePattern.MatchString(name) {
		return fmt.Errorf("%w: provider name must be 2-50 lowercase letters, digits, '-' or '_'", ErrInvalidInput)
	}
	return nil
}

// ValidateProviderURL checks that raw is an absolute http(s) URL.
func ValidateProviderURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: provider url must be an absolute http or https URL", ErrInvalidInput)
	}
	return nil
}
