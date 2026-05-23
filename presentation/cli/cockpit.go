package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/presentation/cli/tui"
)

const cockpitExitCodeNotInteractive = 2

type cockpitExitError struct {
	message  string
	exitCode int
}

func (e cockpitExitError) Error() string { return e.message }
func (e cockpitExitError) ExitCode() int { return e.exitCode }

type cockpitCommandOptions struct {
	dbPath string
}

func (c *RootCLI) newCockpitCommand() *cobra.Command {
	opts := cockpitCommandOptions{}
	cmd := &cobra.Command{
		Use:     "tui",
		Aliases: []string{"dashboard"},
		Short:   Localize("Open the Traceary operator cockpit TUI", "Traceary operator cockpit TUI を開く"),
		Long: Localize(
			"Open the Traceary operator cockpit TUI. The cockpit is the explicit interactive entrypoint that will gather top, tail, doctor, handoff, and memory review workflows behind one TTY-only shell. Bare `traceary` behavior is unchanged.",
			"Traceary operator cockpit TUI を開きます。cockpit は top / tail / doctor / handoff / memory review を 1 つの TTY 専用 shell にまとめる明示的な対話 entrypoint です。bare `traceary` の挙動は変更しません。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runCockpit(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) runCockpit(ctx context.Context, output io.Writer, opts cockpitCommandOptions) error {
	stdin, stdout := cockpitIO(output)
	if !tui.Interactive(stdin, stdout) {
		return newCockpitNonInteractiveError(output)
	}
	home, err := c.loadCockpitHome(ctx, opts)
	if err != nil {
		return err
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	if err := tui.Run(model, tui.RunOptions{Input: stdin, Output: stdout, AltScreen: true}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run cockpit TUI", "cockpit TUI の実行に失敗しました"), err)
	}
	return nil
}

type cockpitHomeSnapshot struct {
	LoadedAt time.Time
	DBPath   string

	DoctorPassCount int
	DoctorWarnCount int
	DoctorFailCount int
	DoctorError     string

	HookWarnCount int
	HookFailCount int

	StaleActiveSessionCount int
	AcceptedMemoryCount     int
	CandidateMemoryCount    int
	StaleMemoryCount        int
	RecentFailureCount      int
	RecentCommandCount      int
	LargePayloadCount       int
}

func (c *RootCLI) loadCockpitHome(ctx context.Context, opts cockpitCommandOptions) (cockpitHomeSnapshot, error) {
	loadedAt := topNowFunc()
	home := cockpitHomeSnapshot{LoadedAt: loadedAt}

	report, reportErr := c.loadCockpitDoctorReport(ctx, opts)
	if reportErr != nil {
		home.DoctorError = reportErr.Error()
	} else if report != nil {
		home.DBPath = report.DBPath
		home.DoctorPassCount = report.Summary.Pass
		home.DoctorWarnCount = report.Summary.Warn
		home.DoctorFailCount = report.Summary.Fail
		home.HookWarnCount, home.HookFailCount = countCockpitHookIssues(report)
	}
	if home.DBPath == "" {
		resolvedDBPath, err := resolveDBPath(opts.dbPath)
		if err != nil {
			home.DoctorError = err.Error()
		} else {
			c.applyDatabasePath(resolvedDBPath)
			home.DBPath = resolvedDBPath
			if c.storeManagement != nil {
				if err := c.storeManagement.Initialize(ctx); err != nil {
					home.DoctorError = err.Error()
				}
			}
		}
	}

	snap, err := c.newTopDataLoader().loadSnapshot(ctx, topDataCriteria{
		SessionLimit:       defaultTopLimit,
		FailureLimit:       topPaneFailureLimit,
		RecentCommandLimit: topPaneRecentCommandLimit,
		CandidateLimit:     topPaneCandidateLimit,
		StaleMemoryLimit:   topPaneStaleMemoryLimit,
		StaleAfter:         defaultActiveSessionStaleAfter,
		Now:                loadedAt,
	})
	if err != nil {
		return cockpitHomeSnapshot{}, err
	}
	home.StaleActiveSessionCount = snap.Reliability.StaleActiveSessionCount
	home.AcceptedMemoryCount = snap.Reliability.AcceptedMemoryCount
	home.CandidateMemoryCount = snap.Reliability.CandidateMemoryCount
	home.StaleMemoryCount = snap.StaleMemories.Count()
	home.RecentFailureCount = len(snap.Failures)
	home.RecentCommandCount = len(snap.RecentCommands)
	home.LargePayloadCount = snap.Reliability.LargePayloads.Count

	return home, nil
}

func (c *RootCLI) loadCockpitDoctorReport(ctx context.Context, opts cockpitCommandOptions) (*doctorReport, error) {
	if c.storeManagement == nil {
		return nil, xerrors.Errorf(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if c.hooksOrchestrator == nil {
		return nil, xerrors.Errorf(Localize("hooks orchestrator is not configured", "hooks orchestrator が設定されていません"))
	}
	return c.buildDoctorReport(ctx, doctorCommandInput{
		dbPath:         opts.dbPath,
		currentVersion: "",
	})
}

func countCockpitHookIssues(report *doctorReport) (warn int, fail int) {
	if report == nil {
		return 0, 0
	}
	for _, check := range report.Checks {
		if check.Section != "Hooks" && check.Section != "MCP" {
			continue
		}
		switch check.Severity {
		case doctorSeverityWarn:
			warn++
		case doctorSeverityFail:
			fail++
		}
	}
	return warn, fail
}

type cockpitWarning struct {
	severity string
	label    string
	hint     string
}

func (s cockpitHomeSnapshot) warnings() []cockpitWarning {
	warnings := make([]cockpitWarning, 0, 6)
	if s.DoctorError != "" {
		warnings = append(warnings, cockpitWarning{severity: "FAIL", label: "doctor unavailable", hint: s.DoctorError})
	}
	if s.DoctorFailCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "FAIL", label: fmt.Sprintf("doctor failures=%d", s.DoctorFailCount), hint: "run `traceary doctor` for remediation details"})
	}
	if s.HookFailCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "FAIL", label: fmt.Sprintf("hook/MCP failures=%d", s.HookFailCount), hint: "run `traceary doctor --json` and inspect Hooks/MCP sections"})
	}
	if s.StaleActiveSessionCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("stale active sessions=%d", s.StaleActiveSessionCount), hint: "run `traceary session gc --stale-after 24h --dry-run`"})
	}
	if s.DoctorWarnCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("doctor warnings=%d", s.DoctorWarnCount), hint: "run `traceary doctor`"})
	}
	if s.HookWarnCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("hook/MCP warnings=%d", s.HookWarnCount), hint: "run `traceary doctor --json` and inspect Hooks/MCP sections"})
	}
	if s.CandidateMemoryCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("candidate memories=%d", s.CandidateMemoryCount), hint: "review with `traceary memory inbox review`"})
	}
	if s.RecentFailureCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("recent failures=%d", s.RecentFailureCount), hint: "open `traceary top` or `traceary tail` for details"})
	}
	if s.LargePayloadCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("large payloads=%d", s.LargePayloadCount), hint: "inspect full events with `traceary show <event_id>`"})
	}
	return warnings
}

