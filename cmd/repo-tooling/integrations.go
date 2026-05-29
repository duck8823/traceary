package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

// canonical hook sources that must be copied verbatim into every packaged
// integration's scripts directory.
var integrationHookSources = []string{
	"scripts/hooks/common.sh",
	"scripts/hooks/traceary-session.sh",
	"scripts/hooks/traceary-audit.sh",
}

// packaged integration scripts directories that must hold copies of the
// canonical hook sources.
var integrationHookPackages = []string{
	"integrations/claude-plugin/scripts",
	"plugins/traceary/scripts",
	"integrations/gemini-extension/scripts",
}

func newIntegrationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "Integration package checks",
	}
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Verify integration manifests and packaged assets are consistent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			if err := verifyIntegrations(root, true); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "ok: integration manifests and packaged assets are consistent"); err != nil {
				return xerrors.Errorf("failed to write verify result: %w", err)
			}
			return nil
		},
	}
	cmd.AddCommand(verify)
	return cmd
}

// verifyIntegrations reproduces scripts/verify_integrations.py. When
// runCLISmoke is false the Codex removed-command CLI smoke (which compiles and
// runs the main binary) is skipped so file-only checks stay fast in tests; CI
// invokes the command with the smoke enabled.
func verifyIntegrations(root string, runCLISmoke bool) error {
	version, err := readVersion(root)
	if err != nil {
		return err
	}
	if err := checkHooksAreCopied(root); err != nil {
		return err
	}
	if err := checkClaude(root, version); err != nil {
		return err
	}
	if err := checkCodex(root, version, runCLISmoke); err != nil {
		return err
	}
	if err := checkGemini(root, version); err != nil {
		return err
	}
	return checkDocs(root)
}

func readVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", xerrors.Errorf("missing file: VERSION")
	}
	return strings.TrimSpace(string(data)), nil
}

func checkHooksAreCopied(root string) error {
	for _, source := range integrationHookSources {
		sourceText, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			return xerrors.Errorf("missing canonical hook source: %s", source)
		}
		name := filepath.Base(source)
		for _, pkg := range integrationHookPackages {
			target := filepath.Join(pkg, name)
			targetText, err := os.ReadFile(filepath.Join(root, target))
			if err != nil {
				return xerrors.Errorf("missing packaged hook script: %s", target)
			}
			if string(targetText) != string(sourceText) {
				return xerrors.Errorf("packaged hook script drifted from canonical source: %s", target)
			}
		}
	}
	return nil
}

