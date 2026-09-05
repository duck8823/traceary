package cli

import (
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/redaction"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/presentation"
)

// RootCLI provides the Traceary root command.
type RootCLI struct {
	event                       usecase.EventUsecase
	eventMetadata               usecase.EventMetadataUsecase
	twoTierSearch               queryservice.TwoTierSearch
	reportCommand               usecase.ReportCommandUsecase
	report                      usecase.ReportUsecase
	codexCaptureDiagnostic      usecase.CodexCaptureDiagnosticUsecase
	session                     usecase.SessionUsecase
	sessionRefinement           usecase.SessionRefinementUsecase
	consolidationPressure       usecase.ConsolidationPressureUsecase
	consolidationRequest        usecase.ConsolidationRequestUsecase
	consolidationConversion     queryservice.ConsolidationConversionQueryService
	sessionEventOrder           model.SessionEventOrderRepository
	sessionWakeSummary          queryservice.SessionWakeSummaryQueryService
	memory                      usecase.MemoryUsecase
	bundle                      usecase.BundleUsecase
	codexUsage                  usecase.CodexUsageCaptureUsecase
	codexHeadlessUsage          application.CodexHeadlessUsageStreamFactory
	claudeUsage                 usecase.ClaudeUsageCaptureUsecase
	claudeHeadlessUsage         application.ClaudeHeadlessUsageStreamFactory
	geminiUsage                 usecase.GeminiUsageCaptureUsecase
	geminiHeadlessUsage         application.GeminiHeadlessUsageStreamFactory
	antigravityUsage            usecase.AntigravityUsageCaptureUsecase
	grokUsage                   usecase.GrokUsageCaptureUsecase
	grokHeadlessUsage           application.GrokHeadlessUsageStreamFactory
	kimiUsage                   usecase.KimiUsageCaptureUsecase
	context                     usecase.ContextUsecase
	storeManagement             usecase.StoreManagementUsecase
	capacityInspector           application.CapacityInspector
	pageMetadataInspector       application.PageMetadataInspector
	endedSessionInspector       application.EndedSessionInspector
	operatorCostInspector       application.OperatorCostInspector
	attestationAnchorInspector  application.AttestationAnchorInspector
	storeCompactionFactory      func(string) application.StoreCompactionUsecase
	preparedStoreUpgradeFactory func(string) application.PreparedStoreUpgradeUsecase
	fileRetention               usecase.FileRetentionUsecase
	fileRetentionCapacity       usecase.FileRetentionCapacityInspector
	workspaceIdentity           usecase.WorkspaceIdentityUsecase
	hooksOrchestrator           application.HooksOrchestrator
	hooksInspector              application.HooksInspector
	pluginCacheInspector        application.PluginCacheInspector
	pluginDetector              application.ClaudePluginDetector
	extraRedactPatterns         []string
	structuredRedactRules       []redaction.RuleConfig
	defaultAuditMaxInputBytes   int
	defaultAuditMaxOutputBytes  int
	defaultReadFields           []string
	readPresets                 map[string]presentation.ReadPreset
	defaultReadColor            string
	hookMemoryExtractLauncher   func(string) error
	hookGrokTranscriptLauncher  func(string) error
	hookMemoryBeforeJobRemoval  func()
	hookMemoryAfterFinalCheck   func()
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

// WithEventMetadata injects body-free event reads used by metadata projections.
func WithEventMetadata(eventMetadata usecase.EventMetadataUsecase) RootCLIOption {
	return func(c *RootCLI) { c.eventMetadata = eventMetadata }
}

// WithTwoTierSearch injects the refinement-plus-fallback search read path.
func WithTwoTierSearch(search queryservice.TwoTierSearch) RootCLIOption {
	return func(c *RootCLI) { c.twoTierSearch = search }
}

// WithReportCommand injects structured command-audit aggregation for report.
func WithReportCommand(reportCommand usecase.ReportCommandUsecase) RootCLIOption {
	return func(c *RootCLI) { c.reportCommand = reportCommand }
}

// WithReport injects the shared report generator.
func WithReport(report usecase.ReportUsecase) RootCLIOption {
	return func(c *RootCLI) { c.report = report }
}

// WithCodexCaptureDiagnostic injects the body-free Codex doctor projection.
func WithCodexCaptureDiagnostic(diagnostic usecase.CodexCaptureDiagnosticUsecase) RootCLIOption {
	return func(c *RootCLI) { c.codexCaptureDiagnostic = diagnostic }
}

// WithSession injects the SessionUsecase used by session-related commands.
func WithSession(session usecase.SessionUsecase) RootCLIOption {
	return func(c *RootCLI) { c.session = session }
}

// WithSessionRefinement injects the L2 session refinement write port.
func WithSessionRefinement(sessionRefinement usecase.SessionRefinementUsecase) RootCLIOption {
	return func(c *RootCLI) { c.sessionRefinement = sessionRefinement }
}

// WithConsolidationPressure injects the read-only stop-hook pressure check.
func WithConsolidationPressure(consolidationPressure usecase.ConsolidationPressureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.consolidationPressure = consolidationPressure }
}

