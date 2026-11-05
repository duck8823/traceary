//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"golang.org/x/sys/unix"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPTYExplicitMasterClosePreservesHUPIgnoringChildAndGuardedStop(t *testing.T) {
	launcher, descriptor, result := ptyFixture(t, "FIXTURE_OUTPUT_MODE=ignore_hup")
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	controller := child.(app.ControlledChild)
	stop, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "explicit-cleanup", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := child.Wait(); done <- e }()
	t.Cleanup(func() {
		_, _ = controller.ApplyControl(context.Background(), stop, nil)
		select {
		case <-done:
		case <-time.After(17 * time.Second):
			t.Error("finite native fixture failed cleanup")
		}
	})
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, e := os.Stat(result + ".ready"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	closer, ok := child.(app.ClosingPTYChild)
	if !ok {
		t.Fatal("owned native PTY lacks explicit master-close port")
	}
	oldFD, ok := infra.PTYMasterForTest(child)
	if !ok {
		t.Fatal("missing test-only owned master witness")
	}
	selection := app.OutputSelection{Identity: descriptor.Identity(), Worktree: descriptor.Target().Worktree().ID(), Profile: descriptor.Snapshot().Profile.ID()}
	sub, err := child.(app.OutputChild).SubscribeOutput(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if err = closer.ValidateTerminalClose(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancelled, cancelledNow := context.WithCancel(context.Background())
	cancelledNow()
	if e := closer.CloseTerminal(cancelled); !errors.Is(e, context.Canceled) {
		t.Fatal("pre-admission cancellation not respected", e)
	}
	if _, open := infra.PTYMasterForTest(child); !open {
		t.Fatal("cancelled closure lost master")
	}
	var joined sync.WaitGroup
	failure := make(chan error, 8)
	for range 8 {
		joined.Add(1)
		go func() { defer joined.Done(); failure <- closer.CloseTerminal(context.Background()) }()
	}
	joined.Wait()
	close(failure)
	for e := range failure {
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, open := infra.PTYMasterForTest(child); open {
		t.Fatal("master remained open after joined closure")
	}
	select {
	case e := <-done:
		t.Fatalf("master close claimed/caused exit of HUP-ignoring fixture: %v", e)
	case <-time.After(50 * time.Millisecond):
	}
	input, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "late-input", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	resize, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "late-resize", Kind: domain.ControlResize, ResizeSequence: 1, Rows: 30, Cols: 100})
	for _, intent := range []domain.ControlIntent{input, resize} {
		if e := controller.ValidateControl(context.Background(), intent); !errors.Is(e, domain.ErrChanged) {
			t.Fatal("closed terminal admitted input/resize", e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sawTerminalClose := false
	for {
		f, e := sub.Next(ctx)
		if e != nil {
			t.Fatal("missing explicit terminal capture end", e)
		}
		if f.Info.Ended && string(f.Info.Reason) == "terminal_closed" {
			sawTerminalClose = true
		}
		if f.Kind == "end" {
			break
		}
	}
	if !sawTerminalClose {
		t.Fatal("intentional master-close capture loss not distinguished")
	}
	// Duplicate a test-owned file at/above the old FD; never force Dup2 onto an
	// unowned descriptor. Reap must not close a newly reused numeric descriptor.
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reused, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, oldFD)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(reused) }()
	if _, err = controller.ApplyControl(context.Background(), stop, nil); err != nil {
		t.Fatal("guarded original-group Stop unavailable after master close", err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("post-close Stop/sole Wait stalled")
	}
	// Keep cleanup's terminal result available without another signal/Wait owner.
	done <- nil
	if _, err = unix.FcntlInt(uintptr(reused), unix.F_GETFD, 0); err != nil {
		t.Fatal("native reaper closed a reused descriptor", err)
	}
	if _, err = unix.FcntlInt(file.Fd(), unix.F_GETFD, 0); err != nil {
		t.Fatal("native reaper closed original test file occupying old descriptor", err)
	}
	if reused == oldFD || int(file.Fd()) == oldFD {
		t.Log("exact numeric FD reuse survived the actual native reaper")
	}
	if reused != oldFD && int(file.Fd()) != oldFD {
		t.Log("old FD occupied elsewhere; native reused-nearby test passed, exact numeric reuse not certified")
	}
	if err = closer.CloseTerminal(context.Background()); err != nil {
		t.Fatal("late closure not idempotent", err)
	}
	if _, err = controller.ApplyControl(context.Background(), stop, nil); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("late Stop passed reaper barrier", err)
	}
}