func checkClaude(root, version string) error {
	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := readJSON(root, ".claude-plugin/marketplace.json", &marketplace); err != nil {
		return err
	}
	if marketplace.Name != "traceary-plugins" {
		return xerrors.Errorf("unexpected Claude marketplace name")
	}
	if len(marketplace.Plugins) != 1 {
		return xerrors.Errorf("claude marketplace must expose exactly one plugin")
	}
	if marketplace.Plugins[0].Name != "traceary" {
		return xerrors.Errorf("unexpected Claude plugin name")
	}
	if marketplace.Plugins[0].Source != "./integrations/claude-plugin" {
		return xerrors.Errorf("unexpected Claude plugin source path")
	}

	var pluginManifest struct {
		Version string `json:"version"`
	}
	if err := readJSON(root, "integrations/claude-plugin/.claude-plugin/plugin.json", &pluginManifest); err != nil {
		return err
	}
	if pluginManifest.Version != version {
		return xerrors.Errorf("claude plugin version must track v%s", version)
	}

	var mcp struct {
		Traceary struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"traceary"`
	}
	if err := readJSON(root, "integrations/claude-plugin/.mcp.json", &mcp); err != nil {
		return err
	}
	if mcp.Traceary.Command != "traceary" {
		return xerrors.Errorf("claude MCP must call traceary")
	}
	if !equalStrings(mcp.Traceary.Args, []string{"mcp-server"}) {
		return xerrors.Errorf("claude MCP args must be traceary mcp-server")
	}

	hooksPath := "integrations/claude-plugin/hooks/hooks.json"
	hooks, hooksRaw, err := readHookFile(root, hooksPath)
	if err != nil {
		return err
	}
	for _, event := range []string{"SessionStart", "SessionEnd", "PostToolUse", "PostCompact"} {
		if _, ok := hooks.Hooks[event]; !ok {
			return xerrors.Errorf("claude hooks must include %s", event)
		}
	}
	if !strings.Contains(hooksRaw, "'hook' 'session' 'claude'") {
		return xerrors.Errorf("claude packaged hooks must invoke traceary hook session directly")
	}
	if !strings.Contains(hooksRaw, "'hook' 'audit' 'claude'") {
		return xerrors.Errorf("claude packaged hooks must invoke traceary hook audit directly")
	}
	// v0.8-6: both PostToolUse and PostToolUseFailure must register three
	// matchers (Bash / mcp__.* / built-in tool list) so Traceary captures the
	// real working surface, not just shell + MCP traffic.
	for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
		matchers := hookMatchers(hooks.Hooks[event])
		if len(matchers) < 2 || matchers[0] != "Bash" || matchers[1] != "mcp__.*" {
			return xerrors.Errorf("claude %s must register Bash and mcp__.* as the first two matchers, got %v", event, matchers)
		}
		if len(matchers) < 3 || !strings.Contains(matchers[2], "Read") || !strings.Contains(matchers[2], "Edit") || !strings.Contains(matchers[2], "Write") {
			return xerrors.Errorf("claude %s must include the built-in tool matcher (Read|Edit|Write|...), got %v", event, matchers)
		}
	}
	if err := requireExists(root, "integrations/claude-plugin/scripts/traceary-compact.sh", "missing Claude compact hook script"); err != nil {
		return err
	}
	for _, skill := range []string{"traceary-help", "traceary-session-history", "traceary-memory-review", "traceary-memory-remember"} {
		if err := requireExists(root, "integrations/claude-plugin/skills/"+skill+"/SKILL.md", "missing Claude "+skill+" skill"); err != nil {
			return err
		}
	}
	if err := requireAbsent(root, "integrations/claude-plugin/skills/traceary-memory-capture",
		"Claude traceary-memory-capture skill stub must be removed (replaced by traceary-memory-review and traceary-memory-remember)"); err != nil {
		return err
	}
	return nil
}

func checkCodex(root, version string, runCLISmoke bool) error {
	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := readJSON(root, ".agents/plugins/marketplace.json", &marketplace); err != nil {
		return err
	}
	if marketplace.Name != "traceary-marketplace" {
		return xerrors.Errorf("unexpected Codex marketplace name")
	}
	if len(marketplace.Plugins) != 1 {
		return xerrors.Errorf("codex marketplace must expose exactly one plugin")
	}
	if marketplace.Plugins[0].Name != "traceary" {
		return xerrors.Errorf("unexpected Codex plugin name")
	}
	if marketplace.Plugins[0].Source.Path != "./plugins/traceary" {
		return xerrors.Errorf("unexpected Codex plugin source path")
	}

	var pluginManifest struct {
		Version string `json:"version"`
		Hooks   string `json:"hooks"`
	}
	if err := readJSON(root, "plugins/traceary/.codex-plugin/plugin.json", &pluginManifest); err != nil {
		return err
	}
	if pluginManifest.Version != version {
		return xerrors.Errorf("codex plugin version must track v%s", version)
	}
	if pluginManifest.Hooks != "./hooks.json" {
		return xerrors.Errorf("codex plugin manifest must declare hooks: ./hooks.json so the official /plugins flow picks up Traceary hooks")
	}

	var mcp struct {
		McpServers struct {
			Traceary struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"traceary"`
		} `json:"mcpServers"`
	}
	if err := readJSON(root, "plugins/traceary/.mcp.json", &mcp); err != nil {
		return err
	}
	if mcp.McpServers.Traceary.Command != "traceary" {
		return xerrors.Errorf("codex MCP must call traceary")
	}
	if !equalStrings(mcp.McpServers.Traceary.Args, []string{"mcp-server"}) {
		return xerrors.Errorf("codex MCP args must be traceary mcp-server")
	}

	hooks, hooksRaw, err := readHookFile(root, "plugins/traceary/hooks.json")
	if err != nil {
		return err
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "PostToolUse"} {
		if _, ok := hooks.Hooks[event]; !ok {
			return xerrors.Errorf("codex hooks must include %s", event)
		}
	}
	for _, fragment := range []struct{ sub, msg string }{
		{"'hook' 'session' 'codex'", "Codex packaged hooks must invoke traceary hook session directly"},
		{"'hook' 'prompt' 'codex'", "Codex packaged hooks must invoke traceary hook prompt directly"},
		{"'hook' 'audit' 'codex'", "Codex packaged hooks must invoke traceary hook audit directly"},
	} {
		if !strings.Contains(hooksRaw, fragment.sub) {
			return xerrors.Errorf("%s", fragment.msg)
		}
	}
	for _, rel := range []struct{ path, msg string }{
		{"plugins/traceary/commands/help.md", "missing Codex help command"},
		{"plugins/traceary/commands/doctor.md", "missing Codex doctor command"},
		{"plugins/traceary/skills/traceary-session-history/SKILL.md", "missing Codex traceary-session-history skill"},
		{"plugins/traceary/skills/traceary-memory-review/SKILL.md", "missing Codex traceary-memory-review skill"},
		{"plugins/traceary/skills/traceary-memory-remember/SKILL.md", "missing Codex traceary-memory-remember skill"},
	} {
		if err := requireExists(root, rel.path, rel.msg); err != nil {
			return err
		}
	}
	if err := requireAbsent(root, "plugins/traceary/skills/traceary-memory-capture",
		"Codex traceary-memory-capture skill stub must be removed (replaced by traceary-memory-review and traceary-memory-remember)"); err != nil {
		return err
	}

	if !runCLISmoke {
		return nil
	}
	return checkCodexRemovedCommands(root)
}

