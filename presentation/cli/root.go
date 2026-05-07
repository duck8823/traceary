package cli

import (
	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/redaction"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation"
)

// RootCLI provides the Traceary root command.
type RootCLI struct {
	event                 usecase.EventUsecase
	session               usecase.SessionUsecase
	memory                usecase.MemoryUsecase
	memoryEdge            usecase.MemoryEdgeUsecase
	bundle                usecase.BundleUsecase
	context               usecase.ContextUsecase
	replay                usecase.ReplayUsecase
	codexIntegration      usecase.CodexIntegrationUsecase
	storeManagement       usecase.StoreManagementUsecase
	mcpServerRunner       MCPServerRunner
	hooksOrchestrator     application.HooksOrchestrator
	hooksInspector        application.HooksInspector
	pluginCacheInspector  application.PluginCacheInspector
	pluginDetector        application.ClaudePluginDetector
	extraRedactPatterns   []string
	structuredRedactRules []redaction.RuleConfig
	defaultReadFields     []string
	readPresets           map[string]presentation.ReadPreset
	defaultReadColor      string
	// databasePathSetter is invoked by each subcommand's RunE after it
	// resolves --db-path / TRACEARY_DB_PATH, so the shared Database
	// instance opens the user-specified path instead of the composition-
	// root default. May be nil in tests that inject stubs directly.
	databasePathSetter func(string)
}

// RootCLIOption configures a RootCLI during construction. Options are
// applied in order, so later options override earlier ones.
type RootCLIOption func(*RootCLI)

// WithEvent injects the EventUsecase used by event-producing commands.
func WithEvent(event usecase.EventUsecase) RootCLIOption {
	return func(c *RootCLI) { c.event = event }
}

// WithSession injects the SessionUsecase used by session-related commands.
func WithSession(session usecase.SessionUsecase) RootCLIOption {
	return func(c *RootCLI) { c.session = session }
}

// WithMemory injects the MemoryUsecase used by durable-memory commands.
func WithMemory(memory usecase.MemoryUsecase) RootCLIOption {
	return func(c *RootCLI) { c.memory = memory }
}

// WithBundle injects the BundleUsecase used by `traceary bundle`
// export / import subcommands.
func WithBundle(b usecase.BundleUsecase) RootCLIOption {
	return func(c *RootCLI) { c.bundle = b }
}

// WithMemoryEdge injects the MemoryEdgeUsecase used by
// `traceary memory graph` subcommands.
func WithMemoryEdge(edge usecase.MemoryEdgeUsecase) RootCLIOption {
	return func(c *RootCLI) { c.memoryEdge = edge }
}

// WithContext injects the ContextUsecase used by structured handoff commands.
func WithContext(contextUsecase usecase.ContextUsecase) RootCLIOption {
	return func(c *RootCLI) { c.context = contextUsecase }
}

// WithReplay injects the ReplayUsecase used by the replay HTML export
// command. WithReplay is required: the CLI returns a configuration
// error at runtime if `traceary replay` is invoked without it.
func WithReplay(replay usecase.ReplayUsecase) RootCLIOption {
	return func(c *RootCLI) { c.replay = replay }
}

// WithCodexIntegration injects the CodexIntegrationUsecase used by the
// integration codex install/uninstall commands.
func WithCodexIntegration(codexIntegration usecase.CodexIntegrationUsecase) RootCLIOption {
	return func(c *RootCLI) { c.codexIntegration = codexIntegration }
}

// WithStoreManagement injects the StoreManagementUsecase used by init,
// backup, gc, and doctor commands.
func WithStoreManagement(storeManagement usecase.StoreManagementUsecase) RootCLIOption {
	return func(c *RootCLI) { c.storeManagement = storeManagement }
}

// WithMCPServerRunner injects the MCPServerRunner used by the mcp-server
// command.
func WithMCPServerRunner(runner MCPServerRunner) RootCLIOption {
	return func(c *RootCLI) { c.mcpServerRunner = runner }
}

// WithHooksOrchestrator injects the HooksOrchestrator used by hooks and
// doctor commands. The orchestrator is required before the corresponding
// commands can run.
func WithHooksOrchestrator(orchestrator application.HooksOrchestrator) RootCLIOption {
	return func(c *RootCLI) { c.hooksOrchestrator = orchestrator }
}

// WithHooksInspector injects the HooksInspector used by the doctor command
// to inspect client hook configurations.
func WithHooksInspector(inspector application.HooksInspector) RootCLIOption {
	return func(c *RootCLI) { c.hooksInspector = inspector }
}

// WithPluginCacheInspector injects the PluginCacheInspector used by the
// doctor command to detect cached-vs-marketplace drift on hosts that
// have a per-plugin version cache (Claude Code).
func WithPluginCacheInspector(inspector application.PluginCacheInspector) RootCLIOption {
	return func(c *RootCLI) { c.pluginCacheInspector = inspector }
}

