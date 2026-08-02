package types_test

import (
	"fmt"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestLiteralQueryCharacterizationAndFingerprints(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query string
		filterable  bool
	}{
		{"unicode", "障害/経路", true},
		{"case", "Not Found", true},
		{"path", "/Users/a/src", true},
		{"identifier", "evt_01JABC", true},
		{"error fragment", "EOF: reset", true},
		{"short", "ßx", false},
		{"unicode case mapping", "ΟΣΣ", false},
		{"empty", "  ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apptypes.CharacterizeLiteralQuery(tc.query)
			if got.Filterable() != tc.filterable {
				t.Fatalf("Filterable() = %v, want %v", got.Filterable(), tc.filterable)
			}
			if tc.filterable && len(got.Fingerprints()) == 0 {
				t.Fatal("Fingerprints() is empty")
			}
		})
	}
}

func TestLiteralFingerprintsAreDeterministicFixedSizeAndMatchCaseInsensitively(t *testing.T) {
	t.Parallel()
	query := apptypes.CharacterizeLiteralQuery("ERR/Path-01")
	body := apptypes.CharacterizeLiteralQuery("prefix err/path-01 suffix")
	if !body.Contains(query) {
		t.Fatal("Contains() = false, want true")
	}
	first, second := query.Fingerprints(), apptypes.CharacterizeLiteralQuery("err/path-01").Fingerprints()
	if len(first) != len(second) {
		t.Fatalf("fingerprint lengths = %d/%d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("fingerprint[%d] differs", i)
		}
		if len(first[i]) != apptypes.LiteralFingerprintBytes {
			t.Fatalf("fingerprint bytes = %d", len(first[i]))
		}
	}
}

func TestLiteralFingerprintCandidatesHaveNoFalseNegativesForCharacterizedClass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, needle, body string }{
		{"unicode", "障害/経路", "prefix 障害/経路 suffix"},
		{"case", "NOT FOUND", "error: not found while opening"},
		{"path", "/Users/a/src", "open /users/a/src/main.go"},
		{"identifier", "evt_01JABC", "id=EVT_01jabc accepted"},
		{"error fragment", "EOF: RESET", "transport eof: reset by peer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := apptypes.CharacterizeLiteralQuery(tc.needle)
			body := apptypes.CharacterizeLiteralQuery(tc.body)
			if !body.Contains(query) {
				t.Fatal("fixture is not a canonical match")
			}
			available := map[string]bool{}
			for _, fp := range body.Fingerprints() {
				available[fp] = true
			}
			for _, fp := range query.Fingerprints() {
				if !available[fp] {
					t.Fatalf("query fingerprint absent from matching body")
				}
			}
		})
	}
}

func TestLiteralSearchCursorBindsCriteriaAndProjection(t *testing.T) {
	t.Parallel()
	cursor := apptypes.LiteralSearchCursor{Version: 1, LastSequence: 42, CriteriaHash: "criteria", Generation: "g1", HighWater: 90, QueryRevision: 3}
	encoded, err := cursor.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := apptypes.DecodeLiteralSearchCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != cursor {
		t.Fatalf("decoded = %+v, want %+v", decoded, cursor)
	}
	if err := decoded.Validate("criteria", "g1", 90, 3); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate("changed", "g1", 90, 3); err == nil {
		t.Fatal("Validate() error = nil")
	}
}

func TestLiteralSearchCursorAuthenticationRejectsTampering(t *testing.T) {
	t.Parallel()
	key := []byte("01234567890123456789012345678901")
	cursor := apptypes.LiteralSearchCursor{Version: 1, LastSequence: 42, CriteriaHash: "criteria", Generation: "g", HighWater: 90, QueryRevision: 3}
	encoded, err := cursor.EncodeAuthenticated(key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := apptypes.DecodeAuthenticatedLiteralSearchCursor(encoded, key)
	if err != nil || decoded != cursor {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	raw := []byte(encoded)
	if raw[len(raw)-1] == 'A' {
		raw[len(raw)-1] = 'B'
	} else {
		raw[len(raw)-1] = 'A'
	}
	if _, err := apptypes.DecodeAuthenticatedLiteralSearchCursor(string(raw), key); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}

func TestLiteralQueryOversizeFingerprintSetFallsBackToInclusiveVerification(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "%03x", i)
	}
	if apptypes.CharacterizeLiteralQuery(b.String()).Filterable() {
		t.Fatal("oversize query remained filterable")
	}
}
