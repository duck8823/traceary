//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

func configureBrokenPipeSignalHandling() {
	signal.Ignore(syscall.SIGPIPE)
}
