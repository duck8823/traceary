package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// stubClaudePluginDetector implements application.ClaudePluginDetector
// with canned results so inspect-level tests do not touch the real home.
type stubClaudePluginDetector struct {
	scan    application.ClaudeLocalLeftoverScan
	scanErr error
}

func (s stubClaudePluginDetector) DetectClaudeTracearyPluginIn(string) application.ClaudePluginDetection {
	return application.ClaudePluginDetection{}
}

func (s stubClaudePluginDetector) ListClaudeLocalPluginLeftovers(string) (application.ClaudeLocalLeftoverScan, error) {
	return s.scan, s.scanErr
}

func TestInspectClaudePluginLocalLeftoversWarnsWithCountAndSample(t *testing.T) {
	missing := []string{filepath.Join(t.TempDir(), "gone-a"), filepath.Join(t.TempDir(), "gone-b")}
	cli := &RootCLI{pluginDetector: stubClaudePluginDetector{
		scan: application.ClaudeLocalLeftoverScan{
			InventoryPath: "/home/test/.claude/plugins/installed_plugins.json",
			LeftoverPaths: missing,
		},
	}}

	check := cli.inspectClaudePluginLocalLeftovers()
	if check == nil {
		t.Fatal("check = nil; want WARN check")
	}
	if check.Status != doctorStatusWarn {
		t.Fatalf("status = %q, want warn", check.Status)
	}
	if check.Name != "claude-plugin-local-leftovers" {
		t.Fatalf("name = %q", check.Name)
	}
	if !strings.Contains(check.Message, "2") {
		t.Fatalf("message = %q, want leftover count 2", check.Message)
	}
	for _, path := range missing {
		if !strings.Contains(check.Message, path) {
			t.Fatalf("message = %q, want sample path %q", check.Message, path)
		}
	}
	if !strings.Contains(check.Hint, "installed_plugins.json") {
		t.Fatalf("hint = %q, want it to name installed_plugins.json", check.Hint)
	}
	if check.AutoFixAvailable {
		t.Fatal("AutoFixAvailable = true, want false")
	}
	if check.FixCommand != "" {
		t.Fatalf("FixCommand = %q, want empty", check.FixCommand)
	}
	if check.FixFunc != nil || check.StructuredFixFunc != nil {
		t.Fatal("leftover check must not carry a fixer (doctor --fix must not write Claude config)")
	}
}

func TestInspectClaudePluginLocalLeftoversPassesWhenNoneLeftover(t *testing.T) {
	cli := &RootCLI{pluginDetector: stubClaudePluginDetector{
		scan: application.ClaudeLocalLeftoverScan{
			InventoryPath: "/home/test/.claude/plugins/installed_plugins.json",
		},
	}}

	check := cli.inspectClaudePluginLocalLeftovers()
	if check == nil {
		t.Fatal("check = nil; want PASS check when inventory exists with no leftovers")
	}
	if check.Status != doctorStatusPass {
		t.Fatalf("status = %q, want pass", check.Status)
	}
}

func TestInspectClaudePluginLocalLeftoversOmitsWhenInventoryMissing(t *testing.T) {
	cli := &RootCLI{pluginDetector: stubClaudePluginDetector{
		scanErr: fmt.Errorf("read: %w", fs.ErrNotExist),
	}}

	if check := cli.inspectClaudePluginLocalLeftovers(); check != nil {
		t.Fatalf("check = %+v, want nil when installed_plugins.json is absent", check)
	}
}

func TestInspectClaudePluginLocalLeftoversWarnsNotFailsOnUnreadableInventory(t *testing.T) {
	cli := &RootCLI{pluginDetector: stubClaudePluginDetector{
		scan:    application.ClaudeLocalLeftoverScan{InventoryPath: "/home/test/.claude/plugins/installed_plugins.json"},
		scanErr: errors.New("invalid character 'x'"),
	}}

	check := cli.inspectClaudePluginLocalLeftovers()
	if check == nil {
		t.Fatal("check = nil; want WARN check for unreadable inventory")
	}
	if check.Status != doctorStatusWarn {
		t.Fatalf("status = %q, want warn (not fail)", check.Status)
	}
	if check.FixFunc != nil || check.StructuredFixFunc != nil {
		t.Fatal("unreadable-inventory WARN must not carry a fixer")
	}
}

func TestInspectClaudePluginLocalLeftoversBoundsSample(t *testing.T) {
	base := t.TempDir()
	leftovers := make([]string, 0, 7)
	for i := 0; i < 7; i++ {
		leftovers = append(leftovers, filepath.Join(base, fmt.Sprintf("gone-%d", i)))
	}
	cli := &RootCLI{pluginDetector: stubClaudePluginDetector{
		scan: application.ClaudeLocalLeftoverScan{
			InventoryPath: "/home/test/.claude/plugins/installed_plugins.json",
			LeftoverPaths: leftovers,
		},
	}}

	check := cli.inspectClaudePluginLocalLeftovers()
	if check == nil || check.Status != doctorStatusWarn {
		t.Fatalf("check = %+v, want WARN", check)
	}
	if !strings.Contains(check.Message, "7") {
		t.Fatalf("message = %q, want full count 7", check.Message)
	}
	if !strings.Contains(check.Message, "and 2 more") {
		t.Fatalf("message = %q, want remainder summary", check.Message)
	}
	printed := 0
	for _, path := range leftovers {
		if strings.Contains(check.Message, path) {
			printed++
		}
	}
	if printed > claudeLocalLeftoverSampleLimit {
		t.Fatalf("message prints %d paths, want at most %d: %q", printed, claudeLocalLeftoverSampleLimit, check.Message)
	}
}

