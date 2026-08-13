package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchText_RecentOnlyMatchesPreSessionShape(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("NO_COLOR", "1")

	event := mustGoldenEvent(
		t,
		"evt-8f21",
		types.EventKindTranscript,
		"cli",
		"codex",
		"session-recent",
		"duck8823/traceary",
		"matching line about needle",
		time.Date(2026, 8, 6, 14, 2, 0, 0, time.UTC),
		"",
	)

	// Capture historical event-only rendering by omitting session search.
	withoutSessions := executeSearchText(t, &eventUsecaseStub{searchEvents: []*model.Event{event}}, nil)
	withEmptySessions := executeSearchText(
		t,
		&eventUsecaseStub{searchEvents: []*model.Event{event}},
		&projectionSessionSearchStub{hits: nil},
	)
	if diff := cmp.Diff(string(withoutSessions), string(withEmptySessions)); diff != "" {
		t.Fatalf("recent-only text must stay byte-identical when sessions empty (-without +with):\n%s", diff)
	}
	if strings.Contains(string(withEmptySessions), "EVENTS") || strings.Contains(string(withEmptySessions), "SESSIONS") {
		t.Fatalf("recent-only output unexpectedly labelled groups:\n%s", withEmptySessions)
	}
}

func TestRootCLI_SearchText_MixedShowsLabelledGroups(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("NO_COLOR", "1")

	event := mustGoldenEvent(
		t,
		"evt-8f21",
		types.EventKindTranscript,
		"cli",
		"codex",
		"session-recent",
		"duck8823/traceary",
		"matching line about needle",
		time.Date(2026, 8, 6, 14, 2, 0, 0, time.UTC),
		"",
	)
	session := apptypes.SearchSessionHitOf(
		types.SessionID("sess-4471"),
		"older summary excerpt for needle",
		5,
		time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	)
	stdout := executeSearchText(
		t,
		&eventUsecaseStub{searchEvents: []*model.Event{event}},
		&projectionSessionSearchStub{hits: []apptypes.SearchSessionHit{session}},
	)
	text := string(stdout)
	if !strings.Contains(text, "EVENTS (literal matches)") {
		t.Fatalf("missing EVENTS header:\n%s", text)
	}
	if !strings.Contains(text, "SESSIONS (summary or keyword matches)") {
		t.Fatalf("missing SESSIONS header:\n%s", text)
	}
	if !strings.Contains(text, "sess-4471") {
		t.Fatalf("missing session id:\n%s", text)
	}
	if !strings.Contains(text, "2026-06-19") {
		t.Fatalf("missing session date:\n%s", text)
	}
}

// `traceary search --json` always emits the events/sessions object. Both keys
// are present even when a tier has no hits, and session hits land in .sessions
// without a stderr omission notice.
func TestRootCLI_SearchJSON_EmitsEventsSessionsObject(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TRACEARY_LANG", "en")

	session := apptypes.SearchSessionHitOf(
		types.SessionID("sess-4471"),
		"older summary excerpt for needle",
		5,
		time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	)
	event := mustGoldenEvent(
		t,
		"evt-8f21",
		types.EventKindTranscript,
		"cli",
		"codex",
		"session-recent",
		"duck8823/traceary",
		"matching line about needle",
		time.Date(2026, 8, 6, 14, 2, 0, 0, time.UTC),
		"",
	)

	testCases := []struct {
		name         string
		events       []*model.Event
		hits         []apptypes.SearchSessionHit
		wantEvents   int
		wantSessions int
		wantSessID   string
	}{
		{
			name:         "empty hits keep both keys as empty arrays",
			events:       nil,
			hits:         nil,
			wantEvents:   0,
			wantSessions: 0,
		},
		{
			name:         "session hits appear under sessions",
			events:       nil,
			hits:         []apptypes.SearchSessionHit{session},
			wantEvents:   0,
			wantSessions: 1,
			wantSessID:   "sess-4471",
		},
		{
			name:         "event and session hits share the envelope",
			events:       []*model.Event{event},
			hits:         []apptypes.SearchSessionHit{session},
			wantEvents:   1,
			wantSessions: 1,
			wantSessID:   "sess-4471",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := executeSearchJSON(
				t,
				&eventUsecaseStub{searchEvents: tc.events},
				&projectionSessionSearchStub{hits: tc.hits},
			)
			if strings.Contains(string(stderr), "not included in --json") {
				t.Fatalf("session omission notice must be gone: %q", stderr)
			}
			var payload struct {
				Events   []map[string]any `json:"events"`
				Sessions []map[string]any `json:"sessions"`
			}
			if err := json.Unmarshal(stdout, &payload); err != nil {
				t.Fatalf("Unmarshal() error = %v\nstdout=%s", err, stdout)
			}
			if payload.Events == nil || payload.Sessions == nil {
				t.Fatalf("both events and sessions keys must be present: %#v", payload)
			}
			if got, want := len(payload.Events), tc.wantEvents; got != want {
				t.Fatalf("events len = %d, want %d; stdout=%s", got, want, stdout)
			}
			if got, want := len(payload.Sessions), tc.wantSessions; got != want {
				t.Fatalf("sessions len = %d, want %d; stdout=%s", got, want, stdout)
			}
			if tc.wantSessID != "" {
				if got, _ := payload.Sessions[0]["session_id"].(string); got != tc.wantSessID {
					t.Fatalf("session_id = %q, want %q", got, tc.wantSessID)
				}
				if _, ok := payload.Sessions[0]["summary"]; !ok {
					t.Fatalf("session object missing summary: %#v", payload.Sessions[0])
				}
				if _, ok := payload.Sessions[0]["event_count"]; !ok {
					t.Fatalf("session object missing event_count: %#v", payload.Sessions[0])
				}
				if _, ok := payload.Sessions[0]["started_at"]; !ok {
					t.Fatalf("session object missing started_at: %#v", payload.Sessions[0])
				}
			}
			// Top-level must be an object, not a bare array.
			trimmed := strings.TrimSpace(string(stdout))
			if strings.HasPrefix(trimmed, "[") {
				t.Fatalf("stdout must be an object envelope, got array:\n%s", stdout)
			}
		})
	}
}

