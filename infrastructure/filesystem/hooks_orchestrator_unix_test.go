//go:build unix

package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

// TestHooksOrchestrator_InstallRefusesSymlink asserts that Install refuses to
// read or write through an attacker-supplied symlink at the destination path.
// Without this guard a merge would follow the link and inject the traceary
// hook into an unrelated file. Unix-only because safe_fs_other.go (non-unix)
// delegates to os.* and does not harden against symlink traversal.
func TestHooksOrchestrator_InstallRefusesSymlink(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte(`{"sensitive":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	if err := os.Symlink(victim, settingsPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := orchestrator.Install(
		context.Background(),
		"claude",
		"/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		false,
	); err == nil {
		t.Fatalf("Install() error = nil, want symlink refusal")
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile(victim) error = %v", err)
	}
	if string(got) != `{"sensitive":true}` {
		t.Fatalf("victim content = %q, want untouched", got)
	}
}

// TestHooksOrchestrator_InstallRefusesAncestorSymlink asserts that Install
// refuses to traverse an attacker-controlled symlink on an ancestor
// directory (e.g. projectDir/.claude → victimDir). Without the ancestor
// check, MkdirAll/WriteFile would follow the link and write a fresh
// settings.json under the victim directory. Unix-only, same reason as
// TestHooksOrchestrator_InstallRefusesSymlink.
func TestHooksOrchestrator_InstallRefusesAncestorSymlink(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	victimDir := t.TempDir()
	claudeDir := filepath.Join(projectDir, ".claude")
	if err := os.Symlink(victimDir, claudeDir); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := orchestrator.Install(
		context.Background(),
		"claude",
		"/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		false,
	); err == nil {
		t.Fatalf("Install() error = nil, want ancestor symlink refusal")
	}

	entries, err := os.ReadDir(victimDir)
	if err != nil {
		t.Fatalf("ReadDir(victim) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("victimDir entries = %d, want 0", len(entries))
	}
}

// TestHooksOrchestrator_InstallForceRefusesSymlink asserts the same guard
// applies in force mode. Force must not be treated as "overwrite symlink
// target" — it still refuses because the path itself is the symlink.
// Unix-only, same reason as TestHooksOrchestrator_InstallRefusesSymlink.
func TestHooksOrchestrator_InstallForceRefusesSymlink(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte(`{"sensitive":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	if err := os.Symlink(victim, settingsPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := orchestrator.Install(
		context.Background(),
		"claude",
		"/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		true,
	); err == nil {
		t.Fatalf("Install(force=true) error = nil, want symlink refusal")
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile(victim) error = %v", err)
	}
	if string(got) != `{"sensitive":true}` {
		t.Fatalf("victim content = %q, want untouched", got)
	}
}
