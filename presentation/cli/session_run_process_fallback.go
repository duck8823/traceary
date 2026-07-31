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

// oneShotOwnExitCode cannot prove on this platform whether an exit status
// came from the child itself, so context errors keep priority there.
func oneShotOwnExitCode(*exec.ExitError) (int, bool) { return 0, false }
