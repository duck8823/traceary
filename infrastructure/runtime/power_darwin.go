//go:build darwin

package runtime

import (
	"context"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
)

// Only these fixed read-only kernel keys are sampled. No shell, settings write,
// power assertion or suspend request is involved. Sampling is not native activity.
type nativeContinuitySampler struct{}

func (nativeContinuitySampler) Sample(ctx context.Context) domain.ContinuitySample {
	return sampleNativePower(ctx, time.Now, unix.SysctlTimeval)
}

func sampleNativePower(ctx context.Context, now func() time.Time, read func(string) (*unix.Timeval, error)) domain.ContinuitySample {
	before := now()
	var pairs [2]domain.PowerWitness
	for i := range pairs {
		for j, key := range [2]string{"kern.sleeptime", "kern.waketime"} {
			if ctx.Err() != nil {
				return domain.ContinuitySample{}
			}
			tv, err := read(key)
			if err != nil {
				return domain.ContinuitySample{}
			}
			stamp, valid := nativePowerTime(tv)
			if !valid {
				return domain.ContinuitySample{}
			}
			if j == 0 {
				pairs[i].Sleep = stamp
			} else {
				pairs[i].Wake = stamp
			}
		}
	}
	after := now()
	elapsed, wallElapsed := after.Sub(before), after.Round(0).Sub(before.Round(0))
	if ctx.Err() != nil || before.IsZero() || after.IsZero() || elapsed < 0 || elapsed > app.ObservationRefreshBudget ||
		wallElapsed < 0 || wallElapsed > app.ObservationRefreshBudget || pairs[0] != pairs[1] {
		return domain.ContinuitySample{}
	}
	// Readable sleep>wake remains Available here: the domain classifies this
	// closed fence as sleep_wake, rather than confusing it with an API failure.
	return domain.ContinuitySample{At: after, Power: pairs[1], Available: true}
}

func nativePowerTime(tv *unix.Timeval) (time.Time, bool) {
	if tv == nil {
		return time.Time{}, false
	}
	if tv.Sec == 0 && tv.Usec == 0 {
		return time.Time{}, true
	}
	// Nonzero timestamps must be after the Unix epoch and representable through
	// the bounded civil-time range; reject partial zero and normalization shortcuts.
	// Native padding is not evidence. SysctlTimeval validates the ABI byte size.
	if tv.Sec <= 0 || tv.Sec > 253402300799 || tv.Usec < 0 || tv.Usec >= 1_000_000 {
		return time.Time{}, false
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000).UTC(), true
}
