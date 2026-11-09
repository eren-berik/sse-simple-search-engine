// Package domain holds the core types shared across the application.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// ContentType is the normalized type of a content item.
type ContentType string

const (
	ContentTypeVideo ContentType = "video"
	ContentTypeText  ContentType = "text"
)

// ParseContentType validates a client-supplied content type.
func ParseContentType(s string) (ContentType, error) {
	switch ContentType(strings.ToLower(strings.TrimSpace(s))) {
	case ContentTypeVideo:
		return ContentTypeVideo, nil
	case ContentTypeText:
		return ContentTypeText, nil
	default:
		return "", fmt.Errorf("%w: unknown content type %q (expected video or text)", ErrInvalidInput, s)
	}
}

// Metrics are the provider-agnostic engagement metrics of a content item.
// Video fields are zero for text items and vice versa.
type Metrics struct {
	Views       int64 `json:"views,omitempty"`
	Likes       int64 `json:"likes,omitempty"`
	DurationSec int   `json:"duration_sec,omitempty"`

	ReadingTime int   `json:"reading_time,omitempty"`
	Reactions   int64 `json:"reactions,omitempty"`
	Comments    int64 `json:"comments,omitempty"`
}

// Content is a content item in the standard format every provider maps into.
type Content struct {
	ID             int64
	Provider       string
	ExternalID     string
	Title          string
	Type           ContentType
	Metrics        Metrics
	Tags           []string
	PublishedAt    time.Time
	Score          float64
	ScoreUpdatedAt time.Time
	// RawPayload is the original provider item encoded as JSON, kept for debugging and reprocessing.
	RawPayload []byte
}
