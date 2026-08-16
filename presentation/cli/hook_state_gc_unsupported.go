//go:build !unix

package cli

func hookProcessAlive(pid int) bool {
	// Without a portable liveness probe, keep per-PID files so a live host
	// is never deleted. Diagnostics and ended markers still age out.
	return pid > 0
}
