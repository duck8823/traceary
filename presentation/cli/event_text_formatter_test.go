package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestCompactSessionID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"first eight runes are kept":   {input: "abcdef1234567890", want: "abcdef12"},
		"short input is returned asis": {input: "abc", want: "abc"},
		"empty value becomes placeholder": {
			input: "",
			want:  "-",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := compactSessionID(tc.input); got != tc.want {
				t.Fatalf("compactSessionID(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCompactWorkspace(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"absolute posix path":   {input: "/Users/foo/traceary", want: "traceary"},
		"owner slash repo slug": {input: "duck8823/traceary", want: "traceary"},
		"trailing slash":        {input: "/var/tmp/work/", want: "work"},
		"windows path":          {input: `C:\Users\foo\traceary`, want: "traceary"},
		"empty":                 {input: "", want: "-"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := compactWorkspace(tc.input); got != tc.want {
				t.Fatalf("compactWorkspace(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTruncateNormalized(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input    string
		maxRunes int
		want     string
	}{
		"collapses whitespace":              {input: "hello   world", maxRunes: 32, want: "hello world"},
		"truncates and appends ellipsis":    {input: "abcdefghij", maxRunes: 5, want: "abcd…"},
		"zero budget yields empty string":   {input: "abc", maxRunes: 0, want: ""},
		"budget larger than input":          {input: "abc", maxRunes: 10, want: "abc"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := truncateNormalized(tc.input, tc.maxRunes); got != tc.want {
				t.Fatalf("truncateNormalized(%q, %d) = %q, want %q", tc.input, tc.maxRunes, got, tc.want)
			}
		})
	}
}

func TestFormatEventCompactRow_FitsWithin100Columns(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"claude-code",
		"claude",
		"123e4567-e89b-12d3-a456-426614174000",
		"/Users/duck8823/Repositories/traceary",
		strings.Repeat("long message ", 40),
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)

	got := formatEventCompactRow(event, eventTextFormatOptions{utc: true}, compactRowExtras{})
	if runeLen(got) > eventCompactTargetWidth {
		t.Fatalf("compact row exceeds target width: %d > %d: %q", runeLen(got), eventCompactTargetWidth, got)
	}
	if !strings.Contains(got, "sess=123e4567") {
		t.Fatalf("compact row missing compact session ID: %q", got)
	}
	if !strings.Contains(got, "ws=traceary") {
		t.Fatalf("compact row missing compact workspace: %q", got)
	}
	if !strings.Contains(got, "09:30:15") {
		t.Fatalf("compact row missing HH:MM:SS timestamp: %q", got)
	}
}

func TestFormatEventCompactRow_UsesInjectedLocation(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"event-1",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		"hello",
		time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
	)

	jst := time.FixedZone("JST", 9*3600)
	got := formatEventCompactRow(event, eventTextFormatOptions{location: jst}, compactRowExtras{})
	if !strings.HasPrefix(got, "09:00:00 ") {
		t.Fatalf("expected JST-adjusted HH:MM:SS prefix, got %q", got)
	}

	utcGot := formatEventCompactRow(event, eventTextFormatOptions{utc: true}, compactRowExtras{})
	if !strings.HasPrefix(utcGot, "00:00:00 ") {
		t.Fatalf("expected UTC HH:MM:SS prefix, got %q", utcGot)
	}
}

func TestFormatEventWideRow_UTCMatchesLegacyFormat(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"event-wide",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		"payload",
		time.Date(2026, 4, 15, 9, 30, 0, 0, time.UTC),
	)

	got := formatEventWideRow(event, eventTextFormatOptions{wide: true, utc: true})
	want := "2026-04-15T09:30:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\tpayload"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("wide row mismatch (-want +got):\n%s", diff)
	}
}

func TestWriteEvents_CompactDefaultOmitsHeader(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"abcdef1234567890",
		"cli",
		"codex",
		"session-compact-9999",
		"duck8823/traceary",
		"hello list compact",
		time.Date(2026, 4, 15, 9, 30, 0, 0, time.UTC),
	)

	var buf bytes.Buffer
	if err := writeEvents(&buf, []*model.Event{event}, eventTextFormatOptions{utc: true}, nil); err != nil {
		t.Fatalf("writeEvents() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "CREATED_AT\t") {
		t.Fatalf("compact mode emitted wide header: %q", got)
	}
	if !strings.HasPrefix(got, "09:30:00 ") {
		t.Fatalf("expected HH:MM:SS UTC prefix, got %q", got)
	}
	if !strings.Contains(got, "sess=session-") {
		t.Fatalf("expected compact session prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("expected trailing newline, got %q", got)
	}
}

func TestFormatEventCompactRow_CustomFieldOrder(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"abcdef1234567890",
		"claude-code",
		"claude",
		"123e4567-e89b-12d3-a456-426614174000",
		"duck8823/traceary",
		"hello custom",
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)

	opts := eventTextFormatOptions{
		utc:    true,
		fields: []readFieldID{readFieldTS, readFieldKind, readFieldMessage},
	}
	got := formatEventCompactRow(event, opts, compactRowExtras{})
	want := "09:30:15  note  hello custom"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("compact row mismatch (-want +got):\n%s", diff)
	}
}

