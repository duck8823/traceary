package cli_test

import (
	"bytes"
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
	if !strings.Contains(text, "EVENTS (recent, full text)") {
		t.Fatalf("missing EVENTS header:\n%s", text)
	}
	if !strings.Contains(text, "SESSIONS (older, from summaries)") {
		t.Fatalf("missing SESSIONS header:\n%s", text)
	}
	if !strings.Contains(text, "sess-4471") {
		t.Fatalf("missing session id:\n%s", text)
	}
	if !strings.Contains(text, "2026-06-19") {
		t.Fatalf("missing session date:\n%s", text)
	}
}

// `traceary search` is Public, so v0.34 keeps the top-level JSON array. Session
// hits therefore cannot appear in stdout; the operator must still be told they
// exist, otherwise an empty array is indistinguishable from "no results".
func TestRootCLI_SearchJSON_KeepsArrayAndWarnsAboutOmittedSessions(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("NO_COLOR", "1")

	session := apptypes.SearchSessionHitOf(
		types.SessionID("sess-4471"),
		"older summary excerpt for needle",
		5,
		time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
	)

	testCases := []struct {
		name      string
		hits      []apptypes.SearchSessionHit
		wantStdes string
		wantWarn  bool
	}{
		{
			name:      "session hits are announced on stderr",
			hits:      []apptypes.SearchSessionHit{session},
			wantStdes: "[]\n",
			wantWarn:  true,
		},
		{
			name:      "no session hits stays silent",
			hits:      nil,
			wantStdes: "[]\n",
			wantWarn:  false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := executeSearchJSON(t, &eventUsecaseStub{}, &projectionSessionSearchStub{hits: tc.hits})
			if diff := cmp.Diff(tc.wantStdes, string(stdout)); diff != "" {
				t.Fatalf("stdout must stay the historical array (-want +got):\n%s", diff)
			}
			warned := strings.Contains(string(stderr), "sess") ||
				strings.Contains(string(stderr), "--json")
			if warned != tc.wantWarn {
				t.Fatalf("stderr warning = %v, want %v; stderr = %q", warned, tc.wantWarn, stderr)
			}
			if tc.wantWarn && !strings.Contains(string(stderr), "v0.35") {
				t.Fatalf("stderr must announce the v0.35 shape change: %q", stderr)
			}
		})
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