// checkCodexRemovedCommands smokes the hidden install/uninstall stubs that
// v0.14.0 (#920) and v0.15.0 (#957) retired: both must exit non-zero and point
// at Codex's official /plugins flow.
func checkCodexRemovedCommands(root string) error {
	install, err := runTraceary(root, "integration", "codex", "install")
	if err != nil {
		return err
	}
	if install.exitCode == 0 {
		return xerrors.Errorf("codex install command must exit non-zero after v0.14.0 removal")
	}
	if !strings.Contains(install.output, "v0.14.0") {
		return xerrors.Errorf("codex install removal message must name v0.14.0")
	}
	if !strings.Contains(install.output, "/plugins") {
		return xerrors.Errorf("codex install removal message must point at the Codex /plugins flow")
	}

	uninstall, err := runTraceary(root, "integration", "codex", "uninstall")
	if err != nil {
		return err
	}
	if uninstall.exitCode == 0 {
		return xerrors.Errorf("codex uninstall command must exit non-zero after v0.15.0 removal")
	}
	if !strings.Contains(uninstall.output, "v0.15.0") {
		return xerrors.Errorf("codex uninstall removal message must name v0.15.0")
	}
	if !strings.Contains(uninstall.output, "/plugins") {
		return xerrors.Errorf("codex uninstall removal message must point at the Codex /plugins flow")
	}
	if !strings.Contains(uninstall.output, "codex-plugin.md") {
		return xerrors.Errorf("codex uninstall removal message must reference the manual cleanup guide")
	}
	return nil
}