func newCockpitNonInteractiveError(output io.Writer) error {
	guidance := Localize(
		"Traceary cockpit requires an interactive terminal (TTY).\nUse the existing non-interactive commands instead:\n  traceary top --snapshot [--json]\n  traceary tail [--follow]\n  traceary doctor --json\n  traceary session handoff\n  traceary memory inbox list\nRun `traceary tui` from a terminal to open the cockpit.",
		"Traceary cockpit には対話 terminal (TTY) が必要です。\n非対話 shell では既存 command を使ってください:\n  traceary top --snapshot [--json]\n  traceary tail [--follow]\n  traceary doctor --json\n  traceary session handoff\n  traceary memory inbox list\nterminal から `traceary tui` を実行すると cockpit を開けます。",
	)
	if output != nil {
		_, _ = fmt.Fprintln(output, guidance)
	}
	return cockpitExitError{message: guidance, exitCode: cockpitExitCodeNotInteractive}
}

type cockpitModel struct {
	keys     tui.KeyMap
	styles   tui.Styles
	showHelp bool
	home     cockpitHomeSnapshot
}

func newCockpitModel(keys tui.KeyMap, styles tui.Styles, home cockpitHomeSnapshot) cockpitModel {
	return cockpitModel{keys: keys, styles: styles, home: home}
}

func (m cockpitModel) Init() tea.Cmd { return nil }

func (m cockpitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil
		}
	}
	return m, nil
}

func (m cockpitModel) View() string {
	lines := []string{
		m.styles.Title.Render("Traceary cockpit"),
		"",
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s db=%s", formatJSONTime(m.home.LoadedAt), formatOptionalColumn(m.home.DBPath))),
		"",
		m.styles.Subtle.Render("ATTENTION"),
	}
	warnings := m.home.warnings()
	if len(warnings) == 0 {
		lines = append(lines, m.styles.Success.Render("• No immediate cockpit warnings."))
	} else {
		for _, warning := range warnings {
			style := m.styles.Warning
			if warning.severity == "FAIL" {
				style = m.styles.Error
			}
			lines = append(lines, style.Render(fmt.Sprintf("• [%s] %s — %s", warning.severity, warning.label, warning.hint)))
		}
	}
	lines = append(lines,
		"",
		m.styles.Subtle.Render("OVERVIEW"),
		fmt.Sprintf("• doctor: pass=%d warn=%d fail=%d", m.home.DoctorPassCount, m.home.DoctorWarnCount, m.home.DoctorFailCount),
		fmt.Sprintf("• hooks/mcp: warn=%d fail=%d", m.home.HookWarnCount, m.home.HookFailCount),
		fmt.Sprintf("• sessions: stale_active=%d recent_failures=%d recent_commands=%d", m.home.StaleActiveSessionCount, m.home.RecentFailureCount, m.home.RecentCommandCount),
		fmt.Sprintf("• memories: accepted=%d candidate=%d stale=%d", m.home.AcceptedMemoryCount, m.home.CandidateMemoryCount, m.home.StaleMemoryCount),
		fmt.Sprintf("• payloads: large=%d", m.home.LargePayloadCount),
		"",
		m.styles.Subtle.Render("Planned cockpit surfaces:"),
		"• sessions: top dashboard and detail drill-down",
		"• tail: live event stream",
		"• memory: inbox notifications and review launcher",
		"• doctor: warnings and remediation commands",
		"",
		m.styles.Help.Render("q/esc/ctrl+c quit · ? help"),
	)
	if m.showHelp {
		lines = append(lines,
			"",
			m.styles.Subtle.Render("Fallback commands available today:"),
			"traceary top --snapshot [--json]",
			"traceary tail [--follow]",
			"traceary doctor --json",
			"traceary session handoff",
			"traceary memory inbox review",
		)
	}
	return strings.Join(lines, "\n")
}

// cockpitIO resolves the stdin/stdout pair the cockpit TUI should drive. Tests
// pass a non-file writer (e.g. *bytes.Buffer), making tui.Interactive refuse
// the run before any Bubble Tea program is spawned.
func cockpitIO(output io.Writer) (*os.File, *os.File) {
	stdout, _ := output.(*os.File)
	return os.Stdin, stdout
}
