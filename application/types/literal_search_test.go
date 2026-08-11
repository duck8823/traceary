package types_test

import (
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
