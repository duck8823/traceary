package types

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	// LiteralFingerprintBytes is the fixed persisted fingerprint width.
	LiteralFingerprintBytes = 16
	// LiteralSearchCursorVersion identifies the continuation envelope.
	LiteralSearchCursorVersion  = 1
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
	ExaminedSources  int64 `json:"examined_sources"`
	HighWater        int64 `json:"high_water"`
	Complete         bool  `json:"complete"`
}

// LiteralSearchBudget bounds physical and decoded work.
type LiteralSearchBudget struct {
	SourceRows                int
	StoredBytes, DecodedBytes int64
}

// NormalLiteralSearchBudget is the default preview resource envelope.
var NormalLiteralSearchBudget = LiteralSearchBudget{SourceRows: 512, StoredBytes: 8 << 20, DecodedBytes: 16 << 20}

// DeepLiteralSearchBudget increases resources without changing semantics.
var DeepLiteralSearchBudget = LiteralSearchBudget{SourceRows: 4096, StoredBytes: 64 << 20, DecodedBytes: 128 << 20}

// Valid reports whether every hard bound is positive.
func (b LiteralSearchBudget) Valid() bool {
	return b.SourceRows > 0 && b.StoredBytes > 0 && b.DecodedBytes > 0
}

// LiteralSearchRequest is the sibling tier-aware query contract.
type LiteralSearchRequest struct {
	Criteria      EventSearchCriteria
	Budget        LiteralSearchBudget
	Continuation  string
	BodyRuneLimit int
}

// LiteralSearchPage exposes matches and honest completeness metadata.
type LiteralSearchPage struct {
	Events             []BoundedEvent        `json:"events"`
	Tier               LiteralSearchTier     `json:"tier"`
	Coverage           LiteralSearchCoverage `json:"coverage"`
	PartialReason      string                `json:"partial_reason,omitempty"`
	Continuation       string                `json:"continuation,omitempty"`
	MatchContinuations []string              `json:"-"`
}

// ErrLiteralSearchCursorMismatch rejects replay against changed inputs.
var ErrLiteralSearchCursorMismatch = errors.New("literal search continuation does not match the request or active projection")

// ErrLiteralSearchQueryTooLarge rejects input before rune/gram allocation.
var ErrLiteralSearchQueryTooLarge = errors.New("literal search query exceeds byte limit")

// ErrLiteralSearchInvalidRequest identifies caller-controlled invariants.
var ErrLiteralSearchInvalidRequest = errors.New("invalid literal search request")

// LiteralSearchCursor binds progress to query and projection identity.
type LiteralSearchCursor struct {
	Version       int    `json:"v"`
	LastSequence  int64  `json:"last_sequence"`
	CriteriaHash  string `json:"criteria_hash"`
	Generation    string `json:"generation"`
	HighWater     int64  `json:"high_water"`
	QueryRevision int64  `json:"query_revision"`
}

type authenticatedLiteralCursor struct {
	Cursor LiteralSearchCursor `json:"cursor"`
	MAC    string              `json:"mac"`
}

// EncodeAuthenticated serializes a cursor with a store-owned integrity MAC.
func (c LiteralSearchCursor) EncodeAuthenticated(key []byte) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode literal cursor payload: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	envelope, err := json.Marshal(authenticatedLiteralCursor{Cursor: c, MAC: base64.RawURLEncoding.EncodeToString(mac.Sum(nil))})
	if err != nil {
		return "", fmt.Errorf("encode authenticated literal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(envelope), nil
}

// DecodeAuthenticatedLiteralSearchCursor verifies store ownership and integrity.
func DecodeAuthenticatedLiteralSearchCursor(value string, key []byte) (LiteralSearchCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	var env authenticatedLiteralCursor
	if json.Unmarshal(raw, &env) != nil {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	payload, err := json.Marshal(env.Cursor)
	if err != nil {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	got, err := base64.RawURLEncoding.DecodeString(env.MAC)
	if err != nil {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(got, mac.Sum(nil)) || env.Cursor.Version != LiteralSearchCursorVersion || env.Cursor.LastSequence < 0 {
		return LiteralSearchCursor{}, ErrLiteralSearchCursorMismatch
	}
	return env.Cursor, nil
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