// WithConsolidationRequest injects the stop-hook consolidation request ledger.
func WithConsolidationRequest(u usecase.ConsolidationRequestUsecase) RootCLIOption {
	return func(c *RootCLI) { c.consolidationRequest = u }
}

// WithConsolidationConversion injects the bounded doctor conversion read.
func WithConsolidationConversion(q queryservice.ConsolidationConversionQueryService) RootCLIOption {
	return func(c *RootCLI) { c.consolidationConversion = q }
}

// WithSessionEventOrder injects canonical event-order lookups for the Stop path.
func WithSessionEventOrder(order model.SessionEventOrderRepository) RootCLIOption {
	return func(c *RootCLI) { c.sessionEventOrder = order }
}

// WithSessionWakeSummary injects the read-only wake-injection summary query.
func WithSessionWakeSummary(sessionWakeSummary queryservice.SessionWakeSummaryQueryService) RootCLIOption {
	return func(c *RootCLI) { c.sessionWakeSummary = sessionWakeSummary }
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

// WithCodexUsage injects the body-free Codex usage capture adapter.
func WithCodexUsage(usage usecase.CodexUsageCaptureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.codexUsage = usage }
}

// WithCodexHeadlessUsage injects the body-free `codex exec --json` stream adapter.
func WithCodexHeadlessUsage(factory application.CodexHeadlessUsageStreamFactory) RootCLIOption {
	return func(c *RootCLI) { c.codexHeadlessUsage = factory }
}

// WithClaudeUsage injects the body-free Claude usage capture adapter.
func WithClaudeUsage(usage usecase.ClaudeUsageCaptureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.claudeUsage = usage }
}

// WithClaudeHeadlessUsage injects the body-free Claude one-shot stream adapter.
func WithClaudeHeadlessUsage(factory application.ClaudeHeadlessUsageStreamFactory) RootCLIOption {
	return func(c *RootCLI) { c.claudeHeadlessUsage = factory }
}

// WithGeminiUsage injects the body-free Gemini usage capture adapter.
func WithGeminiUsage(usage usecase.GeminiUsageCaptureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.geminiUsage = usage }
}

// WithGeminiHeadlessUsage injects the body-free Gemini stream adapter.
func WithGeminiHeadlessUsage(factory application.GeminiHeadlessUsageStreamFactory) RootCLIOption {
	return func(c *RootCLI) { c.geminiHeadlessUsage = factory }
}

// WithAntigravityUsage injects cumulative status and unavailable Stop capture.
func WithAntigravityUsage(usage usecase.AntigravityUsageCaptureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.antigravityUsage = usage }
}

// WithGrokUsage injects headless usage and unavailable Stop capture.
func WithGrokUsage(usage usecase.GrokUsageCaptureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.grokUsage = usage }
}

// WithGrokHeadlessUsage injects the body-free Grok streaming-json adapter.
func WithGrokHeadlessUsage(factory application.GrokHeadlessUsageStreamFactory) RootCLIOption {
	return func(c *RootCLI) { c.grokHeadlessUsage = factory }
}

// WithKimiUsage injects partial wire usage and unavailable lifecycle capture.
func WithKimiUsage(usage usecase.KimiUsageCaptureUsecase) RootCLIOption {
	return func(c *RootCLI) { c.kimiUsage = usage }
}

// WithContext injects the ContextUsecase used by structured handoff commands.
func WithContext(contextUsecase usecase.ContextUsecase) RootCLIOption {
	return func(c *RootCLI) { c.context = contextUsecase }
}

// WithStoreManagement injects the StoreManagementUsecase used by init,
// backup, gc, and doctor commands.
func WithStoreManagement(storeManagement usecase.StoreManagementUsecase) RootCLIOption {
	return func(c *RootCLI) { c.storeManagement = storeManagement }
}

// WithCapacityInspector injects metadata-only SQLite capacity diagnostics.
func WithCapacityInspector(inspector application.CapacityInspector) RootCLIOption {
	return func(c *RootCLI) { c.capacityInspector = inspector }
}

// WithPageMetadataInspector injects the O(1) large-store pragma reader.
func WithPageMetadataInspector(inspector application.PageMetadataInspector) RootCLIOption {
	return func(c *RootCLI) { c.pageMetadataInspector = inspector }
}

// WithEndedSessionInspector injects the bounded path-based ended-session
// reader large-store doctor uses to resolve hook cancellation markers.
func WithEndedSessionInspector(inspector application.EndedSessionInspector) RootCLIOption {
	return func(c *RootCLI) { c.endedSessionInspector = inspector }
}

