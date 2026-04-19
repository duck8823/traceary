package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
)

// TestFormatEventCompactRow_ColorEnabledWrapsRow pins the highlight behavior
// for prompt events under the v0.7-7 color path.
func TestFormatEventCompactRow_ColorEnabledWrapsRow(t *testing.T) {
	t.Parallel()

	base := mustTailEvent(
		t,
		"abcdef1234567890",
		"claude-code",
		"claude",
		"session-1",
		"duck8823/traceary",
		"hello prompt",
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)
	event := model.EventOf(
		base.EventID(),
		"prompt",
		base.Client(),
		base.Agent(),
		base.SessionID(),
		base.Workspace(),
		base.Body(),
		base.CreatedAt(),
	)

	got := formatEventCompactRow(event, eventTextFormatOptions{utc: true, colorEnabled: true}, compactRowExtras{})
	if !strings.HasPrefix(got, ansiCyan) {
		t.Fatalf("expected cyan prefix for prompt kind, got %q", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Fatalf("expected reset suffix, got %q", got)
	}

	plain := formatEventCompactRow(event, eventTextFormatOptions{utc: true}, compactRowExtras{})
	if strings.Contains(plain, ansiCyan) || strings.Contains(plain, ansiReset) {
		t.Fatalf("color-disabled row must stay plain, got %q", plain)
	}
}

// TestFormatEventCompactRow_StripsTerminalControlCharsFromBody is the
// regression test for the terminal-injection HIGH flagged by Codex on
// PR #581. A body that embeds ANSI / BEL must render to plain characters
// so the user's terminal cannot be hijacked through a saved event.
func TestFormatEventCompactRow_StripsTerminalControlCharsFromBody(t *testing.T) {
	t.Parallel()

	body := "hello\x1b[31m\x07world"
	event := mustTailEvent(
		t,
		"abcdef1234567890",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		body,
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)

	got := formatEventCompactRow(event, eventTextFormatOptions{utc: true}, compactRowExtras{})
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("compact output leaked ESC: %q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Fatalf("compact output leaked BEL: %q", got)
	}
	// The stray '[31m' bytes that remain after ESC removal cannot drive a
	// terminal any more (the leading ESC is gone), so they only show up as
	// plain literal text. Confirm the sanitizer at least stripped the
	// control bytes bracketing the payload.
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("expected hello and world preserved, got %q", got)
	}
}

// TestNormalizeTabularColumn_StripsControlChars pins the shared sanitizer
// called by both wide and compact renderers. Removing ANSI and hyperlink
// (OSC 8) sequences here means every read command benefits from the fix.
func TestNormalizeTabularColumn_StripsControlChars(t *testing.T) {
	t.Parallel()

	in := "a\x1b[31m\x07b\tc\n\x1b]8;;https://example.com\x07d"
	got := normalizeTabularColumn(in)
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("expected ESC and BEL stripped, got %q", got)
	}
	// The leading-ESC-removed residue (`[31m`, `]8;;…`) is left in place
	// because only the control runes themselves are removed; what matters
	// is that the terminal cannot interpret any of it as a CSI / OSC
	// sequence once the ESC prefix is gone.
	if !strings.Contains(got, "a") || !strings.Contains(got, "d") {
		t.Fatalf("expected visible letters preserved, got %q", got)
	}
}
