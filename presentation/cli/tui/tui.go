// Package tui hosts the shared Bubble Tea / Bubbles / Lip Gloss foundation
// used by Traceary's interactive CLI surfaces.
//
// The package intentionally keeps a small, opinionated public surface so the
// upcoming `memory inbox review` UI and the redesigned `top` dashboard share
// the same key conventions, color palette, and lifecycle (terminal restore,
// non-TTY guard, Ctrl-C handling).
//
// Callers should treat Model/Update/View as the testable seam: build Bubble
// Tea models against tui.KeyMap and tui.Styles, then pass them to Run for
// production use. Tests should drive the model directly without going through
// a real terminal.
package tui
