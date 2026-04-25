package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	execLookPathFunc = exec.LookPath
	osExecutableFunc = os.Executable
)

func inspectTracearyOnPath() doctorCheck {
	resolved, err := execLookPathFunc("traceary")
	if err != nil {
		return doctorCheck{
			Name:       "path",
			Status:     doctorStatusFail,
			Message:    "traceary executable was not found on PATH",
			Hint:       "install traceary or add its bin directory to PATH",
			FixCommand: "export PATH=\"/path/to/traceary/bin:$PATH\"",
		}
	}
	matches := findTracearyExecutablesOnPath()
	if len(matches) > 1 {
		dirs := make([]string, 0, len(matches))
		for _, match := range matches {
			dirs = append(dirs, filepath.Dir(match))
		}
		return doctorCheck{
			Name:       "path",
			Status:     doctorStatusWarn,
			Message:    localizef("PATH resolves traceary to %s, but multiple traceary executables were found in PATH directories: %s", "PATH の traceary は %s に解決されますが、複数の PATH directory に traceary 実行ファイルがあります: %s", resolved, strings.Join(dirs, ", ")),
			Hint:       "remove stale traceary entries or reorder PATH",
			FixCommand: "which -a traceary",
		}
	}
	if current, currentErr := osExecutableFunc(); currentErr == nil && current != "" && !sameFile(resolved, current) {
		return doctorCheck{
			Name:       "path",
			Status:     doctorStatusWarn,
			Message:    localizef("PATH resolves traceary to %s (directory: %s), which differs from the running binary %s", "PATH の traceary は %s (directory: %s) に解決されますが、実行中の binary %s と異なります", resolved, filepath.Dir(resolved), current),
			Hint:       "ensure the intended traceary binary appears first on PATH",
			FixCommand: "which -a traceary",
		}
	}
	return doctorCheck{
		Name:    "path",
		Status:  doctorStatusPass,
		Message: localizef("PATH resolves traceary to %s (directory: %s)", "PATH の traceary は %s (directory: %s) に解決されます", resolved, filepath.Dir(resolved)),
	}
}

func findTracearyExecutablesOnPath() []string {
	seen := map[string]struct{}{}
	matches := []string{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "traceary")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			abs = candidate
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		matches = append(matches, abs)
	}
	return matches
}

func sameFile(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr == nil && berr == nil {
		return os.SameFile(ai, bi)
	}
	if errors.Is(aerr, os.ErrNotExist) || errors.Is(berr, os.ErrNotExist) {
		return false
	}
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return aa == bb
}
