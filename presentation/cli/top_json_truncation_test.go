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
	}, topSnapshotProfileOperator); err != nil {
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
	}, topSnapshotProfileOperator)
	if err == nil {
		t.Fatal("writeTopSnapshotJSON(, topSnapshotProfileOperator) error = nil, want broken pipe")
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
	}, topSnapshotProfileOperator); err != nil {
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
	}, topSnapshotProfileOperator); err != nil {
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
	}, topSnapshotProfileOperator); err != nil {
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

func newTopSnapshotSessionNode(sessionID, eventID, latestMessage string, started time.Time) *sessionNode {
	return &sessionNode{summary: apptypes.SessionSummaryOf(
		domtypes.SessionID(sessionID),
		domtypes.Workspace("github.com/duck8823/traceary"),
		started,
		domtypes.None[time.Time](),
		"active",
		3,
		1,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(""),
		domtypes.Client("claude"),
		started,
		apptypes.SessionSummaryLatestEventOf(domtypes.EventID(eventID), domtypes.EventKindCommandExecuted, latestMessage),
	)}
}

// A noisy latest event (e.g. a `traceary doctor --json` dump captured as the
// session's latest command_executed) must not be re-emitted verbatim on the
// snapshot node: it is truncated under the shared body cap, the rune/byte
// metadata announces the cut, and latest_event_id is the retrieval hint for
// fetching the full body via `traceary show`.
func TestWriteTopSnapshotJSON_TruncatesLatestEventMessage(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	huge := strings.Repeat("d", apptypes.DefaultTopSnapshotBodyLimit+120)
	node := newTopSnapshotSessionNode("sess-1", "evt-latest-huge", huge, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Sessions: []*sessionNode{node},
		Now:      createdAt,
	}, topSnapshotProfileOperator); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}

	var payload struct {
		Sessions []struct {
			LatestEventID               string `json:"latest_event_id"`
			LatestEventMessage          string `json:"latest_event_message"`
			LatestEventMessageTruncated bool   `json:"latest_event_message_truncated"`
			LatestEventMessageLength    int    `json:"latest_event_message_length"`
			LatestEventMessageBytes     int    `json:"latest_event_message_bytes"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(payload.Sessions))
	}
	got := payload.Sessions[0]
	if !got.LatestEventMessageTruncated {
		t.Fatalf("latest_event_message_truncated = false, want true")
	}
	if got.LatestEventMessageLength != len(huge) {
		t.Fatalf("latest_event_message_length = %d, want %d", got.LatestEventMessageLength, len(huge))
	}
	if got.LatestEventMessageBytes != len(huge) {
		t.Fatalf("latest_event_message_bytes = %d, want %d", got.LatestEventMessageBytes, len(huge))
	}
	if want := apptypes.DefaultTopSnapshotBodyLimit + 1; len([]rune(got.LatestEventMessage)) != want {
		t.Fatalf("len(latest_event_message) in runes = %d, want %d", len([]rune(got.LatestEventMessage)), want)
	}
	if !strings.HasSuffix(got.LatestEventMessage, "…") {
		t.Fatalf("truncated latest_event_message must end with ellipsis: %q", got.LatestEventMessage)
	}
	if strings.Contains(got.LatestEventMessage, huge) {
		t.Fatalf("snapshot node leaked the full latest event body")
	}
	if got.LatestEventID != "evt-latest-huge" {
		t.Fatalf("latest_event_id = %q, want evt-latest-huge (retrieval hint)", got.LatestEventID)
	}
}

// A small latest event keeps the legacy shape: no additive truncation
// metadata keys appear and the body is byte-for-byte identical.
func TestWriteTopSnapshotJSON_LeavesSmallLatestEventMessageUntouched(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	node := newTopSnapshotSessionNode("sess-2", "evt-small", "go build ./...", createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Sessions: []*sessionNode{node},
		Now:      createdAt,
	}, topSnapshotProfileOperator); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "latest_event_message_truncated") {
		t.Fatalf("untruncated node must omit latest_event_message_truncated: %s", out)
	}
	if strings.Contains(out, "latest_event_message_length") {
		t.Fatalf("untruncated node must omit latest_event_message_length: %s", out)
	}
	if !strings.Contains(out, "\"latest_event_message\": \"go build ./...\"") {
		t.Fatalf("latest_event_message body was rewritten unexpectedly: %s", out)
	}
}

// reliability.large_payloads.samples carries only body-safe metadata and a
// retrieval hint, never the full payload. A sample is emitted per oversized
// event, deduped by event id when the same event appears in both the failure
// and recent-command panes, and bounded by topLargePayloadSampleLimit.
func TestWriteTopSnapshotJSON_LargePayloadSamplesAreMetadataOnly(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	secret := strings.Repeat("S", apptypes.DefaultTopSnapshotBodyLimit+200)
	huge := "noisy first line\n" + secret
	shared := newTopSnapshotEvent("evt-shared-huge", huge, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Reliability: topReliabilityMetrics{
			LargePayloads: topLargePayloadMetricsOf(
				[]*model.Event{shared},
				[]*model.Event{shared},
				apptypes.DefaultTopSnapshotBodyLimit,
			),
		},
		Now: createdAt,
	}, topSnapshotProfileOperator); err != nil {
		t.Fatalf("writeTopSnapshotJSON: %v", err)
	}

	var payload struct {
		Reliability struct {
			LargePayloads struct {
				Samples []struct {
					EventID       string `json:"event_id"`
					Kind          string `json:"kind"`
					Source        string `json:"source"`
					MessageLength int    `json:"message_length"`
					MessageBytes  int    `json:"message_bytes"`
					FirstLine     string `json:"first_line"`
					RetrievalHint string `json:"retrieval_hint"`
				} `json:"samples"`
			} `json:"large_payloads"`
		} `json:"reliability"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	samples := payload.Reliability.LargePayloads.Samples
	if len(samples) != 1 {
		t.Fatalf("samples length = %d, want 1 (deduped by event id)", len(samples))
	}
	s := samples[0]
	if s.EventID != "evt-shared-huge" {
		t.Fatalf("sample event_id = %q, want evt-shared-huge", s.EventID)
	}
	if s.Source != largePayloadSourceFailure {
		t.Fatalf("sample source = %q, want %q (failures sampled first)", s.Source, largePayloadSourceFailure)
	}
	if s.MessageLength != len([]rune(huge)) {
		t.Fatalf("sample message_length = %d, want %d", s.MessageLength, len([]rune(huge)))
	}
	if s.RetrievalHint != "traceary show evt-shared-huge" {
		t.Fatalf("sample retrieval_hint = %q, want traceary show evt-shared-huge", s.RetrievalHint)
	}
	if s.FirstLine != "noisy first line" {
		t.Fatalf("sample first_line = %q, want %q", s.FirstLine, "noisy first line")
	}
	// The full payload must never reach the snapshot, in any field.
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("large_payloads sample leaked the full body into the snapshot")
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

func TestWriteTopSnapshotJSON_AIProfileOmitsBodiesAndCandidateFacts(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	huge := strings.Repeat("x", apptypes.DefaultTopSnapshotBodyLimit+80)
	cmd := newTopSnapshotEvent("evt-ai-cmd", huge, createdAt)
	fail := newTopSnapshotEvent("evt-ai-fail", huge, createdAt)
	node := newTopSnapshotSessionNode("sess-ai", "evt-latest", huge, createdAt)

	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Sessions:       []*sessionNode{node},
		Failures:       []*model.Event{fail},
		RecentCommands: []*model.Event{cmd},
		Now:            createdAt,
		Reliability: topReliabilityMetrics{
			CandidateMemoryCount: 12,
		},
	}, topSnapshotProfileAI); err != nil {
		t.Fatalf("writeTopSnapshotJSON(ai): %v", err)
	}

	var payload struct {
		Profile        string `json:"profile"`
		Sessions       []struct {
			LatestEventMessage string `json:"latest_event_message"`
			LatestEventID      string `json:"latest_event_id"`
		} `json:"sessions"`
		Failures []struct {
			Message   string `json:"message"`
			EventID   string `json:"event_id"`
			Truncated bool   `json:"truncated"`
		} `json:"failures"`
		RecentCommands []struct {
			Message string `json:"message"`
			EventID string `json:"event_id"`
		} `json:"recent_commands"`
		Candidates struct {
			Count int                      `json:"count"`
			Items []map[string]interface{} `json:"items"`
		} `json:"candidates"`
		StaleMemories struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"stale_memories"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	if payload.Profile != topSnapshotProfileAI {
		t.Fatalf("profile = %q, want %q", payload.Profile, topSnapshotProfileAI)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(payload.Sessions))
	}
	if !strings.Contains(payload.Sessions[0].LatestEventMessage, "traceary show ") {
		t.Fatalf("latest_event_message = %q, want retrieval hint", payload.Sessions[0].LatestEventMessage)
	}
	if strings.Contains(payload.Sessions[0].LatestEventMessage, "xxx") {
		t.Fatalf("latest_event_message still contains body: %q", payload.Sessions[0].LatestEventMessage)
	}
	if len(payload.Failures) != 1 || !strings.HasPrefix(payload.Failures[0].Message, "traceary show ") {
		t.Fatalf("failures = %+v, want retrieval hint", payload.Failures)
	}
	if len(payload.RecentCommands) != 1 || !strings.HasPrefix(payload.RecentCommands[0].Message, "traceary show ") {
		t.Fatalf("recent_commands = %+v, want retrieval hint", payload.RecentCommands)
	}
	if payload.Candidates.Count != 12 {
		t.Fatalf("candidates.count = %d, want 12 from reliability metrics", payload.Candidates.Count)
	}
	if len(payload.Candidates.Items) != 0 {
		t.Fatalf("candidates.items length = %d, want 0", len(payload.Candidates.Items))
	}
	if len(payload.StaleMemories.Items) != 0 {
		t.Fatalf("stale_memories.items length = %d, want 0", len(payload.StaleMemories.Items))
	}
	// AI payload must stay well under the huge body size.
	if len(buf.Bytes()) > 4000 {
		t.Fatalf("AI snapshot size = %d bytes, want bounded envelope", len(buf.Bytes()))
	}
}

func TestNormalizeTopSnapshotProfile(t *testing.T) {
	t.Parallel()
	got, err := normalizeTopSnapshotProfile("AI")
	if err != nil || got != topSnapshotProfileAI {
		t.Fatalf("normalize AI = %q, %v", got, err)
	}
	got, err = normalizeTopSnapshotProfile("")
	if err != nil || got != topSnapshotProfileOperator {
		t.Fatalf("normalize empty = %q, %v", got, err)
	}
	if _, err := normalizeTopSnapshotProfile("debug"); err == nil {
		t.Fatal("normalize debug error = nil, want error")
	}
}