func TestInspectClaudePluginLocalLeftoversIsStoreIndependent(t *testing.T) {
	t.Parallel()
	if !doctorCheckIsStoreIndependent("claude-plugin-local-leftovers") {
		t.Fatal("claude-plugin-local-leftovers must be labeled store-independent")
	}

	report := &doctorReport{
		Checks: []doctorCheck{{
			Name:    "claude-plugin-local-leftovers",
			Status:  doctorStatusWarn,
			Message: "claude plugin inventory lists 1 enabled local Traceary install(s)",
		}},
	}
	annotateDoctorScopeAndDBPathHints(report)
	if !strings.Contains(report.Checks[0].Message, doctorStoreIndependentLabel) {
		t.Fatalf("message = %q, want store-independent label", report.Checks[0].Message)
	}
}

// writeClaudePluginInventoryFixture writes an installed_plugins.json with
// the given rows body into the test home.
func writeClaudePluginInventoryFixture(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// TestInspectClaudePluginLocalLeftoversRealInventory drives the shipped
// inspect function through the real filesystem adapter against a temp-HOME
// fixture: an existing local project must not be flagged, and the user
// cache checks must stay PASS while leftovers WARN.
func TestInspectClaudePluginLocalLeftoversRealInventory(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	existingProject := t.TempDir()
	missingProject := filepath.Join(home, "deleted-worktree")
	writeClaudePluginInventoryFixture(t, home, fmt.Sprintf(`{
  "version": 2,
  "plugins": {
    "traceary@traceary-plugins": [
      { "scope": "user", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.45.0", "version": "0.45.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.45.0", "version": "0.45.0" },
      { "scope": "local", "projectPath": "%s", "installPath": "%s/.claude/plugins/cache/traceary-plugins/traceary/0.41.0", "version": "0.41.0" }
    ]
  }
}`, home, existingProject, home, missingProject, home))

	// User-scope plugin detection + a current 0.45.0 cache so the existing
	// cache/version checks stay PASS.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"enabledPlugins": {"traceary@traceary-plugins": true}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cacheDir := filepath.Join(home, ".claude", "plugins", "cache", "traceary-plugins", "traceary", "0.45.0")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	manifestPath := filepath.Join(home, ".claude", "plugins", "marketplaces", "traceary-plugins", "integrations", "claude-plugin", ".claude-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"name":"traceary","version":"0.45.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cli := &RootCLI{
		pluginDetector:       filesystem.NewClaudePluginDetectorAdapter(),
		pluginCacheInspector: filesystem.NewPluginCacheInspector(),
	}

	leftovers := cli.inspectClaudePluginLocalLeftovers()
	if leftovers == nil || leftovers.Status != doctorStatusWarn {
		t.Fatalf("leftovers check = %+v, want WARN", leftovers)
	}
	if !strings.Contains(leftovers.Message, "1 enabled local") {
		t.Fatalf("message = %q, want count 1 (existing project excluded)", leftovers.Message)
	}
	if !strings.Contains(leftovers.Message, missingProject) {
		t.Fatalf("message = %q, want missing path %q", leftovers.Message, missingProject)
	}
	if strings.Contains(leftovers.Message, existingProject) {
		t.Fatalf("message = %q, must not flag the existing project %q", leftovers.Message, existingProject)
	}

	cache := cli.inspectClaudePluginCacheStatus()
	if cache == nil || cache.Status != doctorStatusPass {
		t.Fatalf("claude-plugin-cache = %+v, want PASS (leftovers must not flip it)", cache)
	}

	versionPass := false
	for _, check := range cli.inspectPluginVersionChecks("0.45.0") {
		if check.Name == "claude-plugin-version" && check.Status == doctorStatusPass {
			versionPass = true
		}
	}
	if !versionPass {
		t.Fatal("claude-plugin-version must stay PASS on a current user cache")
	}
}

// TestFilesystemHostDoctorIncludesLocalLeftovers proves the bounded
// (large-store) doctor path also reports leftover local installs and that
// the emitted check carries no fixer, so `doctor --fix` cannot rewrite
// Claude's inventory.
func TestFilesystemHostDoctorIncludesLocalLeftovers(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	missingProject := filepath.Join(home, "deleted-worktree")
	writeClaudePluginInventoryFixture(t, home, fmt.Sprintf(`{
  "version": 2,
  "plugins": {
    "traceary@traceary-plugins": [
      { "scope": "local", "projectPath": "%s", "installPath": "x", "version": "0.41.0" }
    ]
  }
}`, missingProject))
	inventoryBytes, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	cli := NewRootCLI(
		WithHooksOrchestrator(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
			"claude": filesystem.NewClaudeHooksHandler(),
		})),
		WithHooksInspector(filesystem.NewHooksInspector()),
		WithPluginCacheInspector(filesystem.NewPluginCacheInspector()),
		WithClaudePluginDetector(filesystem.NewClaudePluginDetectorAdapter()),
	)

	report := &doctorReport{}
	cli.appendFilesystemHostDoctorChecks(context.Background(), report, []string{"claude"}, t.TempDir(), "")

	var leftover *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "claude-plugin-local-leftovers" {
			leftover = &report.Checks[i]
		}
	}
	if leftover == nil {
		t.Fatalf("filesystem-host doctor checks = %+v, want claude-plugin-local-leftovers", report.Checks)
	}
	if leftover.Status != doctorStatusWarn {
		t.Fatalf("status = %q, want warn", leftover.Status)
	}
	if leftover.FixFunc != nil || leftover.StructuredFixFunc != nil || leftover.FixCommand != "" || leftover.AutoFixAvailable {
		t.Fatalf("leftover check must not be fixable: %+v", leftover)
	}

	after, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != string(inventoryBytes) {
		t.Fatal("doctor must not modify Claude's installed_plugins.json")
	}
}
