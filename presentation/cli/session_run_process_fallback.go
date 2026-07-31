//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package cli

import (
	"os/exec"
	"time"
)

const oneShotProcessWaitDelay = 2 * time.Second

func configureOneShotProcess(command *exec.Cmd) {
	command.WaitDelay = oneShotProcessWaitDelay
}

func oneShotSignalExitCode(*exec.ExitError) (int, bool) { return 0, false }

// oneShotOwnExitCode always reports no child-owned exit on this platform.
// Without wait-status signal inspection, a supervisor-initiated kill
// (TerminateProcess) surfaces as an ordinary exit code 1, indistinguishable
// from a child-owned exit. The conservative contract is therefore: a child
// that ran to completion cleanly still records success, but any non-zero
// exit racing with an expired deadline or cancellation is classified as
// timeout or aborted_stream rather than the child's own failure.
func oneShotOwnExitCode(*exec.ExitError) (int, bool) { return 0, false }
