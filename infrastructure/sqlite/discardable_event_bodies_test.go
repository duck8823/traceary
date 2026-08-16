package sqlite

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestSelectDiscardableEventBodiesKindAllowlist pins that the SQL allowlist for
// irreversible body discard names exactly {transcript}. Widening it requires an
// explicit policy decision; this test fails closed if a kind is added without
// updating the documentation in docs/storage/README.md.
func TestSelectDiscardableEventBodiesKindAllowlist(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`(?i)\bkind\s*=\s*'([^']+)'`)
	var kinds []string
	for _, m := range re.FindAllStringSubmatch(selectDiscardableEventBodiesQuery, -1) {
		kinds = append(kinds, m[1])
	}
	sort.Strings(kinds)
	if diff := cmp.Diff([]string{"transcript"}, kinds); diff != "" {
		t.Errorf("discardable kind allowlist mismatch (-want +got):\n%s", diff)
	}
}

func TestDiscardableEventBodiesWrappersContainPlaceholder(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"count":   countDiscardableEventBodiesQuery,
		"discard": discardEventBodiesQuery,
		"list":    listRawBodyCandidatesQuery,
		"verify":  verifyRawBodyCandidateStateQuery,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(query, discardableEventBodiesPlaceholder) {
				t.Fatalf("wrapper has no %q", discardableEventBodiesPlaceholder)
			}
			if _, err := composeDiscardableEventBodiesQuery(query); err != nil {
				t.Fatalf("composeDiscardableEventBodiesQuery() error = %v", err)
			}
		})
	}
}
