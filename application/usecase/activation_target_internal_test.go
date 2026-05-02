package usecase

import (
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

func TestResolveActivationTarget_RejectsUnknownTarget(t *testing.T) {
	t.Parallel()

	_, err := resolveActivationTarget(apptypes.MemoryBridgeTarget("unknown"))
	if err == nil || !strings.Contains(err.Error(), "unsupported memory activation target") {
		t.Fatalf("resolveActivationTarget(unknown) err = %v, want unsupported-target rejection", err)
	}
}

func TestResolveActivationTarget_RejectsClaudeAndGeminiUntilLaterIssues(t *testing.T) {
	t.Parallel()

	cases := []apptypes.MemoryBridgeTarget{
		apptypes.MemoryBridgeTargetClaude,
		apptypes.MemoryBridgeTargetGemini,
	}
	for _, target := range cases {
		t.Run(string(target), func(t *testing.T) {
			t.Parallel()
			_, err := resolveActivationTarget(target)
			if err == nil || !strings.Contains(err.Error(), "not supported yet") {
				t.Fatalf("resolveActivationTarget(%q) err = %v, want not-supported-yet rejection", target, err)
			}
		})
	}
}

func TestCodexActivationTarget_PathOverrideWinsOverRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.md")
	got, err := codexActivationTarget{}.ResolvePath(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Path:   custom,
		Root:   filepath.Join(dir, "ignored"),
	})
	if err != nil {
		t.Fatalf("ResolvePath error = %v", err)
	}
	if got != custom {
		t.Fatalf("ResolvePath = %q, want %q (Path must override Root)", got, custom)
	}
}

func TestCodexActivationTarget_RootOverrideAppendsTracearyMd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := codexActivationTarget{}.ResolvePath(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
		Root:   dir,
	})
	if err != nil {
		t.Fatalf("ResolvePath error = %v", err)
	}
	want := filepath.Join(dir, codexActivationFileName)
	if got != want {
		t.Fatalf("ResolvePath = %q, want %q", got, want)
	}
}

func TestCodexActivationTarget_DefaultPathStillUsesCodexHomeAndFilename(t *testing.T) {
	t.Parallel()

	got, err := codexActivationTarget{}.ResolvePath(apptypes.MemoryActivationCriteria{
		Target: apptypes.MemoryBridgeTargetCodex,
	})
	if err != nil {
		t.Fatalf("ResolvePath error = %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".codex", "memories", codexActivationFileName)) {
		t.Fatalf("default ResolvePath = %q, want suffix .codex/memories/%s (default Codex layout must not change)", got, codexActivationFileName)
	}
}
