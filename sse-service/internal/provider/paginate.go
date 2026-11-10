package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

// DefaultMaxPages bounds how many pages one fetch follows, so a provider reporting a huge or
// wrong total cannot make a sync run forever.
const DefaultMaxPages = 50

// Page is one page of a provider feed, in the standard format, with the provider's
// pagination metadata. Zero metadata means the feed is not paginated.
type Page struct {
	Contents []domain.Content
	// Skipped are items that could not be converted; they still count as received.
	Skipped []error
	Number  int // page number the provider says this is
	PerPage int
	Total   int // items in the whole feed
}

func (p Page) received() int { return len(p.Contents) + len(p.Skipped) }

// Feed is the result of fetching a provider.
type Feed struct {
	Contents []domain.Content
	// Complete reports that Contents is the provider's whole feed. When false (the provider
	// ignores the page parameter, or the page limit was reached) an item missing from Contents
	// may still exist at the provider and must not be treated as removed.
	Complete bool
}

// PageParser decodes one page payload.
type PageParser func(data []byte) (Page, error)

// FetchPages fetches the first page from feedURL and follows the provider's pagination by
// requesting page=2, 3, ... until the feed ends or maxPages pages were fetched.
//
// When the provider reports a total, the feed is complete only once that many items arrived;
// an empty page before that ends the fetch as incomplete. Without a total, a short or empty
// page is the last one. Requests go through get, so the provider's rate
// limit and retries apply to every page. Any failing page fails the whole fetch: a sync
// stores a provider's feed completely or not at all.
func FetchPages(ctx context.Context, get Getter, feedURL string, parse PageParser, maxPages int, log *slog.Logger) (Feed, error) {
	if maxPages < 1 {
		maxPages = DefaultMaxPages
	}
	var feed Feed
	seen := map[string]bool{}
	received := 0

	for n := 1; ; n++ {
		pageURL, err := withPage(feedURL, n)
		if err != nil {
			return Feed{}, err
		}
		body, err := get.Get(ctx, pageURL)
		if err != nil {
			return Feed{}, fmt.Errorf("page %d: %w", n, err)
		}
		page, err := parse(body)
		if err != nil {
			return Feed{}, fmt.Errorf("page %d: %w", n, err)
		}
		for _, e := range page.Skipped {
			log.Warn("skipping invalid item", "page", n, "error", e)
		}

		// A provider that ignores the page parameter answers with the first page again;
		// following it would only repeat the same items.
		if n > 1 && (page.Number == n-1 || repeats(page, seen)) {
			log.Warn("provider ignores the page parameter; keeping the items received so far",
				"received", received, "reported_total", page.Total)
			return feed, nil
		}
		for _, c := range page.Contents {
			seen[c.ExternalID] = true
		}
		feed.Contents = append(feed.Contents, page.Contents...)
		received += page.received()

		switch {
		case page.Total == 0 && page.PerPage == 0: // not paginated
			feed.Complete = true
		case page.Total > 0 && received >= page.Total:
			feed.Complete = true
		case page.Total == 0 && page.received() < page.PerPage: // short or empty page: the last one
			feed.Complete = true
		case page.received() == 0:
			log.Warn("provider ran out of items before its reported total; keeping the items received so far",
				"received", received, "reported_total", page.Total)
			return feed, nil
		case n >= maxPages:
			log.Warn("page limit reached; keeping the items received so far",
				"pages", n, "received", received, "reported_total", page.Total)
			return feed, nil
		default:
			continue
		}
		return feed, nil
	}
}

// repeats reports whether page starts with an item that was already received.
func repeats(page Page, seen map[string]bool) bool {
	return len(page.Contents) > 0 && seen[page.Contents[0].ExternalID]
}

// withPage returns feedURL with page=n. The first page is the URL as configured, so feeds
// that are not paginated are requested exactly as the admin entered them.
func withPage(feedURL string, n int) (string, error) {
	if n == 1 {
		return feedURL, nil
	}
	u, err := url.Parse(feedURL)
	if err != nil {
		return "", fmt.Errorf("parse provider url: %w", err)
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(n))
	u.RawQuery = q.Encode()
	return u.String(), nil
}
