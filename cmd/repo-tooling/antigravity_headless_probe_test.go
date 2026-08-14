package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyAntigravityHeadlessMarkers_ClassifiesAgyOutcomes(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}

	tests := []struct {
		name       string
		agyScript  string
		wantNeedle string
		forbid     string
	}{
		{
			name:       "permission denial on exit 0",
			agyScript:  "#!/bin/sh\nprintf '%s\\n' 'jetski: no output produced — a tool required the \"command\" permission that headless mode cannot prompt for, so it was auto-denied.' >&2\nexit 0\n",
			wantNeedle: "scoped hook permission is absent or shadowed",
			forbid:     "expected public response marker was not returned",
		},
		{
			name:       "nonzero exit without permission wording",
			agyScript:  "#!/bin/sh\nprintf '%s\\n' 'model crashed' >&2\nexit 3\n",
			wantNeedle: "agy exited before the marker response",
			forbid:     "expected public response marker was not returned",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			work := t.TempDir()
			agy := filepath.Join(work, "agy")
			if err := os.WriteFile(agy, []byte(tt.agyScript), 0o700); err != nil {
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
				t.Fatalf("probe exit = 0, want diagnosis; output = %q", out)
			}
			got := string(out)
			if !strings.Contains(got, tt.wantNeedle) {
				t.Fatalf("output = %q, want %q", got, tt.wantNeedle)
			}
			if tt.forbid != "" && strings.Contains(got, tt.forbid) {
				t.Fatalf("output = %q, must not contain %q", got, tt.forbid)
			}
		})
	}
}
