package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectRepoContextFromDir(t *testing.T) {
	t.Run("prefers normalized remote origin", func(t *testing.T) {
		repoDir := initGitRepoForContextTest(t)
		runGitCommandForContextTest(t, repoDir, "remote", "add", "origin", "git@github.com:duck8823/traceary.git")

		got, err := detectRepoContextFromDir(context.Background(), repoDir)
		if err != nil {
			t.Fatalf("detectRepoContextFromDir() error = %v", err)
		}
		if want := "github.com/duck8823/traceary"; got != want {
			t.Fatalf("detectRepoContextFromDir() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to local git root when origin is missing", func(t *testing.T) {
		repoDir := initGitRepoForContextTest(t)
		nestedDir := filepath.Join(repoDir, "nested", "workspace")
		if err := os.MkdirAll(nestedDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}

		got, err := detectRepoContextFromDir(context.Background(), nestedDir)
		if err != nil {
			t.Fatalf("detectRepoContextFromDir() error = %v", err)
		}
		want := normalizeLocalWorkContextPath(gitCommandOutputForContextTest(t, repoDir, "rev-parse", "--show-toplevel"))
		if got != want {
			t.Fatalf("detectRepoContextFromDir() = %q, want %q", got, want)
		}
	})
}

func initGitRepoForContextTest(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGitCommandForContextTest(t, repoDir, "init")

	return repoDir
}

func runGitCommandForContextTest(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v, output = %s", args, err, string(output))
	}
}

func gitCommandOutputForContextTest(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v, output = %s", args, err, string(output))
	}

	return string(output)
}
