package filesystem_test

import (
	"os"
	"path/filepath"
	"testing"

	hookscripts "github.com/duck8823/traceary/scripts/hooks"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestHookScriptsInstaller_MaterializesCanonicalScripts(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(homeEnvKey(), "") // ensure env fallback is not taken
	installer := filesystem.NewHookScriptsInstallerWithHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})

	scriptsDir, err := installer.Ensure()
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if got, want := scriptsDir, filepath.Join(homeDir, ".config", "traceary", "hook-scripts"); got != want {
		t.Fatalf("scriptsDir = %q, want %q", got, want)
	}

	// Test files in infrastructure/filesystem are executed with a working
	// directory of infrastructure/filesystem. The repository root is two
	// levels up so the canonical scripts live at ../../scripts/hooks/.
	repoRoot := filepath.Join("..", "..")
	assets, err := hookscripts.Assets()
	if err != nil {
		t.Fatalf("hookscripts.Assets() error = %v", err)
	}
	for _, asset := range assets {
		outputPath := filepath.Join(scriptsDir, asset.Name())
		gotContent, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", outputPath, err)
		}
		if got, want := string(gotContent), asset.Content(); got != want {
			t.Fatalf("installed asset %q mismatch", asset.Name())
		}

		canonicalPath := filepath.Join(repoRoot, "scripts", "hooks", asset.Name())
		canonicalContent, err := os.ReadFile(canonicalPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", canonicalPath, err)
		}
		if got, want := asset.Content(), string(canonicalContent); got != want {
			t.Fatalf("asset %q drifted from canonical script", asset.Name())
		}
	}
}

func TestHookScriptsInstaller_ResolveDirHonorsEnvOverride(t *testing.T) {
	override := t.TempDir()
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", override)

	installer := filesystem.NewHookScriptsInstaller()
	resolved, err := installer.ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir() error = %v", err)
	}
	if got, want := resolved, override; got != want {
		t.Fatalf("ResolveDir() = %q, want %q", got, want)
	}
}

// TestHookScriptsInstaller_RefusesSymlinkTarget asserts that installing a
// hook script whose target path is an attacker-supplied symbolic link fails
// instead of writing through the link to another location.
func TestHookScriptsInstaller_RefusesSymlinkTarget(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", "")

	scriptsDir := filepath.Join(homeDir, ".config", "traceary", "hook-scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Stage a symlink at the install target path pointing at an unrelated
	// file outside the scripts dir. rejectSymlink must refuse this path
	// before os.ReadFile or os.WriteFile would follow the link.
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

	// The victim file must be untouched.
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile(victim) error = %v", err)
	}
	if string(got) != "do not touch" {
		t.Fatalf("victim content = %q, want untouched", got)
	}
}

func homeEnvKey() string {
	// We intentionally clear TRACEARY_HOOK_SCRIPTS_DIR so the installer
	// falls back to the explicit home-directory lookup configured above.
	return "TRACEARY_HOOK_SCRIPTS_DIR"
}