// WithClaudePluginDetector injects the ClaudePluginDetector used by
// doctor / hooks install to detect whether the Traceary Claude Code
// plugin is active in the user's global settings.
func WithClaudePluginDetector(detector application.ClaudePluginDetector) RootCLIOption {
	return func(c *RootCLI) { c.pluginDetector = detector }
}

// WithExtraRedactPatterns injects additional redaction regex patterns used
// by the audit command.
func WithExtraRedactPatterns(patterns []string) RootCLIOption {
	return func(c *RootCLI) { c.extraRedactPatterns = patterns }
}

// WithStructuredRedactRules injects named/configurable redaction rules.
func WithStructuredRedactRules(rules []redaction.RuleConfig) RootCLIOption {
	return func(c *RootCLI) { c.structuredRedactRules = rules }
}

// WithDefaultReadFields injects the default column order used by tail / list
// / search text output when the user does not pass --fields. Callers
// typically source this from the read.fields entry in the user config. Nil
// or empty lists fall back to the built-in default column order.
func WithDefaultReadFields(columns []string) RootCLIOption {
	return func(c *RootCLI) { c.defaultReadFields = columns }
}

// WithReadPresets injects the user-defined read presets loaded from
// ~/.config/traceary/config.json. The builtin preset catalog is always
// available; these entries merge on top and override built-in names on
// collision (with a stderr warning from the resolver).
func WithReadPresets(presets map[string]presentation.ReadPreset) RootCLIOption {
	return func(c *RootCLI) { c.readPresets = presets }
}

// WithDefaultReadColor injects the default --color mode (auto / always /
// never) applied to read commands when the operator does not pass --color.
// Callers source this from read.color in the user config; empty string
// falls back to the built-in auto behavior.
func WithDefaultReadColor(value string) RootCLIOption {
	return func(c *RootCLI) { c.defaultReadColor = value }
}

// WithDatabasePathSetter injects a callback invoked by every subcommand
// after it resolves the --db-path flag / TRACEARY_DB_PATH environment
// variable. The callback is typically a closure around the shared
// sqlite.Database's SetPath method, so datasources built from it open
// the user-specified path on the next operation.
func WithDatabasePathSetter(setter func(string)) RootCLIOption {
	return func(c *RootCLI) { c.databasePathSetter = setter }
}

// applyDatabasePath forwards the resolved DB path to the injected
// setter. It is a no-op when no setter is configured, which matches the
// test setup where usecases are stubbed and the real Database is not
// wired in.
func (c *RootCLI) applyDatabasePath(resolved string) {
	if c.databasePathSetter == nil {
		return
	}
	c.databasePathSetter(resolved)
}

// NewRootCLI creates a new RootCLI with the given options applied.
func NewRootCLI(opts ...RootCLIOption) *RootCLI {
	c := &RootCLI{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Command returns the Traceary root command.
func (c *RootCLI) Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "traceary",
		Short:         Localize("Local-first CLI for AI agent work history", "AI エージェントの作業履歴をローカルに記録する CLI"),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	// Top-level daily-use commands (kept flat for ergonomics).
	rootCmd.AddCommand(c.newLogCommand())
	rootCmd.AddCommand(c.newAuditCommand())
	rootCmd.AddCommand(c.newSearchCommand())
	rootCmd.AddCommand(c.newTailCommand())
	rootCmd.AddCommand(c.newTopCommand())
	rootCmd.AddCommand(c.newContextCommand())
	rootCmd.AddCommand(c.newListCommand())
	rootCmd.AddCommand(c.newShowCommand())
	rootCmd.AddCommand(c.newHookCommand())
	rootCmd.AddCommand(c.newSessionCommand())
	rootCmd.AddCommand(c.newMemoryCommand())
	rootCmd.AddCommand(c.newTimelineCommand())
	rootCmd.AddCommand(c.newCompletionCommand(rootCmd))
	rootCmd.AddCommand(c.newHooksCommand())
	rootCmd.AddCommand(c.newIntegrationCommand())
	rootCmd.AddCommand(c.newDoctorCommand())
	rootCmd.AddCommand(c.newMCPServerCommand())
	rootCmd.AddCommand(c.newReplayCommand())
	rootCmd.AddCommand(c.newBundleCommand())

	// v0.9.0 grouped namespaces — store administration and
	// session-bootstrap helpers moved behind parent commands.
	rootCmd.AddCommand(c.newStoreCommand())

	// v0.14.0 retired the v0.9-era top-level aliases (init / backup /
	// gc / handoff / compact-summary). Hidden stubs stay so the old
	// names produce a usage error pointing at the canonical command.
	c.addRemovedAliases(rootCmd)
	return rootCmd
}
