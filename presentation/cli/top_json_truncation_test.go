package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// The top --snapshot JSON contract truncates large command bodies on
// the recent-failure and recent-command panes so a single multi-hundred-
// line command_executed payload does not balloon the script-friendly
// snapshot. The tests below pin the shape contract: text rendering
// stays line-tabular (covered elsewhere via truncateMessage), JSON adds
// the additive metadata fields, boundary-length rows stay untruncated,
// and small payloads stay byte-for-byte identical to the legacy shape.

func newTopSnapshotEvent(id, body string, ts time.Time) *model.Event {
	return model.EventOf(
		domtypes.EventID(id),
		domtypes.EventKindCommandExecuted,
		domtypes.Client("claude"),
		domtypes.Agent("claude/explore"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		body,
		ts,
	)
}

func TestWriteTopSnapshotJSON_TruncatesLargeRecentCommandBody(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	huge := strings.Repeat("x", apptypes.DefaultTopSnapshotBodyLimit+50)
	ev := newTopSnapshotEvent("evt-cmd-huge", huge, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		RecentCommands: []*model.Event{ev},
		Now:            createdAt,
	}); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}

	var payload struct {
		RecentCommands []struct {
			Message       string `json:"message"`
			Truncated     bool   `json:"truncated"`
			MessageLength int    `json:"message_length"`
			MessageBytes  int    `json:"message_bytes"`
		} `json:"recent_commands"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	if len(payload.RecentCommands) != 1 {
		t.Fatalf("recent_commands length = %d, want 1", len(payload.RecentCommands))
	}
	got := payload.RecentCommands[0]
	if !got.Truncated {
		t.Fatalf("Truncated = false, want true for a body of %d runes", len(huge))
	}
	if got.MessageLength != len(huge) {
		t.Fatalf("message_length = %d, want %d", got.MessageLength, len(huge))
	}
	if got.MessageBytes != len(huge) {
		t.Fatalf("message_bytes = %d, want %d", got.MessageBytes, len(huge))
	}
	if !strings.HasSuffix(got.Message, "…") {
		t.Fatalf("truncated message must end with ellipsis: %q", got.Message)
	}
	if want := apptypes.DefaultTopSnapshotBodyLimit + 1; len([]rune(got.Message)) != want { // limit runes + ellipsis
		t.Fatalf("len(message) in runes = %d, want %d", len([]rune(got.Message)), want)
	}
}

func TestWriteTopSnapshotJSON_RecognizesBrokenPipeWriter(t *testing.T) {
	t.Parallel()

	err := writeTopSnapshotJSON(brokenPipeWriter{}, topDataSnapshot{
		Now: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("writeTopSnapshotJSON() error = nil, want broken pipe")
	}
	if !IsBrokenPipeError(err) {
		t.Fatalf("IsBrokenPipeError(%v) = false, want true", err)
	}
}

type brokenPipeWriter struct{}

func (brokenPipeWriter) Write(_ []byte) (int, error) {
	return 0, syscall.EPIPE
}

func TestWriteTopSnapshotJSON_LeavesSmallBodiesUntouched(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ev := newTopSnapshotEvent("evt-cmd-small", "go test ./...", createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		RecentCommands: []*model.Event{ev},
		Now:            createdAt,
	}); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}

	// The additive metadata keys must be omitted (omitempty) when no
	// truncation happened so v0.16-2 consumers see the same shape they
	// did before.
	if strings.Contains(buf.String(), "\"truncated\"") {
		t.Fatalf("untruncated row must omit `truncated`: %s", buf.String())
	}
	if strings.Contains(buf.String(), "\"message_length\"") {
		t.Fatalf("untruncated row must omit `message_length`: %s", buf.String())
	}
	if strings.Contains(buf.String(), "\"message_bytes\"") {
		t.Fatalf("untruncated row must omit `message_bytes`: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "\"message\": \"go test ./...\"") {
		t.Fatalf("message body was rewritten unexpectedly: %s", buf.String())
	}
}

func TestWriteTopSnapshotJSON_BoundaryLengthIsNotTruncated(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	boundary := strings.Repeat("a", apptypes.DefaultTopSnapshotBodyLimit)
	ev := newTopSnapshotEvent("evt-cmd-boundary", boundary, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		RecentCommands: []*model.Event{ev},
		Now:            createdAt,
	}); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}

	if strings.Contains(buf.String(), "\"truncated\": true") {
		t.Fatalf("boundary-length row must not be truncated: %s", buf.String())
	}
	if !strings.Contains(buf.String(), boundary) {
		t.Fatalf("boundary message was rewritten unexpectedly")
	}
}

func TestWriteTopSnapshotJSON_TruncatesRecentFailures(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	huge := strings.Repeat("y", apptypes.DefaultTopSnapshotBodyLimit+25)
	ev := newTopSnapshotEvent("evt-fail-huge", huge, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Failures: []*model.Event{ev},
		Now:      createdAt,
	}); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}

	var payload struct {
		Failures []struct {
			Truncated     bool `json:"truncated"`
			MessageLength int  `json:"message_length"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Failures) != 1 || !payload.Failures[0].Truncated {
		t.Fatalf("expected recent failures pane to truncate large payloads: %+v", payload.Failures)
	}
	if payload.Failures[0].MessageLength != len(huge) {
		t.Fatalf("message_length = %d, want %d", payload.Failures[0].MessageLength, len(huge))
	}
}

func TestWriteTopSnapshotTextEvents_TruncatesLongBody(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	huge := strings.Repeat("z", 400)
	ev := newTopSnapshotEvent("evt-cmd-huge", huge, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotTextEvents(&buf, "RECENT COMMANDS", []*model.Event{ev}, time.UTC); err != nil {
		t.Fatalf("writeTopSnapshotTextEvents: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "…") {
		t.Fatalf("text snapshot row must show truncation ellipsis: %q", out)
	}
	if strings.Contains(out, huge) {
		t.Fatalf("text snapshot row leaked the full body: %q", out)
	}
}
