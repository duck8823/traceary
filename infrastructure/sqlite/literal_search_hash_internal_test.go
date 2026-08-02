package sqlite

import (
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestLiteralCriteriaHashSeparatesNULDelimitedFields(t *testing.T) {
	t.Parallel()
	first := apptypes.NewEventSearchCriteriaBuilder(1).Query("a\x00b").Workspace(types.Workspace("c")).Build()
	second := apptypes.NewEventSearchCriteriaBuilder(1).Query("a").Workspace(types.Workspace("b\x00c")).Build()
	firstHash := literalCriteriaHash(first, apptypes.CharacterizeLiteralQuery(first.Query()).Canonical(), 500)
	secondHash := literalCriteriaHash(second, apptypes.CharacterizeLiteralQuery(second.Query()).Canonical(), 500)
	if firstHash == secondHash {
		t.Fatalf("ambiguous criteria hashes collided: %q", firstHash)
	}
}
