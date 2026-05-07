package tui

import "github.com/charmbracelet/lipgloss"

// Styles bundles the Lip Gloss styles shared across Traceary TUIs.
//
// Keep the palette small: every interactive screen should pick from these
// styles so the inbox-review UI and the top dashboard stay visually
// consistent. Add a new field here only when an existing style cannot be
// reused without distortion.
type Styles struct {
	// Title styles a single-line screen header.
	Title lipgloss.Style
	// Subtle styles secondary text such as filters, counts, and meta lines.
	Subtle lipgloss.Style
	// Active highlights the currently focused row or selection.
	Active lipgloss.Style
	// Idle dims rows that are not the current selection.
	Idle lipgloss.Style
	// Success styles positive state (active sessions, accepted candidates).
	Success lipgloss.Style
	// Warning styles cautionary state (stale rows, soft validation).
	Warning lipgloss.Style
	// Error styles failure states such as load errors or rejected items.
	Error lipgloss.Style
	// Help styles the bottom-of-screen key hints.
	Help lipgloss.Style
	// Border wraps a panel; reused by both inbox preview and top tree panes.
	Border lipgloss.Style
}

// DefaultStyles returns the canonical Traceary TUI palette.
//
// The colors deliberately match the ANSI hints used elsewhere in the CLI
// (see read_color.go) so output between interactive and non-interactive
// modes stays readable.
func DefaultStyles() Styles {
	return Styles{
		Title:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")),
		Subtle:  lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Active:  lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true),
		Idle:    lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Success: lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		Help:    lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true),
		Border:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")),
	}
}
