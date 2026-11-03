//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestPTYNativeWatcherDoesNotReapOrConfuseStopContinue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := checkReapPolicy(ctx); errors.Is(err, domain.ErrUnsupported) {
		t.Skip("unverified signal ABI")
	} else if err != nil {
		t.Fatal(err)
	}
	watch, err := prepareExitWatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.close()
	command := exec.Command("/bin/sleep", "2")
	command.Env = []string{"PATH=/usr/bin:/bin"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	// No other Wait exists in this test: the actual child PID stays unreaped and
	// cannot be recycled while these owned-handle signals and the watcher run.
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	if err = watch.bind(ctx, command.Process.Pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- watch.wait() }()
	for _, signal := range []syscall.Signal{syscall.SIGSTOP, syscall.SIGCONT} {
		if err = command.Process.Signal(signal); err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-done:
			t.Fatalf("stop/continue reported as exit: %v", err)
		case <-time.After(30 * time.Millisecond):
		}
	}
	if err = command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native exit witness missed owned exit")
	}
	// Actual Wait below must still recover the exit status: the watcher did not reap.
	state, err := command.Process.Wait()
	if err != nil || state.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatal("non-reaping witness consumed owned exit status")
	}
}
