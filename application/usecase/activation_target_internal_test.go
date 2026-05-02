package usecase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestResolveActivationTarget_ReturnsCodexDescriptor(t *testing.T) {
	t.Parallel()

	target, err := resolveActivationTarget(apptypes.MemoryBridgeTargetCodex)
	if err != nil {
		t.Fatalf("resolveActivationTarget(codex) error = %v", err)
	}
	if got := target.Target(); got != apptypes.MemoryBridgeTargetCodex {
		t.Fatalf("Target() = %q, want codex", got)
	}
}

func TestResolveActivationTarget_ReturnsClaudeDescriptor(t *testing.T) {
	t.Parallel()

	target, err := resolveActivationTarget(apptypes.MemoryBridgeTargetClaude)
	if err != nil {
		t.Fatalf("resolveActivationTarget(claude) error = %v", err)
	}
	if got := target.Target(); got != apptypes.MemoryBridgeTargetClaude {
		t.Fatalf("Target() = %q, want claude", got)
	}
}

func TestResolveActivationTarget_RejectsUnknownTarget(t *testing.T) {
	t.Parallel()

	_, err := resolveActivationTarget(apptypes.MemoryBridgeTarget("unknown"))
	if err == nil || !strings.Contains(err.Error(), "unsupported memory activation target") {
		t.Fatalf("resolveActivationTarget(unknown) err = %v, want unsupported-target rejection", err)
	}
}

func TestResolveActivationTarget_RejectsGeminiUntilLaterIssues(t *testing.T) {
	t.Parallel()

	_, err := resolveActivationTarget(apptypes.MemoryBridgeTargetGemini)
	if err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Fatalf("resolveActivationTarget(gemini) err = %v, want not-supported-yet rejection", err)
	}
}

func TestCodexActivationTarget_PathOverrideWinsOverRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.md")
	got, err := codexActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   custom,
		Root:   filepath.Join(dir, "ignored"),
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if got.HostContextPath != custom {
		t.Fatalf("HostContextPath = %q, want %q (Path must override Root)", got.HostContextPath, custom)
	}
	if got.IsTwoFile() {
		t.Fatalf("codex resolution must not be two-file: %+v", got)
	}
}

func TestCodexActivationTarget_RootOverrideAppendsTracearyMd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := codexActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Root:   dir,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	want := filepath.Join(dir, codexActivationFileName)
	if got.HostContextPath != want {
		t.Fatalf("HostContextPath = %q, want %q", got.HostContextPath, want)
	}
}

func TestCodexActivationTarget_DefaultPathStillUsesCodexHomeAndFilename(t *testing.T) {
	t.Parallel()

	got, err := codexActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if !strings.HasSuffix(got.HostContextPath, filepath.Join(".codex", "memories", codexActivationFileName)) {
		t.Fatalf("default HostContextPath = %q, want suffix .codex/memories/%s (default Codex layout must not change)", got.HostContextPath, codexActivationFileName)
	}
}

func TestClaudeActivationTarget_PathOverrideWinsOverRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	custom := filepath.Join(dir, "nested", "OTHER.md")
	if err := os.MkdirAll(filepath.Dir(custom), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := claudeActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Path:   custom,
		Root:   filepath.Join(dir, "ignored"),
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if got.HostContextPath != custom {
		t.Fatalf("HostContextPath = %q, want %q (Path must override Root)", got.HostContextPath, custom)
	}
	wantExternal := filepath.Join(filepath.Dir(custom), filepath.FromSlash(claudeExternalMemoryRelDir), claudeExternalMemoryFileName)
	if got.ExternalMemoryPath != wantExternal {
		t.Fatalf("ExternalMemoryPath = %q, want %q (must derive from Path's directory)", got.ExternalMemoryPath, wantExternal)
	}
	if got.ImportPath != "./.traceary/memories/claude.md" {
		t.Fatalf("ImportPath = %q, want ./.traceary/memories/claude.md", got.ImportPath)
	}
}

func TestClaudeActivationTarget_RootOverrideUsesCanonicalLayout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := claudeActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetClaude,
		Root:   dir,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	wantHost := filepath.Join(dir, claudeHostContextFileName)
	if got.HostContextPath != wantHost {
		t.Fatalf("HostContextPath = %q, want %q", got.HostContextPath, wantHost)
	}
	wantExternal := filepath.Join(dir, filepath.FromSlash(claudeExternalMemoryRelDir), claudeExternalMemoryFileName)
	if got.ExternalMemoryPath != wantExternal {
		t.Fatalf("ExternalMemoryPath = %q, want %q", got.ExternalMemoryPath, wantExternal)
	}
	if got.ImportPath != "./.traceary/memories/claude.md" {
		t.Fatalf("ImportPath = %q, want ./.traceary/memories/claude.md", got.ImportPath)
	}
	if !got.IsTwoFile() {
		t.Fatalf("claude resolution must be two-file: %+v", got)
	}
}

func TestClaudeActivationTarget_DefaultRootDetectsGitAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatalf("MkdirAll(.git): %v", err)
	}
	nested := filepath.Join(root, "pkg", "feature")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll(nested): %v", err)
	}
	// macOS exposes t.TempDir() as `/var/folders/...` while the
	// resolved path is `/private/var/folders/...`. The detector calls
	// os.Getwd from inside `nested`, which the kernel returns through
	// the resolved (private) prefix, so the assertion must compare
	// against the same prefix.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	withWorkingDir(t, nested, func() {
		got, err := claudeActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
			Target: apptypes.MemoryBridgeTargetClaude,
		})
		if err != nil {
			t.Fatalf("Resolve error = %v", err)
		}
		wantHost := filepath.Join(resolvedRoot, claudeHostContextFileName)
		if got.HostContextPath != wantHost {
			t.Fatalf("HostContextPath = %q, want git ancestor %q", got.HostContextPath, wantHost)
		}
	})
}

func TestClaudeActivationTarget_DefaultRootFallsBackToCwdWhenNoGit(t *testing.T) {
	// Not parallel-safe: withWorkingDir uses os.Chdir which mutates
	// process-global state, so this test must serialise with the other
	// chdir-based test (TestClaudeActivationTarget_DefaultRootDetectsGitAncestor).
	root := t.TempDir()
	withWorkingDir(t, root, func() {
		got, err := claudeActivationTarget{}.Resolve(apptypes.MemoryActivationCriteria{
			Target: apptypes.MemoryBridgeTargetClaude,
		})
		if err != nil {
			t.Fatalf("Resolve error = %v", err)
		}
		// Use os.Getwd() to compare evaluated paths in case the
		// platform resolves the temp dir through symlinks (macOS
		// /var → /private/var).
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		wantHost := filepath.Join(cwd, claudeHostContextFileName)
		if got.HostContextPath != wantHost {
			t.Fatalf("HostContextPath = %q, want cwd fallback %q", got.HostContextPath, wantHost)
		}
	})
}

// withWorkingDir runs fn with the process working directory set to dir
// and restores the previous working directory afterwards. The helper is
// not parallel-safe; tests using it serialise their os.Chdir calls
// because they share the global cwd.
func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore Chdir(%q): %v", previous, err)
		}
	})
	fn()
}
