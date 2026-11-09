package provider

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/internal/domain"
)

// typeAliases maps provider-specific type names onto domain content types.
var typeAliases = map[string]domain.ContentType{
	"video":   domain.ContentTypeVideo,
	"text":    domain.ContentTypeText,
	"article": domain.ContentTypeText,
}

// ParseType maps a provider type name to a domain content type.
func ParseType(s string) (domain.ContentType, error) {
	t, ok := typeAliases[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return "", fmt.Errorf("unsupported content type %q", s)
	}
	return t, nil
}

// ParseDuration parses "mm:ss" or "hh:mm:ss" into seconds. An empty string yields 0.
func ParseDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	total := 0
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (i > 0 && n >= 60) {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		total = total*60 + n
	}
	return total, nil
}

// dateLayouts are tried in order when parsing provider timestamps.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseDate parses the timestamp formats seen across providers. Values without a zone are UTC.
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", s)
}

// CleanTags trims, lowercases and de-duplicates tags, dropping empty ones.
func CleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// Validate checks the invariants every mapped content item must satisfy.
func Validate(c domain.Content) error {
	switch {
	case strings.TrimSpace(c.ExternalID) == "":
		return fmt.Errorf("missing id")
	case strings.TrimSpace(c.Title) == "":
		return fmt.Errorf("item %q: missing title", c.ExternalID)
	case c.PublishedAt.IsZero():
		return fmt.Errorf("item %q: missing publish date", c.ExternalID)
	}
	m := c.Metrics
	if m.Views < 0 || m.Likes < 0 || m.ReadingTime < 0 || m.Reactions < 0 || m.Comments < 0 {
		return fmt.Errorf("item %q: negative metric", c.ExternalID)
	}
	return nil
}
