package memory

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// The duplicate check keeps a candidate when at least DuplicatePercent of the
// new name's significant words appear in the candidate's name or description,
// and returns at most MaxDuplicates. The rule is in Go, not in the store, so
// every store gives the same answer.
const (
	DuplicatePercent = 60
	MaxDuplicates    = 5
	minWordRunes     = 3
)

// stopwords are common words that say nothing about what a memory is about.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "are": true, "was": true, "not": true, "but": true, "you": true,
	"has": true, "have": true, "its": true, "into": true, "when": true, "then": true,
	"than": true, "use": true, "uses": true, "used": true, "using": true, "via": true,
}

// significant returns the words of a name that count towards a match: three
// letters or more and not a stopword.
func significant(name string) []string {
	var out []string
	for _, w := range Terms(name) {
		if utf8.RuneCountInString(w) >= minWordRunes && !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

// duplicates asks the store for candidates among the readable layers and
// applies the rule.
func (s *Service) duplicates(ctx context.Context, a access, d Draft) ([]Entry, error) {
	words := significant(d.Name())
	if len(words) == 0 {
		return nil, nil
	}
	cands, err := s.store.Candidates(ctx, a.readable, Words(d.Name()+" "+d.Description()), MaxCandidates)
	if err != nil {
		return nil, fmt.Errorf("check for duplicates: %w", err)
	}
	return closeMatches(words, cands), nil
}

// closeMatches keeps, in order, the candidates that hold enough of words.
func closeMatches(words []string, cands []Entry) []Entry {
	var out []Entry
	for _, e := range cands {
		have := make(map[string]bool)
		for _, w := range Terms(e.Name + " " + e.Description) {
			have[w] = true
		}
		hit := 0
		for _, w := range words {
			if have[w] {
				hit++
			}
		}
		if hit*100 >= DuplicatePercent*len(words) {
			out = append(out, e)
			if len(out) == MaxDuplicates {
				break
			}
		}
	}
	return out
}
