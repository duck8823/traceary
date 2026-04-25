package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectTracearyOnPath(t *testing.T) {
	origLookPath := execLookPathFunc
	origExecutable := osExecutableFunc
	t.Cleanup(func() {
		execLookPathFunc = origLookPath
		osExecutableFunc = origExecutable
	})

	t.Run("fails when traceary is not on PATH", func(t *testing.T) {
		t.Setenv("PATH", "")
		execLookPathFunc = origLookPath
		got := inspectTracearyOnPath()
		if got.Status != doctorStatusFail {
			t.Fatalf("status = %q, want fail", got.Status)
		}
		if got.FixCommand == "" {
			t.Fatalf("FixCommand is empty")
		}
	})

	t.Run("warns when multiple traceary executables are on PATH", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()
		bin1 := writeExecutable(t, dir1, "traceary")
		_ = writeExecutable(t, dir2, "traceary")
		t.Setenv("PATH", strings.Join([]string{dir1, dir2}, string(os.PathListSeparator)))
		execLookPathFunc = origLookPath
		osExecutableFunc = func() (string, error) { return bin1, nil }

		got := inspectTracearyOnPath()
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, dir1) || !strings.Contains(got.Message, dir2) {
			t.Fatalf("message should include both PATH directories, got %q", got.Message)
		}
	})
}

func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
