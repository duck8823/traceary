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

// integrationHookCopy describes one canonical scripts/hooks/*.sh source and the
// packaged destinations that must stay byte-identical to it. Host packages only
// receive the wrappers they still ship; Antigravity calls the Go runtime
// directly and is intentionally absent here.
type integrationHookCopy struct {
	source   string
	packages []string
}

// sharedCompatibilityPackages are the host packages that still ship the common
// compatibility shell wrappers (session/audit + common helpers).
var sharedCompatibilityPackages = []string{
	"integrations/claude-plugin/scripts",
	"plugins/traceary/scripts",
	"integrations/gemini-extension/scripts",
}

// integrationHookCopies is the single-source membership matrix for packaged
// hook shell wrappers. Edit scripts/hooks/, then run
// `go run ./cmd/repo-tooling integrations sync-hooks` (or hand-copy) so each
// destination stays byte-identical; integrations verify fails on drift.
var integrationHookCopies = []integrationHookCopy{
	{
		source:   "scripts/hooks/common.sh",
		packages: sharedCompatibilityPackages,
	},
	{
		source:   "scripts/hooks/traceary-session.sh",
		packages: sharedCompatibilityPackages,
	},
	{
		source:   "scripts/hooks/traceary-audit.sh",
		packages: sharedCompatibilityPackages,
	},
	{
		source: "scripts/hooks/traceary-prompt.sh",
		packages: []string{
			"integrations/claude-plugin/scripts",
			"integrations/gemini-extension/scripts",
		},
	},
	{
		source: "scripts/hooks/traceary-compact.sh",
		packages: []string{
			"integrations/claude-plugin/scripts",
		},
	},
	{
		source: "scripts/hooks/traceary-grok.sh",
		packages: []string{
			"integrations/grok-plugin/scripts",
		},
	},
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
	syncHooks := &cobra.Command{
		Use:   "sync-hooks",
		Short: "Copy canonical scripts/hooks/*.sh into packaged integration scripts directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			n, err := syncHookCopies(root)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "ok: synchronized %d packaged hook script copies from scripts/hooks/\n", n); err != nil {
				return xerrors.Errorf("failed to write sync result: %w", err)
			}
			return nil
		},
	}
	cmd.AddCommand(verify)
	cmd.AddCommand(syncHooks)
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
	if err := checkGrok(root, version); err != nil {
		return err
	}
	if err := checkKimi(root, version); err != nil {
		return err
	}
	if err := checkAntigravity(root); err != nil {
		return err
	}
	if err := checkRememberSkillContract(root); err != nil {
		return err
	}
	if err := checkSharedSkillParity(root); err != nil {
		return err
	}
	return checkDocs(root)
}

// rememberSkillPaths are the packaged copies of the shared
// traceary-memory-remember skill. They must stay byte-identical and must not
// reintroduce the accepted-status contradiction for explicit remember.
var rememberSkillPaths = []string{
	"integrations/claude-plugin/skills/traceary-memory-remember/SKILL.md",
	"plugins/traceary/skills/traceary-memory-remember/SKILL.md",
	"integrations/gemini-extension/skills/traceary-memory-remember/SKILL.md",
	"integrations/antigravity-plugin/skills/traceary-memory-remember/SKILL.md",
	"integrations/grok-plugin/skills/traceary-memory-remember/SKILL.md",
	"integrations/kimi-plugin/skills/traceary-memory-remember/SKILL.md",
}

// checkRememberSkillContract enforces the explicit-remember product contract:
// agent skills write review-inbox candidates (status=candidate via
// action=propose) and never auto-accept. Host package copies must stay
// synchronized so a single package cannot reintroduce status=accepted wording.
func checkRememberSkillContract(root string) error {
	var reference []byte
	for i, rel := range rememberSkillPaths {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path) // #nosec G304 -- fixed package path under repo root
		if err != nil {
			return xerrors.Errorf("missing remember skill: %s: %w", rel, err)
		}
		body := string(data)
		if !strings.Contains(body, "status=candidate") && !strings.Contains(body, "`status=candidate`") {
			return xerrors.Errorf("%s must state that explicit remember lands as status=candidate", rel)
		}
		if !strings.Contains(body, `action="propose"`) && !strings.Contains(body, `"action": "propose"`) {
			return xerrors.Errorf("%s must instruct manage_memory action=propose for the agent skill path", rel)
		}
		// Reject the old contradiction: procedure claimed action=remember →
		// accepted while the frontmatter promised candidate / never auto-accepted.
		if strings.Contains(body, "status=accepted") {
			return xerrors.Errorf("%s must not claim status=accepted for the agent remember skill path", rel)
		}
		// JSON example must not invoke action=remember (immediate accept).
		if strings.Contains(body, `"action": "remember"`) {
			return xerrors.Errorf("%s must not call manage_memory action=remember from the agent skill path", rel)
		}
		if i == 0 {
			reference = data
			continue
		}
		if string(data) != string(reference) {
			return xerrors.Errorf("remember skill copies diverged: %s does not match %s", rel, rememberSkillPaths[0])
		}
	}
	return nil
}

