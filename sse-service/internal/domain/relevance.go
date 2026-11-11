package domain

import (
	"math"
	"sort"
)

// RelevanceExplanation shows how a content item's relevance for a search was computed. The
// values come from the same SQL that ranks search results.
type RelevanceExplanation struct {
	Query string
	// Matches reports whether the item is a result of the search at all.
	Matches bool
	// FullText is true for a whole-word match (ranked by ts_rank); false for a match on part of
	// a word only (ranked by word similarity).
	FullText bool
	// Terms are the searched words and where the item contains them.
	Terms []TermMatch
	// TitleWeight and TagWeight are the ts_rank weights of title and tag words.
	TitleWeight float64
	TagWeight   float64
	// Rank is ts_rank for a whole-word match.
	Rank float64
	// Steps reproduce Rank for a single-word search (see RankSteps); empty otherwise, because
	// for several words PostgreSQL also weighs how close the words are to each other.
	Steps []RankStep
	// WordSimilarity and PartialFactor give the score of a partial match: their product.
	WordSimilarity float64
	PartialFactor  float64
	Relevance      float64
}

// TermMatch says where an item contains a searched word.
type TermMatch struct {
	Term    string
	InTitle int // occurrences in the title
	InTags  int // occurrences in the tags
}

// RankStep is one occurrence of the searched word: Weight / Position² = Value.
type RankStep struct {
	Field    string // "title" or "tags"
	Weight   float64
	Position int // 1 for the heaviest occurrence, 2 for the next, ...
	Value    float64
}

// RankNormalizer is π²/6: ts_rank divides the sum of the steps by it, which keeps a word with
// endless occurrences below 1.
const RankNormalizer = math.Pi * math.Pi / 6

// RankSteps reproduces PostgreSQL's ts_rank for a single word with the given occurrences:
// occurrences are sorted by weight, the n-th contributes weight / n², and the sum is divided by
// RankNormalizer. It returns the steps and the resulting rank.
func RankSteps(term TermMatch, titleWeight, tagWeight float64) ([]RankStep, float64) {
	var steps []RankStep
	for range term.InTitle {
		steps = append(steps, RankStep{Field: "title", Weight: titleWeight})
	}
	for range term.InTags {
		steps = append(steps, RankStep{Field: "tags", Weight: tagWeight})
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Weight > steps[j].Weight })

	var sum float64
	for i := range steps {
		steps[i].Position = i + 1
		steps[i].Value = steps[i].Weight / float64((i+1)*(i+1))
		sum += steps[i].Value
	}
	return steps, sum / RankNormalizer
}
