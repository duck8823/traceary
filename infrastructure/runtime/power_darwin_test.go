//go:build darwin

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
)

func TestNativePowerTimevalRejectsMalformedAndPartialZero(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *unix.Timeval
		valid bool
	}{
		{"missing", nil, false},
		{"zero", &unix.Timeval{}, true},
		{"positive", &unix.Timeval{Sec: 100, Usec: 999999}, true},
		{"partial zero", &unix.Timeval{Usec: 1}, false},
		{"negative seconds", &unix.Timeval{Sec: -1}, false},
		{"negative micros", &unix.Timeval{Sec: 100, Usec: -1}, false},
		{"micros overflow", &unix.Timeval{Sec: 100, Usec: 1000000}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stamp, ok := nativePowerTime(tc.value)
			if ok != tc.valid {
				t.Fatalf("invalid native timeval classification: %v", ok)
			}
			if ok && tc.value.Sec == 0 && !stamp.IsZero() {
				t.Fatal("native no-cycle witness became a real event")
			}
			if ok && tc.value.Sec > 0 && (stamp.Unix() != tc.value.Sec || stamp.Nanosecond() != int(tc.value.Usec)*1000) {
				t.Fatal("native timestamp precision lost")
			}
		})
	}
}

func TestNativePowerSamplingCompletesBeforeStampAndRefusesTornSlowOrCanceledRead(t *testing.T) {
	base := time.Now().Round(0)
	for _, mode := range []string{"stable", "zero", "sleeping", "torn", "read-error", "slow", "clock-reversal", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before, after := base, base.Add(time.Millisecond)
			if mode == "slow" {
				after = base.Add(app.ObservationRefreshBudget + time.Nanosecond)
			}
			if mode == "clock-reversal" {
				after = base.Add(-time.Second)
			}
			clockCalls, readCalls := 0, 0
			now := func() time.Time {
				clockCalls++
				if clockCalls == 1 {
					return before
				}
				return after
			}
			read := func(name string) (*unix.Timeval, error) {
				want := []string{"kern.sleeptime", "kern.waketime", "kern.sleeptime", "kern.waketime"}
				if readCalls >= len(want) || name != want[readCalls] {
					t.Fatal("unexpected native read key/order")
				}
				readCalls++
				if mode == "read-error" {
					return nil, errors.New("fixture unavailable")
				}
				if mode == "canceled" {
					cancel()
				}
				value := &unix.Timeval{}
				if mode != "zero" {
					value.Sec = base.Unix() - 2
					if name == "kern.waketime" {
						value.Sec++
					}
					if mode == "sleeping" && name == "kern.waketime" {
						value.Sec -= 2
					}
					if mode == "torn" && readCalls == 4 {
						value.Usec = 1
					}
				}
				return value, nil
			}
			sample := sampleNativePower(ctx, now, read)
			valid := mode == "stable" || mode == "zero" || mode == "sleeping"
			if sample.Available != valid {
				t.Fatalf("sample falsely accepted/refused: %+v", sample)
			}
			if valid && !sample.At.Equal(after) {
				t.Fatal("sample stamped before native read completion")
			}
			if mode == "sleeping" {
				owner, err := domain.NewObservationContinuity(time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if err = owner.Observe(sample); err != nil || owner.Snapshot().Available || owner.Snapshot().Reason != domain.FreshnessSleepWake {
					t.Fatal("readable native sleep lost its domain classification")
				}
			}
		})
	}
}

func TestNativePowerPassiveReadReportsUnavailableOrValidatedTuple(t *testing.T) {
	sample := (nativeContinuitySampler{}).Sample(context.Background())
	if !sample.Available {
		t.Log("native power read unavailable; NOT physical sleep/wake qualification")
		return
	}
	if sample.At.IsZero() {
		t.Fatal("native sample missing local completion clock")
	}
	// Never suspend the workstation. This tests actual read-only API access only.
	t.Log("read-only native power tuple available; NOT physical sleep/wake qualification")
}