// WithOperatorCostInspector injects this-store measured cost for doctor.
func WithOperatorCostInspector(inspector application.OperatorCostInspector) RootCLIOption {
	return func(c *RootCLI) { c.operatorCostInspector = inspector }
}

// WithAttestationAnchorInspector injects the store-side attestation sidecar check.
func WithAttestationAnchorInspector(inspector application.AttestationAnchorInspector) RootCLIOption {
	return func(c *RootCLI) { c.attestationAnchorInspector = inspector }
}

// WithStoreCompactionFactory injects a path-bound, dedicated composition.
func WithStoreCompactionFactory(factory func(string) application.StoreCompactionUsecase) RootCLIOption {
	return func(c *RootCLI) { c.storeCompactionFactory = factory }
}

// WithPreparedStoreUpgradeFactory injects the offline-migration upgrade driver.
func WithPreparedStoreUpgradeFactory(factory func(string) application.PreparedStoreUpgradeUsecase) RootCLIOption {
	return func(c *RootCLI) { c.preparedStoreUpgradeFactory = factory }
}

// WithFileRetention injects reviewed archive/backup capacity management.
func WithFileRetention(retention usecase.FileRetentionUsecase) RootCLIOption {
	return func(c *RootCLI) {
		c.fileRetention = retention
		c.fileRetentionCapacity = retention
	}
}

// WithFileRetentionCapacityInspector injects only the read-only doctor/status capability.
func WithFileRetentionCapacityInspector(inspector usecase.FileRetentionCapacityInspector) RootCLIOption {
	return func(c *RootCLI) { c.fileRetentionCapacity = inspector }
}

// WithWorkspaceIdentity injects body-free identity reporting and reviewed aliases.
func WithWorkspaceIdentity(workspaceIdentity usecase.WorkspaceIdentityUsecase) RootCLIOption {
	return func(c *RootCLI) { c.workspaceIdentity = workspaceIdentity }
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
	var dbPath string
	rootCmd := &cobra.Command{
		Use:   "traceary",
		Short: Localize("Local-first CLI for AI agent work history", "AI エージェントの作業履歴をローカルに記録する CLI"),
		Long: Localize(
			"Traceary records and inspects local AI-agent work history. Running `traceary` with no subcommand prints this help (TTY and non-TTY). Use explicit read commands such as `traceary list`, `traceary search`, or `traceary doctor --json`.",
			"Traceary はローカルの AI agent 作業履歴を記録・確認します。subcommand なしの `traceary` は TTY / 非 TTY ともこの help を表示します。`traceary list`、`traceary search`、`traceary doctor --json` などの明示的な read command を使ってください。",
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
			return c.runRootDefault(cmd)
		},
	}
	// Keep root --db-path so `traceary --db-path … <subcommand>` stays valid;
	// bare root ignores the value and only prints help.
	rootCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	// Top-level daily-use commands (kept flat for ergonomics).
	rootCmd.AddCommand(c.newLogCommand())
	rootCmd.AddCommand(c.newAuditCommand())
	rootCmd.AddCommand(c.newSearchCommand())
	rootCmd.AddCommand(c.newContextCommand())
	rootCmd.AddCommand(c.newListCommand())
	rootCmd.AddCommand(c.newShowCommand())
	rootCmd.AddCommand(c.newHookCommand())
	rootCmd.AddCommand(c.newSessionCommand())
	rootCmd.AddCommand(c.newMemoryCommand())

	rootCmd.AddCommand(c.newCompletionCommand(rootCmd))
	rootCmd.AddCommand(c.newHooksCommand())
	rootCmd.AddCommand(c.newDoctorCommand())
	rootCmd.AddCommand(c.newReportCommand())
	rootCmd.AddCommand(c.newBundleCommand())

	// v0.9.0 grouped namespaces — store administration and
	// session-bootstrap helpers moved behind parent commands.
	rootCmd.AddCommand(c.newStoreCommand())

	// Make every pure group command (e.g. `memory`, `store`, `session`, and
	// their sub-namespaces) reject unknown subcommands with a usage error
	// instead of silently printing help and exiting 0. The root keeps its own
	// RunE (help + stray-arg guard) and is left untouched.
	applyStrictGroups(rootCmd)
	applyInventoryDeprecations(rootCmd)
	c.attachCompactReclaimWarning(rootCmd)

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

// runRootDefault always prints help for a bare `traceary` invocation.
// The former TTY cockpit default was removed in v0.35.0 (#1764).
func (c *RootCLI) runRootDefault(cmd *cobra.Command) error {
	helpErr := cmd.Help()
	if helpErr != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to render help", "help の表示に失敗しました"), helpErr)
	}
	return nil
}
