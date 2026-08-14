package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyAntigravityHeadlessMarkers_DiagnosesPermissionOnZeroExit(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}

	work := t.TempDir()
	agy := filepath.Join(work, "agy")
	// Matches the live host: exit 0, empty stdout, permission denial on stderr.
	script := "#!/bin/sh\nprintf '%s\\n' 'jetski: no output produced — a tool required the \"command\" permission that headless mode cannot prompt for, so it was auto-denied.' >&2\nexit 0\n"
	if err := os.WriteFile(agy, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	dummy := filepath.Join(work, "traceary")
	if err := os.WriteFile(dummy, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", filepath.Join(root, "scripts/verify-antigravity-headless-markers.sh"))
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"PATH="+work+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TRACEARY_DOGFOOD_BINARY="+dummy,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("probe exit = 0, want permission diagnosis; output = %q", out)
	}
	got := string(out)
	if !strings.Contains(got, "scoped hook permission is absent or shadowed") {
		t.Fatalf("output = %q, want permission diagnosis", got)
	}
	if strings.Contains(got, "expected public response marker was not returned") {
		t.Fatalf("output = %q, must not fold a permission denial into a missing marker", got)
	}
}