// sharedSkillPaths groups the packaged copies of each shared skill across
// hosts. Copies of a skill must stay byte-identical so a single host cannot
// drift; the first path in each group is the reference document.
//
// The Claude copy of traceary-session-history intentionally uses older,
// Claude-specific wording and is excluded from that skill's parity group;
// all other hosts share one text.
var sharedSkillPaths = map[string][]string{
	"traceary-memory-review": {
		"integrations/claude-plugin/skills/traceary-memory-review/SKILL.md",
		"plugins/traceary/skills/traceary-memory-review/SKILL.md",
		"integrations/gemini-extension/skills/traceary-memory-review/SKILL.md",
		"integrations/antigravity-plugin/skills/traceary-memory-review/SKILL.md",
		"integrations/grok-plugin/skills/traceary-memory-review/SKILL.md",
		"integrations/kimi-plugin/skills/traceary-memory-review/SKILL.md",
	},
	"traceary-session-history": {
		"integrations/grok-plugin/skills/traceary-session-history/SKILL.md",
		"plugins/traceary/skills/traceary-session-history/SKILL.md",
		"integrations/gemini-extension/skills/traceary-session-history/SKILL.md",
		"integrations/antigravity-plugin/skills/traceary-session-history/SKILL.md",
		"integrations/kimi-plugin/skills/traceary-session-history/SKILL.md",
	},
}

// checkSharedSkillParity enforces byte-identity of the shared skill copies
// across every host package.
func checkSharedSkillParity(root string) error {
	for skill, paths := range sharedSkillPaths {
		var reference []byte
		for i, rel := range paths {
			data, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- fixed package path under repo root
			if err != nil {
				return xerrors.Errorf("missing shared %s skill: %s: %w", skill, rel, err)
			}
			if i == 0 {
				reference = data
				continue
			}
			if string(data) != string(reference) {
				return xerrors.Errorf("shared %s skill copies diverged: %s does not match %s", skill, rel, paths[0])
			}
		}
	}
	return nil
}

func checkGrok(root, version string) error {
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := readJSON(root, "integrations/grok-plugin/plugin.json", &manifest); err != nil {
		return err
	}
	if manifest.Name != "traceary" {
		return xerrors.Errorf("unexpected Grok plugin name")
	}
	if manifest.Version != version {
		return xerrors.Errorf("grok plugin version must track v%s", version)
	}

	var mcp map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := readJSON(root, "integrations/grok-plugin/.mcp.json", &mcp); err != nil {
		return err
	}
	server, ok := mcp["traceary"]
	if len(mcp) != 1 || !ok || server.Command != "traceary" || !equalStrings(server.Args, []string{"mcp-server"}) {
		return xerrors.Errorf("grok plugin must expose traceary mcp-server")
	}

	hooksPath := "integrations/grok-plugin/hooks/hooks.json"
	hooks, _, err := readHookFile(root, hooksPath)
	if err != nil {
		return err
	}
	if err := checkNoDuplicateTracearyHookEntries(hooksPath, hooks); err != nil {
		return err
	}
	if err := checkGrokHooks(hooksPath, hooks); err != nil {
		return err
	}
	if err := requireExists(root, "integrations/grok-plugin/scripts/traceary-grok.sh", "missing Grok hook wrapper"); err != nil {
		return err
	}
	expectedSkills := []string{"traceary-memory-remember", "traceary-memory-review", "traceary-session-history"}
	entries, err := os.ReadDir(filepath.Join(root, "integrations/grok-plugin/skills"))
	if err != nil {
		return xerrors.Errorf("failed to read Grok skills: %w", err)
	}
	actualSkills := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			actualSkills = append(actualSkills, entry.Name())
		}
	}
	if !equalStrings(actualSkills, expectedSkills) {
		return xerrors.Errorf("grok plugin skills must be exactly %v, got %v", expectedSkills, actualSkills)
	}
	for _, skill := range expectedSkills {
		if err := requireExists(root, "integrations/grok-plugin/skills/"+skill+"/SKILL.md", "missing Grok "+skill+" skill"); err != nil {
			return err
		}
	}
	return nil
}

