package types

import (
	"crypto/sha256"
	"strings"
	"unicode"
)

const (
	// LiteralFingerprintBytes is the fixed persisted fingerprint width.
	LiteralFingerprintBytes     = 16
	maxLiteralQueryFingerprints = 128
	// MaxLiteralSearchQueryBytes bounds normalization and gram allocation.
	MaxLiteralSearchQueryBytes = 64 << 10
)

// LiteralQuery owns the normalization shared by candidate construction and
// canonical verification. Fingerprints are only available for three-rune
// literals; an unavailable fingerprint must never be interpreted as no match.
type LiteralQuery struct {
	canonical    string
	filterable   bool
	fingerprints []string
}

// CharacterizeLiteralQuery normalizes a literal and derives safe fingerprints.
func CharacterizeLiteralQuery(raw string) LiteralQuery {
	canonical := foldLiteralASCII(strings.TrimSpace(raw))
	if len(canonical) > MaxLiteralSearchQueryBytes {
		return LiteralQuery{canonical: canonical}
	}
	runes := []rune(canonical)
	caseStable := true
	for _, r := range runes {
		if r > unicode.MaxASCII && (unicode.ToLower(r) != r || unicode.ToUpper(r) != r) {
			caseStable = false
			break
		}
	}
	q := LiteralQuery{canonical: canonical, filterable: len(runes) >= 3 && caseStable}
	if !q.filterable {
		return q
	}
	seen := make(map[string]struct{}, len(runes)-2)
	for i := 0; i+3 <= len(runes); i++ {
		sum := sha256.Sum256([]byte(string(runes[i : i+3])))
		fp := string(sum[:LiteralFingerprintBytes])
		if _, exists := seen[fp]; exists {
			continue
		}
		seen[fp] = struct{}{}
		q.fingerprints = append(q.fingerprints, fp)
		if len(q.fingerprints) > maxLiteralQueryFingerprints {
			q.filterable = false
			q.fingerprints = nil
			break
		}
	}
	return q
}

// LiteralSearchBudget bounds physical and decoded work for the projection authority.
type LiteralSearchBudget struct {
	SourceRows                int
	StoredBytes, DecodedBytes int64
}

func foldLiteralASCII(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}

// Canonical returns verification text.
func (q LiteralQuery) Canonical() string { return q.canonical }

// Filterable reports whether fingerprints may narrow candidates.
func (q LiteralQuery) Filterable() bool { return q.filterable }

// Fingerprints returns cloned fixed-size hashes.
func (q LiteralQuery) Fingerprints() []string { return append([]string(nil), q.fingerprints...) }

// Contains applies canonical literal semantics.
func (q LiteralQuery) Contains(needle LiteralQuery) bool {
	return needle.canonical != "" && strings.Contains(q.canonical, needle.canonical)
}

// DeepLiteralSearchBudget bounds the authoritative projection candidate window.
var DeepLiteralSearchBudget = LiteralSearchBudget{SourceRows: 4096, StoredBytes: 64 << 20, DecodedBytes: 128 << 20}
