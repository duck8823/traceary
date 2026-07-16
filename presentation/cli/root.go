package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/redaction"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation"
)

// RootCLI provides the Traceary root command.
type RootCLI struct {
	event                      usecase.EventUsecase
	session                    usecase.SessionUsecase
	memory                     usecase.MemoryUsecase
	memoryEdge                 usecase.MemoryEdgeUsecase
	bundle                     usecase.BundleUsecase
	context                    usecase.ContextUsecase
	replay                     usecase.ReplayUsecase
	storeManagement            usecase.StoreManagementUsecase
	mcpServerRunner            MCPServerRunner
	hooksOrchestrator          application.HooksOrchestrator
	hooksInspector             application.HooksInspector
	pluginCacheInspector       application.PluginCacheInspector
	pluginDetector             application.ClaudePluginDetector
	cockpitState               CockpitStateReader
	cockpitInteractive         cockpitInteractiveFunc
	cockpitRunner              cockpitRunnerFunc
	extraRedactPatterns        []string
	structuredRedactRules      []redaction.RuleConfig
	defaultAuditMaxInputBytes  int
	defaultAuditMaxOutputBytes int
	defaultReadFields          []string
	readPresets                map[string]presentation.ReadPreset
	defaultReadColor           string
	hookMemoryExtractLauncher  func(string) error
	hookGrokTranscriptLauncher func(string) error
	hookMemoryBeforeJobRemoval func()
	hookMemoryAfterFinalCheck  func()
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

// WithCockpitStateReader injects optional local cockpit state used for
// non-critical notification checkpoints such as memory/event last-seen time.
func WithCockpitStateReader(reader CockpitStateReader) RootCLIOption {
	return func(c *RootCLI) { c.cockpitState = reader }
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

// WithDefaultAuditPayloadLimits injects config-backed command-audit
// persistence limits. Command flags and TRACEARY_MAX_AUDIT_* environment
// variables still override these defaults at runtime.
func WithDefaultAuditPayloadLimits(maxInputBytes int, maxOutputBytes int) RootCLIOption {
	return func(c *RootCLI) {
		c.defaultAuditMaxInputBytes = maxInputBytes
		c.defaultAuditMaxOutputBytes = maxOutputBytes
	}
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

// WithHookMemoryExtractLauncher overrides the detached worker launcher used by
// hook-driven memory extraction. It is primarily intended for deterministic
// tests; production callers use the default process launcher.
func WithHookMemoryExtractLauncher(launcher func(string) error) RootCLIOption {
	return func(c *RootCLI) { c.hookMemoryExtractLauncher = launcher }
}

// WithHookGrokTranscriptLauncher overrides the detached Grok transcript
// worker launcher. Production callers use the default detached process;
// tests can capture the durable job path and run the worker deterministically.
func WithHookGrokTranscriptLauncher(launcher func(string) error) RootCLIOption {
	return func(c *RootCLI) { c.hookGrokTranscriptLauncher = launcher }
}

// WithHookMemoryBeforeJobRemoval installs a deterministic synchronization
// point for queue race tests. Production callers must leave it unset.
func WithHookMemoryBeforeJobRemoval(hook func()) RootCLIOption {
	return func(c *RootCLI) { c.hookMemoryBeforeJobRemoval = hook }
}

// WithHookMemoryAfterFinalCheck installs a deterministic synchronization
// point after the worker's final marker check but before unlock.
func WithHookMemoryAfterFinalCheck(hook func()) RootCLIOption {
	return func(c *RootCLI) { c.hookMemoryAfterFinalCheck = hook }
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
	rootCockpitOpts := cockpitCommandOptions{}
	rootCmd := &cobra.Command{
		Use:   "traceary",
		Short: Localize("Local-first CLI for AI agent work history", "AI エージェントの作業履歴をローカルに記録する CLI"),
		Long: Localize(
			"Traceary records and inspects local AI-agent work history. In an interactive terminal, running `traceary` with no subcommand opens the Tail-first operator cockpit. The bare cockpit also accepts the compatibility flags `--db-path` and `--reset-state`; use `traceary tui --help` for the discoverable explicit form. In scripts, pipes, or CI, use explicit read commands such as `traceary list`, `traceary sessions --snapshot [--json]`, or `traceary doctor --json`; `traceary top --snapshot [--json]` remains available as a compatibility alias.",
			"Traceary はローカルの AI agent 作業履歴を記録・確認します。対話 terminal では、subcommand なしの `traceary` が Tail-first operator cockpit を開きます。bare cockpit は互換 flag の `--db-path` と `--reset-state` も受け付けます。発見しやすい明示 form は `traceary tui --help` を参照してください。script、pipe、CI では `traceary list`、`traceary sessions --snapshot [--json]`、`traceary doctor --json` などの明示的な read command を使ってください。`traceary top --snapshot [--json]` は互換 alias として引き続き使えます。",
		),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				// Cobra's legacy root validation misses `traceary -- extra`
				// after flag parsing, so the bare entrypoint keeps its own
				// no-positional-arguments guard.
				return noArgsLocalized()(cmd, args)
			}
			return c.runRootDefault(cmd, rootCockpitOpts)
		},
	}
	bindCockpitFlags(rootCmd, &rootCockpitOpts)
	// Top-level daily-use commands (kept flat for ergonomics).
	rootCmd.AddCommand(c.newLogCommand())
	rootCmd.AddCommand(c.newAuditCommand())
	rootCmd.AddCommand(c.newSearchCommand())
	rootCmd.AddCommand(c.newTailCommand())
	rootCmd.AddCommand(c.newSessionsCommand())
	rootCmd.AddCommand(c.newTopCommand())
	rootCmd.AddCommand(c.newCockpitCommand())
	rootCmd.AddCommand(c.newContextCommand())
	rootCmd.AddCommand(c.newListCommand())
	rootCmd.AddCommand(c.newShowCommand())
	rootCmd.AddCommand(c.newHookCommand())
	rootCmd.AddCommand(c.newSessionCommand())
	rootCmd.AddCommand(c.newMemoryCommand())
	rootCmd.AddCommand(c.newTimelineCommand())
	rootCmd.AddCommand(c.newCompletionCommand(rootCmd))
	rootCmd.AddCommand(c.newHooksCommand())
	rootCmd.AddCommand(c.newDoctorCommand())
	rootCmd.AddCommand(c.newMCPServerCommand())
	rootCmd.AddCommand(c.newReplayCommand())
	rootCmd.AddCommand(c.newBundleCommand())

	// v0.9.0 grouped namespaces — store administration and
	// session-bootstrap helpers moved behind parent commands.
	rootCmd.AddCommand(c.newStoreCommand())

	// Make every pure group command (e.g. `memory`, `store`, `session`, and
	// their sub-namespaces) reject unknown subcommands with a usage error
	// instead of silently printing help and exiting 0. The root keeps its own
	// RunE (the cockpit + stray-arg guard) and is left untouched.
	applyStrictGroups(rootCmd)

	return rootCmd
}

// applyStrictGroups walks the command tree and turns every pure group command
// — one that has subcommands but no Run/RunE of its own — strict: a bare
// invocation still prints help (exit 0), but an unrecognized subcommand fails
// with a usage error (non-zero exit). Commands with their own RunE (leaf
// commands and the root) are not modified.
func applyStrictGroups(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		applyStrictGroups(sub)
	}
	if !cmd.HasSubCommands() || cmd.RunE != nil || cmd.Run != nil {
		return
	}
	// `--help` / `-h` is intentionally still honored even alongside an
	// unrecognized positional (e.g. `traceary memory bogus --help` prints memory
	// help): Cobra processes the help flag before RunE, so an explicit help
	// request short-circuits before this RunE runs. Always honoring `--help` is
	// the standard CLI convention; the strict error below covers the
	// typo-in-automation case (`traceary memory bogus` with no help flag).
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return xerrors.Errorf("%s", Localizef(
			"unknown subcommand %q for %q; run %q for available commands",
			"%q は %q の不明なサブコマンドです。利用可能なコマンドは %q を参照してください",
			args[0], cmd.CommandPath(), cmd.CommandPath()+" --help",
		))
	}
}

