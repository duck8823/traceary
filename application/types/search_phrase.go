package types

import "strings"

// SearchPhrase is the fold+substring predicate shared by both two-tier
// search lanes. Spaces and hyphens are preserved; matching is not token AND.
type SearchPhrase struct {
	canonical string
}

// SearchPhraseOf normalizes a raw query with the same ASCII fold used by
// literal verification. An empty or whitespace-only query never matches.
func SearchPhraseOf(raw string) SearchPhrase {
	return SearchPhrase{canonical: foldLiteralASCII(strings.TrimSpace(raw))}
}

// Canonical returns the folded needle.
func (p SearchPhrase) Canonical() string { return p.canonical }

// IsEmpty reports that the needle cannot produce a phrase hit.
func (p SearchPhrase) IsEmpty() bool { return p.canonical == "" }

// Matches reports a case-folded contiguous substring match.
// '%' and '_' are literals; they are not LIKE wildcards.
func (p SearchPhrase) Matches(haystack string) bool {
	if p.canonical == "" {
		return false
	}
	return strings.Contains(foldLiteralASCII(haystack), p.canonical)
}
