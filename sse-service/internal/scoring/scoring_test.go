package scoring

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

var fixedNow = time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC)

func newScorer() *Scorer {
	return New(DefaultConfig(), func() time.Time { return fixedNow })
}

func TestScore(t *testing.T) {
	old := fixedNow.AddDate(-1, 0, 0)

	tests := []struct {
		name    string
		content domain.Content
		want    Breakdown
	}{
		{
			name: "video from provider1 (v1), old",
			content: domain.Content{
				Type:        domain.ContentTypeVideo,
				Metrics:     domain.Metrics{Views: 15000, Likes: 1200},
				PublishedAt: old,
			},
			// base = 15 + 12 = 27; 27*1.5 = 40.5; engagement = 1200/15000*10 = 0.8
			want: Breakdown{Base: 27, TypeCoefficient: 1.5, Recency: 0, Engagement: 0.8, Final: 41.3},
		},
		{
			name: "article from provider2 (a1), old",
			content: domain.Content{
				Type:        domain.ContentTypeText,
				Metrics:     domain.Metrics{ReadingTime: 8, Reactions: 450},
				PublishedAt: old,
			},
			// base = 8 + 9 = 17; engagement = 450/8*5 = 281.25
			want: Breakdown{Base: 17, TypeCoefficient: 1, Recency: 0, Engagement: 281.25, Final: 298.25},
		},
		{
			name: "video without views does not divide by zero",
			content: domain.Content{
				Type:        domain.ContentTypeVideo,
				PublishedAt: fixedNow,
			},
			want: Breakdown{TypeCoefficient: 1.5, Recency: 5, Final: 5},
		},
		{
			name: "text without reading time does not divide by zero",
			content: domain.Content{
				Type:        domain.ContentTypeText,
				Metrics:     domain.Metrics{Reactions: 100},
				PublishedAt: old,
			},
			want: Breakdown{Base: 2, TypeCoefficient: 1, Final: 2},
		},
	}

	s := newScorer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Score(tt.content)
			assert.InDelta(t, tt.want.Base, got.Base, 1e-9)
			assert.InDelta(t, tt.want.TypeCoefficient, got.TypeCoefficient, 1e-9)
			assert.InDelta(t, tt.want.Recency, got.Recency, 1e-9)
			assert.InDelta(t, tt.want.Engagement, got.Engagement, 1e-9)
			assert.InDelta(t, tt.want.Final, got.Final, 1e-9)
		})
	}
}

func TestRecencyTiers(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want float64
	}{
		{"future date", -time.Hour, 5},
		{"just published", 0, 5},
		{"exactly 7 days", 7 * day, 5},
		{"7 days + 1s", 7*day + time.Second, 3},
		{"exactly 30 days", 30 * day, 3},
		{"30 days + 1s", 30*day + time.Second, 1},
		{"exactly 90 days", 90 * day, 1},
		{"90 days + 1s", 90*day + time.Second, 0},
		{"a year", 365 * day, 0},
	}

	s := newScorer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := domain.Content{Type: domain.ContentTypeText, PublishedAt: fixedNow.Add(-tt.age)}
			assert.Equal(t, tt.want, s.Score(c).Recency)
		})
	}
}

func TestCustomCoefficients(t *testing.T) {
	cfg := DefaultConfig()
	cfg.VideoCoefficient = 2
	s := New(cfg, func() time.Time { return fixedNow })

	got := s.Score(domain.Content{
		Type:        domain.ContentTypeVideo,
		Metrics:     domain.Metrics{Views: 1000, Likes: 100},
		PublishedAt: fixedNow.AddDate(-1, 0, 0),
	})
	// base = 1 + 1 = 2; 2*2 = 4; engagement = 0.1*10 = 1
	assert.InDelta(t, 5.0, got.Final, 1e-9)
}

func TestExplanationMatchesTheScore(t *testing.T) {
	s := newScorer()

	t.Run("text", func(t *testing.T) {
		b := s.Score(domain.Content{
			Type:        domain.ContentTypeText,
			Metrics:     domain.Metrics{ReadingTime: 8, Reactions: 450},
			PublishedAt: fixedNow.AddDate(-1, 0, 0),
		})
		e := b.Explanation

		assert.Equal(t, []BaseTerm{
			{Metric: "reading_time", Value: 8, Divisor: 1, Result: 8},
			{Metric: "reactions", Value: 450, Divisor: 50, Result: 9},
		}, e.BaseTerms)
		assert.Equal(t, EngagementTerm{
			Numerator: "reactions", NumeratorValue: 450, Denominator: "reading_time", DenominatorValue: 8,
			Multiplier: 5, Result: 281.25,
		}, e.Engagement)
		assert.Equal(t, -1, e.Recency.MatchedTier, "a year old: no tier applies")
		assert.Equal(t, 0.0, e.Recency.Points)
	})

	t.Run("video published two days ago", func(t *testing.T) {
		b := s.Score(domain.Content{
			Type:        domain.ContentTypeVideo,
			Metrics:     domain.Metrics{Views: 15000, Likes: 1200},
			PublishedAt: fixedNow.Add(-50 * time.Hour),
		})
		e := b.Explanation

		var sum float64
		for _, term := range e.BaseTerms {
			sum += term.Result
		}
		assert.InDelta(t, b.Base, sum, 1e-9, "the terms add up to the base")
		assert.Equal(t, []string{"views", "likes"}, []string{e.BaseTerms[0].Metric, e.BaseTerms[1].Metric})
		assert.InDelta(t, b.Engagement, e.Engagement.Result, 1e-9)
		assert.Equal(t, 2.0, e.Recency.AgeDays)
		assert.Equal(t, 0, e.Recency.MatchedTier)
		assert.Equal(t, []RecencyRow{{7, 5}, {30, 3}, {90, 1}}, e.Recency.Tiers)
		assert.InDelta(t, b.Recency, e.Recency.Points, 1e-9)
	})

	t.Run("zero denominator", func(t *testing.T) {
		e := s.Score(domain.Content{Type: domain.ContentTypeVideo, Metrics: domain.Metrics{Likes: 5}, PublishedAt: fixedNow}).Explanation
		assert.Equal(t, 0.0, e.Engagement.DenominatorValue)
		assert.Equal(t, 0.0, e.Engagement.Result)
	})
}
