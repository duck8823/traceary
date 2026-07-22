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
