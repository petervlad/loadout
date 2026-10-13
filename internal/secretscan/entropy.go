package secretscan

import "math"

// entropy is the Shannon entropy of a run, in bits per character. Runs are
// ASCII (the rule's character class), so n is the character count.
func entropy(counts map[rune]int, n int) float64 {
	var h float64
	for _, c := range counts {
		p := float64(c) / float64(n)
		h -= p * math.Log2(p)
	}
	return h
}
