package types

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// LiteralFingerprintBytes is the fixed persisted fingerprint width.
	LiteralFingerprintBytes = 16
	// LiteralSearchCursorVersion identifies the continuation envelope.
	LiteralSearchCursorVersion = 1
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
	canonical := strings.ToLower(strings.TrimSpace(raw))
	runes := []rune(canonical)
	q := LiteralQuery{canonical: canonical, filterable: len(runes) >= 3}
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
	}
	return q
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

// LiteralSearchTier identifies candidate provenance.
type LiteralSearchTier string

const (
	// LiteralSearchTierFingerprint identifies the historical candidate table.
	LiteralSearchTierFingerprint LiteralSearchTier = "historical_fingerprint"
	// LiteralSearchTierBoundedVerification identifies an inclusive source scan.
	LiteralSearchTierBoundedVerification LiteralSearchTier = "bounded_verification"
)

// LiteralSearchCoverage describes fully processed source coverage.
type LiteralSearchCoverage struct {
	ProcessedSources int64 `json:"processed_sources"`
	HighWater        int64 `json:"high_water"`
	Complete         bool  `json:"complete"`
}

// LiteralSearchBudget bounds physical and decoded work.
type LiteralSearchBudget struct {
	SourceRows                int
	StoredBytes, DecodedBytes int64
}

// Valid reports whether every hard bound is positive.
func (b LiteralSearchBudget) Valid() bool {
	return b.SourceRows > 0 && b.StoredBytes > 0 && b.DecodedBytes > 0
}

// LiteralSearchRequest is the sibling tier-aware query contract.
type LiteralSearchRequest struct {
	Criteria     EventSearchCriteria
	Budget       LiteralSearchBudget
	Continuation string
}

// LiteralSearchPage exposes matches and honest completeness metadata.
type LiteralSearchPage struct {
	Events        []BoundedEvent        `json:"events"`
	Tier          LiteralSearchTier     `json:"tier"`
	Coverage      LiteralSearchCoverage `json:"coverage"`
	PartialReason string                `json:"partial_reason,omitempty"`
	Continuation  string                `json:"continuation,omitempty"`
}

// ErrLiteralSearchCursorMismatch rejects replay against changed inputs.
var ErrLiteralSearchCursorMismatch = errors.New("literal search continuation does not match the request or active projection")

// LiteralSearchCursor binds progress to query and projection identity.
type LiteralSearchCursor struct {
	Version       int    `json:"v"`
	LastSequence  int64  `json:"last_sequence"`
	CriteriaHash  string `json:"criteria_hash"`
	Generation    string `json:"generation"`
	HighWater     int64  `json:"high_water"`
	QueryRevision int64  `json:"query_revision"`
}

// Encode serializes a URL-safe opaque continuation.
func (c LiteralSearchCursor) Encode() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode literal search cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeLiteralSearchCursor parses and validates the envelope version.
func DecodeLiteralSearchCursor(value string) (LiteralSearchCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	var c LiteralSearchCursor
	if json.Unmarshal(b, &c) != nil || c.Version != LiteralSearchCursorVersion || c.LastSequence < 0 {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	return c, nil
}

// Validate binds a decoded cursor to current immutable query inputs.
func (c LiteralSearchCursor) Validate(criteriaHash, generation string, highWater, revision int64) error {
	if c.Version != LiteralSearchCursorVersion || c.CriteriaHash != criteriaHash || c.Generation != generation || c.HighWater != highWater || c.QueryRevision != revision {
		return ErrLiteralSearchCursorMismatch
	}
	return nil
}