func checkGemini(root, version string) error {
	var manifest struct {
		Name            string `json:"name"`
		Version         string `json:"version"`
		ContextFileName string `json:"contextFileName"`
		McpServers      struct {
			Traceary struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"traceary"`
		} `json:"mcpServers"`
	}
	if err := readJSON(root, "integrations/gemini-extension/gemini-extension.json", &manifest); err != nil {
		return err
	}
	if manifest.Name != "traceary" {
		return xerrors.Errorf("unexpected Gemini extension name")
	}
	if manifest.Version != version {
		return xerrors.Errorf("gemini extension version must track v%s", version)
	}
	if manifest.McpServers.Traceary.Command != "traceary" {
		return xerrors.Errorf("gemini MCP must call traceary")
	}
	if !equalStrings(manifest.McpServers.Traceary.Args, []string{"mcp-server"}) {
		return xerrors.Errorf("gemini MCP args must be traceary mcp-server")
	}
	if manifest.ContextFileName != "GEMINI.md" {
		return xerrors.Errorf("gemini extension must expose GEMINI.md as context file")
	}

	hooks, hooksRaw, err := readHookFile(root, "integrations/gemini-extension/hooks/hooks.json")
	if err != nil {
		return err
	}
	for _, event := range []string{"SessionStart", "SessionEnd", "AfterTool", "BeforeAgent", "PreCompress"} {
		if _, ok := hooks.Hooks[event]; !ok {
			return xerrors.Errorf("gemini hooks must include %s", event)
		}
	}
	for _, fragment := range []struct{ sub, msg string }{
		{"'hook' 'session' 'gemini'", "Gemini packaged hooks must invoke traceary hook session directly"},
		{"'hook' 'audit' 'gemini'", "Gemini packaged hooks must invoke traceary hook audit directly"},
		{"'hook' 'prompt' 'gemini'", "Gemini packaged hooks must invoke traceary hook prompt directly"},
		{"'hook' 'compact' 'gemini' 'pre-compact'", "Gemini packaged hooks must invoke traceary hook compact pre-compact directly"},
	} {
		if !strings.Contains(hooksRaw, fragment.sub) {
			return xerrors.Errorf("%s", fragment.msg)
		}
	}
	for _, rel := range []struct{ path, msg string }{
		{"integrations/gemini-extension/commands/traceary-help.toml", "missing Gemini help command"},
		{"integrations/gemini-extension/commands/traceary-doctor.toml", "missing Gemini doctor command"},
		{"integrations/gemini-extension/skills/traceary-session-history/SKILL.md", "missing Gemini traceary-session-history skill"},
		{"integrations/gemini-extension/skills/traceary-memory-review/SKILL.md", "missing Gemini traceary-memory-review skill"},
		{"integrations/gemini-extension/skills/traceary-memory-remember/SKILL.md", "missing Gemini traceary-memory-remember skill"},
		{"integrations/gemini-extension/GEMINI.md", "missing Gemini context file"},
	} {
		if err := requireExists(root, rel.path, rel.msg); err != nil {
			return err
		}
	}
	return requireAbsent(root, "integrations/gemini-extension/skills/traceary-memory-capture",
		"Gemini traceary-memory-capture skill stub must be removed (replaced by traceary-memory-review and traceary-memory-remember)")
}

func checkDocs(root string) error {
	pairs := []string{
		"docs/integrations/README.md",
		"docs/integrations/claude-plugin.md",
		"docs/integrations/codex-plugin.md",
		"docs/integrations/gemini-extension.md",
	}
	for _, english := range pairs {
		japanese := strings.TrimSuffix(english, ".md") + ".ja.md"
		if err := requireExists(root, japanese, fmt.Sprintf("missing Japanese docs pair for %s", english)); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ---

type hookFile struct {
	Hooks map[string][]hookEntry `json:"hooks"`
}

type hookEntry struct {
	Matcher string `json:"matcher"`
}

func hookMatchers(entries []hookEntry) []string {
	matchers := make([]string, 0, len(entries))
	for _, entry := range entries {
		matchers = append(matchers, entry.Matcher)
	}
	return matchers
}

func readHookFile(root, rel string) (hookFile, string, error) {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return hookFile{}, "", xerrors.Errorf("missing file: %s", rel)
	}
	var hf hookFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return hookFile{}, "", xerrors.Errorf("invalid json in %s: %w", rel, err)
	}
	return hf, string(data), nil
}

func readJSON(root, rel string, dest any) error {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return xerrors.Errorf("missing file: %s", rel)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return xerrors.Errorf("invalid json in %s: %w", rel, err)
	}
	return nil
}

func requireExists(root, rel, message string) error {
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		return xerrors.Errorf("%s", message)
	}
	return nil
}

func requireAbsent(root, rel, message string) error {
	if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
		return xerrors.Errorf("%s", message)
	}
	return nil
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type cliResult struct {
	exitCode int
	output   string
}

// runTraceary runs `go run . <args...>` from the repo root with the CLI pinned
// to English so removed-command smoke assertions are deterministic regardless
// of the operator's ui.language / OS locale (mirrors the presentation/cli
// TestMain locale pin and CI's English default).
func runTraceary(root string, args ...string) (cliResult, error) {
	cmdArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TRACEARY_LANG=en")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return cliResult{exitCode: exitErr.ExitCode(), output: string(output)}, nil
		}
		return cliResult{}, xerrors.Errorf("failed to run traceary %v: %w", args, err)
	}
	return cliResult{exitCode: 0, output: string(output)}, nil
}

// findRepoRoot walks up from the working directory to the module root (the
// directory holding go.mod). repo-tooling is run from the repository, so this
// keeps it robust whether invoked from the root or a subdirectory.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", xerrors.Errorf("failed to resolve working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", xerrors.Errorf("could not locate repository root (go.mod) from working directory")
		}
		dir = parent
	}
}