// runRootDefault opens the Tail-first cockpit only when the bare root command
// has an interactive stdin/stdout pair; plain non-TTY callers receive only
// deterministic help output to keep scripts and pipes stable.
func (c *RootCLI) runRootDefault(cmd *cobra.Command, opts cockpitCommandOptions) error {
	stdin, stdout, ok := cockpitIO(cmd.InOrStdin(), cmd.OutOrStdout())
	if ok && c.isCockpitInteractive(stdin, stdout) {
		return c.cockpitRunnerFunc()(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), opts)
	}

	if cmd.Flags().Changed("db-path") || cmd.Flags().Changed("reset-state") {
		guidance := Localize(
			"Cockpit flags require an interactive TTY; run `traceary` or `traceary tui` from a terminal to use --db-path or --reset-state.",
			"cockpit flag には対話 TTY が必要です。--db-path や --reset-state を使うには terminal から `traceary` または `traceary tui` を実行してください。",
		)
		return cockpitExitError{message: guidance, exitCode: cockpitExitCodeNotInteractive}
	}
	helpErr := cmd.Help()
	if helpErr != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to render help", "help の表示に失敗しました"), helpErr)
	}
	return nil
}

type cockpitRunnerFunc func(context.Context, io.Reader, io.Writer, cockpitCommandOptions) error
