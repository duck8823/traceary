package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

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
	_ = ctx
	_ = opts
	stdin, stdout := cockpitIO(output)
	if !tui.Interactive(stdin, stdout) {
		return newCockpitNonInteractiveError(output)
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles())
	if err := tui.Run(model, tui.RunOptions{Input: stdin, Output: stdout, AltScreen: true}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run cockpit TUI", "cockpit TUI の実行に失敗しました"), err)
	}
	return nil
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
}

func newCockpitModel(keys tui.KeyMap, styles tui.Styles) cockpitModel {
	return cockpitModel{keys: keys, styles: styles}
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
		"Home status model lands in v0.17-2. This shell pins the explicit TTY entrypoint first, without changing bare `traceary`.",
		"",
		m.styles.Subtle.Render("Planned cockpit surfaces:"),
		"• home status: DB/hooks/doctor summary, stale sessions, memory counts",
		"• sessions: top dashboard and detail drill-down",
		"• tail: live event stream",
		"• memory: inbox notifications and review launcher",
		"• doctor: warnings and remediation commands",
		"",
		m.styles.Help.Render("q/esc/ctrl+c quit · ? help"),
	}
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
