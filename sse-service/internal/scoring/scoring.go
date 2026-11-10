// Package scoring implements the content scoring formula:
//
//	Final = (Base * TypeCoefficient) + Recency + Engagement
package scoring

import (
	"math"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
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

// Breakdown exposes each component of a score, and how each was derived.
type Breakdown struct {
	Base            float64     `json:"base"`
	TypeCoefficient float64     `json:"type_coefficient"`
	Recency         float64     `json:"recency"`
	Engagement      float64     `json:"engagement"`
	Final           float64     `json:"final"`
	Explanation     Explanation `json:"explanation"`
}

// Explanation shows the inputs and constants behind each component. It is produced by the
// same code that computes the score, so it can never disagree with it.
type Explanation struct {
	// BaseTerms add up to Base: each is a metric divided by a constant.
	BaseTerms  []BaseTerm       `json:"base_terms"`
	Engagement EngagementTerm   `json:"engagement"`
	Recency    RecencyReasoning `json:"recency"`
}

// BaseTerm is Value / Divisor = Result for one metric.
type BaseTerm struct {
	Metric  string  `json:"metric"`
	Value   float64 `json:"value"`
	Divisor float64 `json:"divisor"`
	Result  float64 `json:"result"`
}

// EngagementTerm is (Numerator / Denominator) * Multiplier = Result; zero when Denominator is 0.
type EngagementTerm struct {
	Numerator        string  `json:"numerator"`
	NumeratorValue   float64 `json:"numerator_value"`
	Denominator      string  `json:"denominator"`
	DenominatorValue float64 `json:"denominator_value"`
	Multiplier       float64 `json:"multiplier"`
	Result           float64 `json:"result"`
}

// RecencyReasoning lists the tiers and which one, if any, the content's age falls into.
type RecencyReasoning struct {
	AgeDays float64      `json:"age_days"`
	Tiers   []RecencyRow `json:"tiers"`
	// MatchedTier is the index into Tiers, or -1 when the content is older than every tier.
	MatchedTier int     `json:"matched_tier"`
	Points      float64 `json:"points"`
}

// RecencyRow is one tier: content at most MaxAgeDays old gets Points.
type RecencyRow struct {
	MaxAgeDays float64 `json:"max_age_days"`
	Points     float64 `json:"points"`
}

// rule is the formula for one content type: base terms (metric / divisor), the engagement
// ratio and the coefficient. Score evaluates it and records every step.
type rule struct {
	base        []baseRule
	engagement  ratioRule
	coefficient func(Config) float64
}

type baseRule struct {
	metric  string
	divisor float64
	value   func(domain.Metrics) float64
}

type ratioRule struct {
	numerator, denominator string
	numValue, denValue     func(domain.Metrics) float64
	multiplier             float64
}

var (
	views       = func(m domain.Metrics) float64 { return float64(m.Views) }
	likes       = func(m domain.Metrics) float64 { return float64(m.Likes) }
	readingTime = func(m domain.Metrics) float64 { return float64(m.ReadingTime) }
	reactions   = func(m domain.Metrics) float64 { return float64(m.Reactions) }
)

// rules is the case formula. Metric names match the API's metric fields.
var rules = map[domain.ContentType]rule{
	// base = views/1000 + likes/100; engagement = (likes/views) * 10
	domain.ContentTypeVideo: {
		base:        []baseRule{{"views", 1000, views}, {"likes", 100, likes}},
		engagement:  ratioRule{numerator: "likes", denominator: "views", numValue: likes, denValue: views, multiplier: 10},
		coefficient: func(c Config) float64 { return c.VideoCoefficient },
	},
	// base = reading_time + reactions/50; engagement = (reactions/reading_time) * 5
	domain.ContentTypeText: {
		base:        []baseRule{{"reading_time", 1, readingTime}, {"reactions", 50, reactions}},
		engagement:  ratioRule{numerator: "reactions", denominator: "reading_time", numValue: reactions, denValue: readingTime, multiplier: 5},
		coefficient: func(c Config) float64 { return c.TextCoefficient },
	},
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

	if r, ok := rules[c.Type]; ok {
		for _, term := range r.base {
			value := term.value(m)
			result := value / term.divisor
			b.Base += result
			b.Explanation.BaseTerms = append(b.Explanation.BaseTerms,
				BaseTerm{Metric: term.metric, Value: value, Divisor: term.divisor, Result: result})
		}
		b.TypeCoefficient = r.coefficient(s.cfg)

		e := r.engagement
		num, den := e.numValue(m), e.denValue(m)
		b.Engagement = ratio(num, den) * e.multiplier
		b.Explanation.Engagement = EngagementTerm{
			Numerator: e.numerator, NumeratorValue: num,
			Denominator: e.denominator, DenominatorValue: den,
			Multiplier: e.multiplier, Result: b.Engagement,
		}
	}

	b.Explanation.Recency = s.recency(c.PublishedAt)
	b.Recency = b.Explanation.Recency.Points
	b.Final = b.Base*b.TypeCoefficient + b.Recency + b.Engagement
	return b
}

// Final is a shorthand for Score(c).Final.
func (s *Scorer) Final(c domain.Content) float64 {
	return s.Score(c).Final
}

func (s *Scorer) recency(publishedAt time.Time) RecencyReasoning {
	age := s.now().Sub(publishedAt)
	if age < 0 { // future-dated items are treated as brand new
		age = 0
	}
	r := RecencyReasoning{AgeDays: math.Floor(age.Hours() / 24), MatchedTier: -1}
	for i, tier := range s.cfg.RecencyTiers {
		r.Tiers = append(r.Tiers, RecencyRow{MaxAgeDays: tier.MaxAge.Hours() / 24, Points: tier.Points})
		if r.MatchedTier < 0 && age <= tier.MaxAge {
			r.MatchedTier, r.Points = i, tier.Points
		}
	}
	return r
}

// ratio guards against division by zero for items without views or reading time.
func ratio(num, den float64) float64 {
	if den <= 0 {
		return 0
	}
	return num / den
}