func TestRootCLI_SearchJSONFields_SelectsEventFieldsInsideEnvelope(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TRACEARY_LANG", "en")

	session := apptypes.SearchSessionHitOf(
		types.SessionID("sess-4471"),
		"older summary excerpt for needle",
		5,
		time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	)
	metadata := &eventMetadataUsecaseStub{
		searchMetadata: []apptypes.EventMetadata{newCLIMetadataFixture(t, "event-search")},
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
		cli.WithEventMetadata(metadata),
		cli.WithProjectionSessionSearch(&projectionSessionSearchStub{hits: []apptypes.SearchSessionHit{session}}),
	).Command()
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{
		"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary",
		"--json", "--fields", "id,kind", "needle",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var payload struct {
		Events   []map[string]any `json:"events"`
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v\nstdout=%s", err, out.String())
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events len = %d, want 1; stdout=%s", len(payload.Events), out.String())
	}
	if _, ok := payload.Events[0]["event_id"]; !ok {
		t.Fatalf("events must keep selected id field: %#v", payload.Events[0])
	}
	if _, ok := payload.Events[0]["kind"]; !ok {
		t.Fatalf("events must keep selected kind field: %#v", payload.Events[0])
	}
	if _, ok := payload.Events[0]["message"]; ok {
		t.Fatalf("events must not include unselected message field: %#v", payload.Events[0])
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1; stdout=%s", len(payload.Sessions), out.String())
	}
	if got, _ := payload.Sessions[0]["session_id"].(string); got != "sess-4471" {
		t.Fatalf("session_id = %q, want sess-4471", got)
	}
	if _, ok := payload.Sessions[0]["summary"]; !ok {
		t.Fatalf("sessions keep their full object shape under --fields: %#v", payload.Sessions[0])
	}
	if strings.Contains(errOut.String(), "not included in --json") {
		t.Fatalf("session omission notice must be gone: %q", errOut.String())
	}
}

func executeSearchJSON(
	t *testing.T,
	eventStub *eventUsecaseStub,
	sessionSearch *projectionSessionSearchStub,
) (stdout []byte, stderr []byte) {
	t.Helper()
	options := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	}
	if sessionSearch != nil {
		options = append(options, cli.WithProjectionSessionSearch(sessionSearch))
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd := newTestRootCLI(options...).Command()
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{
		"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json", "needle",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.Bytes(), errOut.Bytes()
}

func executeSearchText(t *testing.T, eventStub *eventUsecaseStub, sessionSearch *projectionSessionSearchStub) []byte {
	t.Helper()
	options := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	}
	if sessionSearch != nil {
		options = append(options, cli.WithProjectionSessionSearch(sessionSearch))
	}
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(options...).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--utc", "needle",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return stdout.Bytes()
}
