//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"testing"
)

func TestPTYControlPartialAndInterruptedWriteNeverRetry(t *testing.T) {
	for _, scenario := range []struct {
		n       int
		err     error
		outcome domain.ControlOutcome
	}{{2, nil, domain.ControlUnknown}, {2, unix.EINTR, domain.ControlUnknown}, {0, unix.EAGAIN, domain.ControlUnknown}, {5, nil, domain.ControlDelivered}} {
		calls := 0
		result, err := writeControlOnce(func(data []byte) (int, error) {
			calls++
			if string(data) != "bytes" {
				t.Fatal("write boundary changed data")
			}
			return scenario.n, scenario.err
		}, []byte("bytes"))
		if calls != 1 || result.WrittenBytes != scenario.n || result.Outcome != scenario.outcome {
			t.Fatal("partial write retried or prefix lost")
		}
		if scenario.outcome == domain.ControlUnknown && !errors.Is(err, app.ErrControlUnknown) {
			t.Fatal("partial/interrupted input falsely confirmed")
		}
	}
}
func TestPTYClosedHandleRejectsSimulatedFDReuseAndLateGroupStop(t *testing.T) {
	fds := make([]int, 2)
	if err := unix.Pipe(fds); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fds[0]); _ = unix.Close(fds[1]) }()
	if err := unix.SetNonblock(fds[0], true); err != nil {
		t.Fatal(err)
	}
	// A new unrelated FD occupies a remembered descriptor after the old ownership
	// barrier closed. No launcher/PID authority exists on this synthetic closed handle.
	child := &ptyChild{fd: fds[1], controlsClosed: true}
	input, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "late", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	if _, err := child.ApplyControl(context.Background(), input, []byte("x")); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("closed ownership admitted reused FD")
	}
	stop, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "late-stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if _, err := child.ApplyControl(context.Background(), stop, nil); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("closed ownership admitted late group signal")
	}
	var data [1]byte
	if n, err := unix.Read(fds[0], data[:]); n > 0 || !errors.Is(err, unix.EAGAIN) {
		t.Fatal("reused FD received old-generation input")
	}
}
