//go:build !darwin

package runtime

import (
	"context"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// Other platforms retain optional history delivery, but have no certified
// native power witness. Never fabricate a stable tuple to enable causal intake.
type nativeContinuitySampler struct{}

func (nativeContinuitySampler) Sample(context.Context) domain.ContinuitySample {
	return domain.ContinuitySample{}
}