func checkKimi(root, version string) error {
	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Homepage    string `json:"homepage"`
		Interface   struct {
			DisplayName string `json:"displayName"`
		} `json:"interface"`
		Skills     []string `json:"skills"`
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
		Hooks []struct {
			Event   string `json:"event"`
			Matcher string `json:"matcher"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}
	if err := readJSON(root, "integrations/kimi-plugin/kimi.plugin.json", &manifest); err != nil {
		return err
	}
	if manifest.Name != "traceary" {
		return xerrors.Errorf("unexpected Kimi plugin name")
	}
	if manifest.Version != version {
		return xerrors.Errorf("kimi plugin version must track v%s", version)
	}
	if strings.TrimSpace(manifest.Description) == "" || strings.TrimSpace(manifest.Homepage) == "" || strings.TrimSpace(manifest.Interface.DisplayName) == "" {
		return xerrors.Errorf("kimi plugin must declare description, homepage, and interface.displayName")
	}
	if !equalStrings(manifest.Skills, []string{"./skills/"}) {
		return xerrors.Errorf("kimi plugin skills field must be exactly [\"./skills/\"], got %v", manifest.Skills)
	}
	server, ok := manifest.MCPServers["traceary"]
	if len(manifest.MCPServers) != 1 || !ok || server.Command != "traceary" || !equalStrings(server.Args, []string{"mcp-server"}) {
		return xerrors.Errorf("kimi plugin must expose the traceary mcp-server")
	}

	// The manifest hook rules must stay in lockstep with the verified
	// TOML plan (infrastructure/filesystem/kimi_hooks_handler.go).
	expectedHooks := []struct {
		event, matcher, action string
	}{
		{"SessionStart", "", "session-start"},
		{"SessionEnd", "", "session-end"},
		{"UserPromptSubmit", "", "user-prompt-submit"},
		{"PreToolUse", "Agent", "pre-tool-use"},
		{"PostToolUse", "", "post-tool-use"},
		{"PostToolUseFailure", "", "post-tool-use-failure"},
		{"Stop", "", "stop"},
		{"SubagentStop", "", "subagent-stop"},
		{"PreCompact", "", "pre-compact"},
		{"PostCompact", "", "post-compact"},
	}
	if len(manifest.Hooks) != len(expectedHooks) {
		return xerrors.Errorf("kimi plugin must declare exactly %d verified hook rules, got %d", len(expectedHooks), len(manifest.Hooks))
	}
	for i, want := range expectedHooks {
		hook := manifest.Hooks[i]
		expectedCommand := "traceary hook kimi " + want.action
		if hook.Event != want.event || hook.Matcher != want.matcher || hook.Command != expectedCommand || hook.Timeout != 5 {
			return xerrors.Errorf("kimi plugin hook rule %d (%s) drifted from the verified Kimi contract", i, want.event)
		}
	}

	expectedSkills := []string{"traceary-memory-remember", "traceary-memory-review", "traceary-session-history"}
	entries, err := os.ReadDir(filepath.Join(root, "integrations/kimi-plugin/skills"))
	if err != nil {
		return xerrors.Errorf("failed to read Kimi skills: %w", err)
	}
	actualSkills := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			actualSkills = append(actualSkills, entry.Name())
		}
	}
	if !equalStrings(actualSkills, expectedSkills) {
		return xerrors.Errorf("kimi plugin skills must be exactly %v, got %v", expectedSkills, actualSkills)
	}
	for _, skill := range expectedSkills {
		if err := requireExists(root, "integrations/kimi-plugin/skills/"+skill+"/SKILL.md", "missing Kimi "+skill+" skill"); err != nil {
			return err
		}
	}
	return nil
}

func checkGrokHooks(path string, hooks hookFile) error {
	required := []struct{ event, name, action string }{
		{"SessionStart", "traceary-session-start", "session-start"},
		{"UserPromptSubmit", "traceary-prompt", "user-prompt-submit"},
		{"PreToolUse", "traceary-tool-pre", "pre-tool-use"},
		{"PostToolUse", "traceary-audit", "post-tool-use"},
		{"Stop", "traceary-stop", "stop"},
		{"PreCompact", "traceary-compact-pre", "pre-compact"},
		{"PostCompact", "traceary-compact-post", "post-compact"},
	}
	if len(hooks.Hooks) != len(required) {
		return xerrors.Errorf("%s must expose exactly %d verified events", path, len(required))
	}
	for _, want := range required {
		entries := hooks.Hooks[want.event]
		if len(entries) != 1 || entries[0].Matcher != "" || len(entries[0].Hooks) != 1 {
			return xerrors.Errorf("%s %s must contain exactly one unfiltered command", path, want.event)
		}
		command := entries[0].Hooks[0]
		expectedCommand := `"${GROK_PLUGIN_ROOT}/scripts/traceary-grok.sh" "` + want.action + `"`
		if command.Name != want.name || command.Type != "command" || command.Command != expectedCommand || command.Timeout != 5 {
			return xerrors.Errorf("%s %s command drifted from the verified Grok contract", path, want.event)
		}
	}
	return nil
}

func readVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", xerrors.Errorf("missing file: VERSION")
	}
	return strings.TrimSpace(string(data)), nil
}

func checkHooksAreCopied(root string) error {
	for _, copy := range integrationHookCopies {
		sourceText, err := os.ReadFile(filepath.Join(root, copy.source)) // #nosec G304 -- fixed package path under repo root
		if err != nil {
			return xerrors.Errorf("missing canonical hook source: %s", copy.source)
		}
		name := filepath.Base(copy.source)
		for _, pkg := range copy.packages {
			target := filepath.Join(pkg, name)
			targetText, err := os.ReadFile(filepath.Join(root, target)) // #nosec G304 -- fixed package path under repo root
			if err != nil {
				return xerrors.Errorf("missing packaged hook script: %s", target)
			}
			if string(targetText) != string(sourceText) {
				return xerrors.Errorf("packaged hook script drifted from canonical source: %s (expected byte-identical to %s)", target, copy.source)
			}
		}
	}
	return nil
}

// syncHookCopies overwrites each packaged destination with the canonical
// scripts/hooks source. Returns the number of files written.
func syncHookCopies(root string) (int, error) {
	written := 0
	for _, copy := range integrationHookCopies {
		sourcePath := filepath.Join(root, copy.source)
		sourceText, err := os.ReadFile(sourcePath) // #nosec G304 -- fixed package path under repo root
		if err != nil {
			return written, xerrors.Errorf("missing canonical hook source: %s: %w", copy.source, err)
		}
		name := filepath.Base(copy.source)
		info, err := os.Stat(sourcePath)
		if err != nil {
			return written, xerrors.Errorf("stat canonical hook source: %s: %w", copy.source, err)
		}
		mode := info.Mode()
		if mode&0o111 == 0 {
			// Canonical sources should stay executable; force +x on copies.
			mode |= 0o111
		}
		for _, pkg := range copy.packages {
			targetDir := filepath.Join(root, pkg)
			if err := os.MkdirAll(targetDir, 0o755); err != nil {
				return written, xerrors.Errorf("create package scripts dir %s: %w", pkg, err)
			}
			target := filepath.Join(targetDir, name)
			if err := os.WriteFile(target, sourceText, mode.Perm()); err != nil {
				return written, xerrors.Errorf("write packaged hook script %s: %w", filepath.Join(pkg, name), err)
			}
			written++
		}
	}
	return written, nil
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
	if err := checkNoDuplicateTracearyHookEntries(hooksPath, hooks); err != nil {
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
	if err := checkNoDuplicateTracearyHookEntries("plugins/traceary/hooks.json", hooks); err != nil {
		return err
	}
	for _, event := range []string{"SessionStart", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "UserPromptSubmit", "Stop", "PostToolUse"} {
		if _, ok := hooks.Hooks[event]; !ok {
			return xerrors.Errorf("codex hooks must include %s", event)
		}
	}
	for _, fragment := range []struct{ sub, msg string }{
		{"'hook' 'session' 'codex'", "Codex packaged hooks must invoke traceary hook session directly"},
		{"'hook' 'prompt' 'codex'", "Codex packaged hooks must invoke traceary hook prompt directly"},
		{"'hook' 'audit' 'codex'", "Codex packaged hooks must invoke traceary hook audit directly"},
		{"'hook' 'compact' 'codex' 'pre-compact'", "Codex packaged hooks must invoke traceary pre-compact directly"},
		{"'hook' 'compact' 'codex' 'post-compact'", "Codex packaged hooks must invoke traceary post-compact directly"},
		{"'hook' 'subagent-start' 'codex'", "Codex packaged hooks must invoke traceary subagent-start directly"},
		{"'hook' 'subagent-stop' 'codex'", "Codex packaged hooks must invoke traceary subagent-stop directly"},
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

// checkCodexRemovedCommands asserts the entire `integration` command tree was
// deleted in v0.25.0 (#1266). Legacy install/uninstall paths must fail as
// unknown commands (no migration stubs remain).
func checkCodexRemovedCommands(root string) error {
	for _, args := range [][]string{
		{"integration"},
		{"integration", "codex", "install"},
		{"integration", "codex", "uninstall"},
	} {
		result, err := runTraceary(root, args...)
		if err != nil {
			return err
		}
		if result.exitCode == 0 {
			return xerrors.Errorf("%q must exit non-zero after v0.25.0 integration subtree removal", strings.Join(args, " "))
		}
		lower := strings.ToLower(result.output)
		if !strings.Contains(lower, "unknown") && !strings.Contains(lower, "invalid") &&
			!strings.Contains(result.output, "不明") && !strings.Contains(result.output, "未知") {
			return xerrors.Errorf("%q must fail as unknown command after v0.25.0 removal; got: %s", strings.Join(args, " "), result.output)
		}
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

	hooksPath := "integrations/gemini-extension/hooks/hooks.json"
	hooks, _, err := readHookFile(root, hooksPath)
	if err != nil {
		return err
	}
	if err := checkNoDuplicateTracearyHookEntries(hooksPath, hooks); err != nil {
		return err
	}
	for _, event := range []string{"SessionStart", "SessionEnd", "BeforeAgent", "AfterAgent", "AfterTool", "PreCompress"} {
		if _, ok := hooks.Hooks[event]; !ok {
			return xerrors.Errorf("gemini hooks must include %s", event)
		}
	}
	for _, required := range []struct {
		event           string
		matcher         string
		name            string
		commandFragment string
	}{
		{"SessionStart", "*", "traceary-session-start", "'hook' 'session' 'gemini' 'start'"},
		{"SessionEnd", "*", "traceary-session-end", "'hook' 'session' 'gemini' 'end'"},
		{"BeforeAgent", "*", "traceary-prompt", "'hook' 'prompt' 'gemini'"},
		{"AfterAgent", "*", "traceary-transcript", "'hook' 'transcript' 'gemini'"},
		{"AfterTool", "run_shell_command", "traceary-audit", "'hook' 'audit' 'gemini'"},
		{"PreCompress", "*", "traceary-pre-compress", "'hook' 'compact' 'gemini' 'pre-compact'"},
	} {
		if err := requirePackagedHookCommand(hooksPath, hooks, required.event, required.matcher, required.name, required.commandFragment); err != nil {
			return err
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

// checkAntigravity validates the packaged Antigravity plugin. Antigravity's
// hooks.json uses a top-level hook-group map (Traceary owns the "traceary"
// group) rather than the shared {"hooks": {...}} shape, so it is validated with
// a dedicated parser. The package also carries validator-native skills and
// mcp_config.json files so the host discovers the same memory/context surface
// as Traceary's other supported plugins.
func checkAntigravity(root string) error {
	var manifest struct {
		Schema      string `json:"$schema"`
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if err := readJSON(root, "integrations/antigravity-plugin/plugin.json", &manifest); err != nil {
		return err
	}
	if manifest.Name != "traceary" {
		return xerrors.Errorf("unexpected Antigravity plugin name")
	}
	if manifest.Schema != "https://antigravity.google/schemas/v1/plugin.json" {
		return xerrors.Errorf("antigravity plugin must declare the official plugin.json schema")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return xerrors.Errorf("antigravity plugin manifest must declare a version")
	}

	var mcpConfig struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := readJSON(root, "integrations/antigravity-plugin/mcp_config.json", &mcpConfig); err != nil {
		return err
	}
	server, ok := mcpConfig.MCPServers["traceary"]
	if !ok || server.Command != "traceary" || len(server.Args) != 1 || server.Args[0] != "mcp-server" {
		return xerrors.Errorf("antigravity plugin must expose the traceary mcp-server")
	}
	for _, skill := range []string{"traceary-session-history", "traceary-memory-review", "traceary-memory-remember"} {
		path := filepath.Join("integrations/antigravity-plugin/skills", skill, "SKILL.md")
		if err := requireExists(root, path, "missing Antigravity "+skill+" skill"); err != nil {
			return err
		}
	}

	hooksPath := "integrations/antigravity-plugin/hooks.json"
	data, err := os.ReadFile(filepath.Join(root, hooksPath))
	if err != nil {
		return xerrors.Errorf("missing file: %s", hooksPath)
	}
	var groups map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &groups); err != nil {
		return xerrors.Errorf("invalid json in %s: %w", hooksPath, err)
	}
	group, ok := groups["traceary"]
	if !ok {
		return xerrors.Errorf("antigravity hooks must define the traceary group")
	}
	for _, event := range []string{"PreInvocation", "PreToolUse", "PostToolUse", "Stop"} {
		if _, ok := group[event]; !ok {
			return xerrors.Errorf("antigravity traceary group must include %s", event)
		}
	}
	raw := string(data)
	for _, fragment := range []string{
		"'hook' 'antigravity' 'pre-invocation'",
		"'hook' 'antigravity' 'pre-tool-use'",
		"'hook' 'antigravity' 'post-tool-use'",
		"'hook' 'antigravity' 'stop'",
	} {
		if !strings.Contains(raw, fragment) {
			return xerrors.Errorf("antigravity packaged hooks must invoke %s", fragment)
		}
	}
	return nil
}

func checkDocs(root string) error {
	pairs := []string{
		"docs/integrations/README.md",
		"docs/integrations/claude-plugin.md",
		"docs/integrations/codex-plugin.md",
		"docs/integrations/gemini-extension.md",
		"docs/integrations/antigravity.md",
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
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func hookMatchers(entries []hookEntry) []string {
	matchers := make([]string, 0, len(entries))
	for _, entry := range entries {
		matchers = append(matchers, entry.Matcher)
	}
	return matchers
}

func requirePackagedHookCommand(rel string, hooks hookFile, event, matcher, name, commandFragment string) error {
	entries, ok := hooks.Hooks[event]
	if !ok {
		return xerrors.Errorf("%s must include %s", rel, event)
	}
	for _, entry := range entries {
		if entry.Matcher != matcher {
			continue
		}
		for _, command := range entry.Hooks {
			if command.Name != name {
				continue
			}
			if command.Type != "command" {
				return xerrors.Errorf("%s %s/%s must use command hook type, got %q", rel, event, name, command.Type)
			}
			if strings.Contains(command.Command, commandFragment) {
				return nil
			}
		}
	}
	return xerrors.Errorf(
		"%s must include %s matcher %q hook %q invoking %s (got matchers %v)",
		rel,
		event,
		matcher,
		name,
		commandFragment,
		hookMatchers(entries),
	)
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

func checkNoDuplicateTracearyHookEntries(rel string, hooks hookFile) error {
	for event, entries := range hooks.Hooks {
		counts := map[string]int{}
		for _, entry := range entries {
			matcher := entry.Matcher
			for _, command := range entry.Hooks {
				key := packagedTracearyHookKey(command)
				if key == "" {
					continue
				}
				countKey := event + "\x00" + matcher + "\x00" + key
				counts[countKey]++
				if counts[countKey] > 1 {
					displayMatcher := matcher
					if displayMatcher == "" {
						displayMatcher = "<default>"
					}
					return xerrors.Errorf(
						"%s registers duplicate Traceary hook entry for event=%s matcher=%q command=%s",
						rel,
						event,
						displayMatcher,
						key,
					)
				}
			}
		}
	}
	return nil
}

func packagedTracearyHookKey(command hookCommand) string {
	if command.Type != "" && command.Type != "command" {
		return ""
	}
	name := strings.TrimSpace(command.Name)
	commandText := strings.Join(strings.Fields(command.Command), " ")
	if name == "" && commandText == "" {
		return ""
	}
	if strings.HasPrefix(name, "traceary-") {
		return name
	}
	if strings.Contains(commandText, "traceary") && strings.Contains(commandText, "hook") {
		return commandText
	}
	return ""
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
