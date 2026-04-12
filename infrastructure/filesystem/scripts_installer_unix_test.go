//go:build unix

package filesystem_test

import (
	"os"
	"path/filepath"
	"testing"

	hookscripts "github.com/duck8823/traceary/scripts/hooks"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// TestHookScriptsInstaller_RefusesAncestorSymlink asserts that Ensure()
// refuses to write through an attacker-controlled symlink on an ancestor
// directory of the scripts dir. Without the ancestor check, MkdirAll
// would accept the existing link and WriteFile would follow it into the
// victim directory. Unix-only because safe_fs_other.go (non-unix)
// delegates to os.* and does not harden against symlink traversal.
func TestHookScriptsInstaller_RefusesAncestorSymlink(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", "")

	victimDir := t.TempDir()
	configDir := filepath.Join(homeDir, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	scriptsLink := filepath.Join(configDir, "hook-scripts")
	if err := os.Symlink(victimDir, scriptsLink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	installer := filesystem.NewHookScriptsInstallerWithHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})

	if _, err := installer.Ensure(); err == nil {
		t.Fatalf("Ensure() error = nil, want ancestor symlink refusal")
	}

	entries, err := os.ReadDir(victimDir)
	if err != nil {
		t.Fatalf("ReadDir(victim) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("victimDir entries = %d, want 0", len(entries))
	}
}

// TestHookScriptsInstaller_RefusesSymlinkTarget asserts that installing a
// hook script whose target path is an attacker-supplied symbolic link
// fails instead of writing through the link to another location. Unix-only
// because safe_fs_other.go (non-unix) delegates to os.* and does not
// harden against symlink traversal.
func TestHookScriptsInstaller_RefusesSymlinkTarget(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", "")

	scriptsDir := filepath.Join(homeDir, ".config", "traceary", "hook-scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}

	assets, err := hookscripts.Assets()
	if err != nil {
		t.Fatalf("hookscripts.Assets() error = %v", err)
	}
	if len(assets) == 0 {
		t.Fatalf("no hook script assets available")
	}
	linkPath := filepath.Join(scriptsDir, assets[0].Name())
	if err := os.Symlink(victim, linkPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	installer := filesystem.NewHookScriptsInstallerWithHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})

	if _, err := installer.Ensure(); err == nil {
		t.Fatalf("Ensure() error = nil, want symlink refusal")
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile(victim) error = %v", err)
	}
	if string(got) != "do not touch" {
		t.Fatalf("victim content = %q, want untouched", got)
	}
}
