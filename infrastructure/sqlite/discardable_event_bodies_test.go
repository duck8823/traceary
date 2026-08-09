package sqlite

import (
	"strings"
	"testing"
)

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
