package tui

import (
	"errors"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/xerrors"
)

// ErrNotInteractive is returned when Run is invoked without a TTY-backed
// input/output pair. Callers should detect this and fall back to a
// non-interactive snapshot rendering.
var ErrNotInteractive = errors.New("tui: stdin/stdout is not a terminal")

// RunOptions configures Run.
//
// Input/Output default to os.Stdin/os.Stdout when nil. AltScreen toggles the
// alternate-screen buffer (top dashboard wants it; an inline review prompt
// may not). ExtraTeaOptions are appended after the runner's own options so
// callers can layer Bubble Tea features (mouse, focus reporting, etc.).
type RunOptions struct {
	Input           *os.File
	Output          *os.File
	AltScreen       bool
	ExtraTeaOptions []tea.ProgramOption
}

// Model is a thin re-export of tea.Model so callers in package cli can build
// against tui without importing bubbletea directly. Tests that exercise the
// model/update/view seam can instantiate concrete types and drive them
// without spawning a Program.
type Model = tea.Model

// Cmd is a thin re-export of tea.Cmd for the same reason as Model.
type Cmd = tea.Cmd

// Msg is a thin re-export of tea.Msg.
type Msg = tea.Msg

// Quit is the canonical quit command. Re-exported so screens that satisfy
// KeyMap.Quit can return tui.Quit without importing bubbletea.
func Quit() Msg { return tea.Quit() }

// Run launches model under a Bubble Tea program with Traceary's standard
// safety net:
//
//   - The runner refuses to start when stdin/stdout are not TTYs and returns
//     ErrNotInteractive so the caller can render a snapshot instead.
//   - Bubble Tea installs its own SIGINT/SIGTERM handler which restores the
//     terminal before exiting; we keep that behavior on so Ctrl-C never
//     leaves the user in a broken raw-mode shell.
//   - tea.Program.Run guarantees terminal restoration on every return path,
//     including panics inside the model. We surface any restore-time error
//     up to the caller wrapped with xerrors so the stack is preserved.
func Run(model Model, opts RunOptions) error {
	_, err := RunModel(model, opts)
	return err
}

// RunModel is the variant of Run that returns the final model after the
// Bubble Tea program exits. Screens that accumulate state inside the model
// (e.g. memory inbox review queues operator decisions) need the post-quit
// snapshot so they can apply the queued work; Run remains for screens that
// are pure renderers and only care about the run-time error.
func RunModel(model Model, opts RunOptions) (Model, error) {
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}
	if !Interactive(in, out) {
		return model, ErrNotInteractive
	}

	teaOpts := []tea.ProgramOption{
		tea.WithInput(io.Reader(in)),
		tea.WithOutput(io.Writer(out)),
	}
	if opts.AltScreen {
		teaOpts = append(teaOpts, tea.WithAltScreen())
	}
	teaOpts = append(teaOpts, opts.ExtraTeaOptions...)

	prog := tea.NewProgram(model, teaOpts...)
	final, err := prog.Run()
	if err != nil {
		return final, xerrors.Errorf("tui: program run: %w", err)
	}
	return final, nil
}
