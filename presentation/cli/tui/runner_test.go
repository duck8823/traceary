package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// noopModel is the smallest possible tea.Model. We never actually run it via
// a Program here; it exists so we can verify the runner's pre-flight checks
// in tests that must not depend on a real TTY.
type noopModel struct{}

func (noopModel) Init() tea.Cmd                       { return nil }
func (noopModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return noopModel{}, tea.Quit }
func (noopModel) View() string                        { return "" }

func TestRun_RefusesNonInteractive(t *testing.T) {
	dir := t.TempDir()
	in, err := os.Create(filepath.Join(dir, "in"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = in.Close() })

	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = out.Close() })

	err = Run(noopModel{}, RunOptions{Input: in, Output: out})
	if !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("Run on non-TTY = %v, want ErrNotInteractive", err)
	}
}

func TestRun_RefusesWhenStdoutIsNotTTY(t *testing.T) {
	// os.Stdin under `go test` is typically a pipe, but we don't rely on
	// that — we only assert that swapping stdout for a regular file forces
	// the non-interactive guard, which is the path the inbox-review and top
	// dashboard need to take when output is redirected.
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "redirected.log"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = out.Close() })

	err = Run(noopModel{}, RunOptions{Output: out})
	if !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("Run with redirected stdout = %v, want ErrNotInteractive", err)
	}
}

func TestQuit_ReexportsBubbleTea(t *testing.T) {
	if Quit() == nil {
		t.Fatal("Quit() must return a non-nil tea.Msg")
	}
}
