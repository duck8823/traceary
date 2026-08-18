//go:build darwin || linux

package sqlite

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func describeStoreLeaseHolders(lockPath string) string {
	cmd := exec.Command("lsof", "-F", "pcn", "--", lockPath)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	var holders []string
	var pid, command string
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			if pid != "" {
				holders = append(holders, formatLeaseHolder(pid, command))
			}
			pid = string(line[1:])
			command = ""
		case 'c':
			command = string(line[1:])
		}
	}
	if pid != "" {
		holders = append(holders, formatLeaseHolder(pid, command))
	}
	if len(holders) == 0 {
		return ""
	}
	return strings.Join(holders, "; ")
}

func formatLeaseHolder(pid, command string) string {
	if _, err := strconv.Atoi(pid); err != nil {
		return strings.TrimSpace(command + " " + pid)
	}
	if command == "" {
		return "pid=" + pid
	}
	return fmt.Sprintf("pid=%s command=%s", pid, command)
}
