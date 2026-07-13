//go:build windows

package cli

import "os/exec"

func configureDetachedHookProcess(_ *exec.Cmd) {}
