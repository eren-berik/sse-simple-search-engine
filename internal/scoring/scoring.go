// Package scoring implements the content scoring formula:
//
//	Final = (Base * TypeCoefficient) + Recency + Engagement
package scoring

import (
	"time"

	"github.com/eren-berik/sse-simple-search-engine/internal/domain"
)

// RecencyTier awards Points to content published within MaxAge.
type RecencyTier struct {
	MaxAge time.Duration
	Points float64
}

// Config holds the tunable weights of the formula.
type Config struct {
	VideoCoefficient float64
	TextCoefficient  float64
	// RecencyTiers must be sorted by ascending MaxAge; the first matching tier wins.
	RecencyTiers []RecencyTier
}

const day = 24 * time.Hour

// DefaultConfig returns the weights defined in the case specification.
func DefaultConfig() Config {
	return Config{
		VideoCoefficient: 1.5,
		TextCoefficient:  1.0,
		RecencyTiers: []RecencyTier{
			{MaxAge: 7 * day, Points: 5},
			{MaxAge: 30 * day, Points: 3},
			{MaxAge: 90 * day, Points: 1},
		},
	}
}

// Breakdown exposes each component of a score.
type Breakdown struct {
	Base            float64 `json:"base"`
	TypeCoefficient float64 `json:"type_coefficient"`
	Recency         float64 `json:"recency"`
	Engagement      float64 `json:"engagement"`
	Final           float64 `json:"final"`
}

// Scorer computes content scores relative to the current time.
type Scorer struct {
	cfg Config
	now func() time.Time
}

// New returns a Scorer. now is injectable so recency is deterministic in tests.
func New(cfg Config, now func() time.Time) *Scorer {
	if now == nil {
		now = time.Now
	}
	return &Scorer{cfg: cfg, now: now}
}

// Score returns the full breakdown for c.
func (s *Scorer) Score(c domain.Content) Breakdown {
	var b Breakdown
	m := c.Metrics

	switch c.Type {
	case domain.ContentTypeVideo:
		b.Base = float64(m.Views)/1000 + float64(m.Likes)/100
		b.TypeCoefficient = s.cfg.VideoCoefficient
		b.Engagement = ratio(float64(m.Likes), float64(m.Views)) * 10
	case domain.ContentTypeText:
		b.Base = float64(m.ReadingTime) + float64(m.Reactions)/50
		b.TypeCoefficient = s.cfg.TextCoefficient
		b.Engagement = ratio(float64(m.Reactions), float64(m.ReadingTime)) * 5
	}

	b.Recency = s.recency(c.PublishedAt)
	b.Final = b.Base*b.TypeCoefficient + b.Recency + b.Engagement
	return b
}

// Final is a shorthand for Score(c).Final.
func (s *Scorer) Final(c domain.Content) float64 {
	return s.Score(c).Final
}

func (s *Scorer) recency(publishedAt time.Time) float64 {
	age := s.now().Sub(publishedAt)
	if age < 0 { // future-dated items are treated as brand new
		age = 0
	}
	for _, tier := range s.cfg.RecencyTiers {
		if age <= tier.MaxAge {
			return tier.Points
		}
	}
	return 0
}

// ratio guards against division by zero for items without views or reading time.
func ratio(num, den float64) float64 {
	if den <= 0 {
		return 0
	}
	return num / den
}
