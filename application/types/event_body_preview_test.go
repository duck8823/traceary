package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestEventBodyPreviewOf_RejectsInvalidExtent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		stored    int
		original  types.Optional[int]
		createdAt time.Time
	}{
		"negative stored":   {-1, types.None[int](), now},
		"negative original": {0, types.Some(-1), now},
		"zero created at":   {0, types.None[int](), time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := apptypes.EventBodyPreviewOf(types.EventID("event"), "", tc.stored, tc.original, types.None[bool](), types.None[bool](), tc.createdAt); err == nil {
				t.Fatal("EventBodyPreviewOf() error = nil")
			}
		})
	}
}
