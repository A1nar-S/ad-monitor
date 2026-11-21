// Package textutil matches what people type against names: folding case
// and accents, "did you mean" suggestions, and the small list helpers the
// site adapters' search forms share.
package textutil

import (
	"fmt"
	"slices"
	"strings"
)

// accents maps the accented letters of the region's languages (Baltic,
// German, French, Spanish, Polish) to plain ones.
var accents = strings.NewReplacer(
	"ā", "a", "č", "c", "ē", "e", "ģ", "g", "ī", "i", "ķ", "k", "ļ", "l", "ņ", "n",
	"š", "s", "ū", "u", "ž", "z", "ō", "o", "ŗ", "r", "ä", "a", "ö", "o", "ü", "u",
	"é", "e", "è", "e", "ë", "e", "á", "a", "ó", "o", "í", "i", "ú", "u", "ą", "a",
	"ę", "e", "ė", "e", "į", "i", "ų", "u", "ł", "l",
)

// Fold makes text comparable: lower case, no accents, trimmed; inner spaces
// are kept.
func Fold(s string) string { return accents.Replace(strings.ToLower(strings.TrimSpace(s))) }

// Normalize is Fold with runs of spaces made single, for comparing names.
func Normalize(s string) string {
	return strings.Join(strings.Fields(accents.Replace(strings.ToLower(s))), " ")
}

// Levenshtein is the edit distance between a and b, in runes.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// Closest returns up to three names nearest to s by edit distance, for "did
// you mean".
func Closest(s string, names []string) []string { return closest(s, names, false) }

// ClosestContaining is Closest that also counts a name containing s as a
// match: "turbo" surely means one of the names with "turbo" in them.
func ClosestContaining(s string, names []string) []string { return closest(s, names, true) }

func closest(s string, names []string, contains bool) []string {
	type cand struct {
		name string
		d    int
	}
	fs := Normalize(s)
	var cs []cand
	for _, n := range names {
		fn := Normalize(n)
		d := Levenshtein(fs, fn)
		if contains && fs != "" && strings.Contains(fn, fs) {
			d = 0
		}
		cs = append(cs, cand{n, d})
	}
	slices.SortStableFunc(cs, func(a, b cand) int { return a.d - b.d })
	var out []string
	for _, c := range cs {
		if len(out) == 3 || c.d > max(2, len(s)/2) {
			break
		}
		out = append(out, c.name)
	}
	return out
}

// DidYouMean renders suggestions as ` (did you mean "a" or "b"?)`, or ""
// when there are none.
func DidYouMean(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	return " (did you mean " + strings.Join(QuoteAll(suggestions), " or ") + "?)"
}

// QuoteAll quotes every string.
func QuoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// ToAny turns strings into a []any, the shape JSON filters come in.
func ToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Uniq drops repeats, keeping the first occurrence's place.
func Uniq(ss []string) []string {
	seen := map[string]bool{}
	out := ss[:0:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// NonEmpty trims the strings and drops the empty ones.
func NonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Number reads a JSON or Go number.
func Number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// Keyword limits for a search's free words.
const (
	MaxKeywords   = 10
	MaxKeywordLen = 50
)

// Keywords cleans a search's free words: single spaces, no repeats (by
// Normalize), each 2 to MaxKeywordLen letters, at most MaxKeywords.
func Keywords(names []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.Join(strings.Fields(n), " ")
		switch {
		case n == "" || seen[Normalize(n)]:
			continue
		case len([]rune(n)) < 2:
			return nil, fmt.Errorf("keyword %q is too short; use at least 2 letters", n)
		case len([]rune(n)) > MaxKeywordLen:
			return nil, fmt.Errorf("keyword %q is too long; keep it under %d letters", n, MaxKeywordLen)
		}
		seen[Normalize(n)] = true
		out = append(out, n)
	}
	if len(out) > MaxKeywords {
		return nil, fmt.Errorf("at most %d keywords, got %d", MaxKeywords, len(out))
	}
	return out, nil
}