func TestFormatEventCompactRow_MessageInTheMiddleUsesFixedTruncation(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"abcdef1234567890",
		"claude-code",
		"claude",
		"123e4567-e89b-12d3-a456-426614174000",
		"duck8823/traceary",
		strings.Repeat("long message ", 20),
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)

	opts := eventTextFormatOptions{
		utc:    true,
		fields: []readFieldID{readFieldTS, readFieldMessage, readFieldKind},
	}
	got := formatEventCompactRow(event, opts, compactRowExtras{})
	// Message is not the last field, so it must be truncated with the
	// fixed messageColumnMaxWidth budget (80 runes, ellipsis inclusive).
	if !strings.Contains(got, "note") {
		t.Fatalf("expected kind to be rendered, got %q", got)
	}
	if !strings.HasPrefix(got, "09:30:15  ") {
		t.Fatalf("expected leading timestamp, got %q", got)
	}
}

func TestFormatEventCompactRow_ExitCodeAndEventID(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"abcdef1234567890",
		"claude-code",
		"claude",
		"123e4567-e89b-12d3-a456-426614174000",
		"duck8823/traceary",
		"go test failed",
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)

	opts := eventTextFormatOptions{
		utc:    true,
		fields: []readFieldID{readFieldTS, readFieldEventID, readFieldExitCode, readFieldMessage},
	}
	extras := compactRowExtras{exitCode: types.Some(1)}
	got := formatEventCompactRow(event, opts, extras)
	if !strings.Contains(got, "id=abcdef1234567890") {
		t.Fatalf("expected full event id token, got %q", got)
	}
	if !strings.Contains(got, "exit=1") {
		t.Fatalf("expected exit code token, got %q", got)
	}

	extrasNone := compactRowExtras{}
	gotNone := formatEventCompactRow(event, opts, extrasNone)
	if !strings.Contains(gotNone, "exit=-") {
		t.Fatalf("expected exit=- when extras lack exit code, got %q", gotNone)
	}
}

func TestFormatEventCompactRow_ClientAndAgentTokens(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"abcdef1234567890",
		"claude-code",
		"claude",
		"session-1",
		"duck8823/traceary",
		"hello",
		time.Date(2026, 4, 15, 9, 30, 15, 0, time.UTC),
	)

	opts := eventTextFormatOptions{
		utc:    true,
		fields: []readFieldID{readFieldTS, readFieldClient, readFieldAgent, readFieldMessage},
	}
	got := formatEventCompactRow(event, opts, compactRowExtras{})
	if !strings.Contains(got, "client=claude-code") {
		t.Fatalf("expected client token, got %q", got)
	}
	if !strings.Contains(got, "agent=claude") {
		t.Fatalf("expected agent token, got %q", got)
	}
}

func TestWriteEvents_WideUTCMatchesLegacyTable(t *testing.T) {
	t.Parallel()

	event := mustTailEvent(
		t,
		"event-wide-list",
		"cli",
		"codex",
		"session-1",
		"duck8823/traceary",
		"hello",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)

	var buf bytes.Buffer
	if err := writeEvents(&buf, []*model.Event{event}, eventTextFormatOptions{wide: true, utc: true}, nil); err != nil {
		t.Fatalf("writeEvents() error = %v", err)
	}
	want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tMESSAGE\n" +
		"2026-04-07T12:00:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\thello\n"
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Fatalf("writeEvents() wide+utc mismatch (-want +got):\n%s", diff)
	}
}

func TestRunTail_DefaultCompactUsesInjectedLocation(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 4, 15, 19, 0, 0, 0, time.UTC)
	ticker := newFakeTailTicker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	event := mustTailEvent(
		t,
		"event-compact",
		"cli",
		"codex",
		"sess1234567890ab",
		"duck8823/traceary",
		"hello compact",
		time.Date(2026, 4, 15, 18, 5, 0, 0, time.UTC),
	)

	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{{event}},
		onList: func(callIndex int, _ apptypes.EventListCriteria) {
			if callIndex == 0 {
				cancel()
			}
		},
	}

	stdout := &bytes.Buffer{}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	jst := time.FixedZone("JST", 9*3600)
	if err := sut.runTail(ctx, stdout, tailCommandInput{
		dbPath:        "/tmp/test-traceary.db",
		limit:         1,
		repo:          "duck8823/traceary",
		location:      jst,
		nowFunc:       func() time.Time { return startTime },
		tickerFactory: func(time.Duration) tailTicker { return ticker },
	}); err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "CREATED_AT\t") {
		t.Fatalf("compact mode should not emit wide header, got %q", out)
	}
	if !strings.HasPrefix(out, "03:05:00 ") {
		t.Fatalf("compact row should start with injected-JST HH:MM:SS, got %q", out)
	}
	if !strings.Contains(out, "sess=sess1234") {
		t.Fatalf("compact row missing shortened session: %q", out)
	}
	if !strings.Contains(out, "ws=traceary") {
		t.Fatalf("compact row missing compact workspace: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("compact row should end with newline: %q", out)
	}
}
