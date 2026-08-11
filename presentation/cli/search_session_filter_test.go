package cli_test

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
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchKindSuppressionAnnouncement(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) { return "", nil })
	defer cli.ResetDetectRepoContextFunc()

	event := mustGoldenEvent(t, "evt-kind", types.EventKindNote, "cli", "codex", "session-kind", "", "needle", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), "")
	session := mustSessionHit(t, "session-old")

	testCases := []struct {
		name       string
		json       bool
		locale     string
		wantOutput string
		wantWarn   string
	}{
		{
			name:       "text",
			locale:     "en",
			wantOutput: "needle",
			wantWarn:   "--kind",
		},
		{
			name:       "json does not repeat the JSON shape notice",
			json:       true,
			locale:     "en",
			wantOutput: "evt-kind",
			wantWarn:   "--kind",
		},
		{
			name:       "Japanese",
			locale:     "ja",
			wantOutput: "needle",
			wantWarn:   "セッション要約",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRACEARY_LANG", tc.locale)
			stub := &projectionSessionSearchStub{hits: []apptypes.SearchSessionHit{session}}
			args := []string{"search", "--db-path", "/tmp/test-traceary.db", "--kind", "note", "needle"}
			if tc.json {
				args = append(args[:len(args)-1], "--json", args[len(args)-1])
			}
			stdout, stderr := executeSearchWithSessionStub(t, args, &eventUsecaseStub{searchEvents: []*model.Event{event}}, stub)
			if !strings.Contains(string(stdout), tc.wantOutput) {
				t.Fatalf("stdout = %q, want substring %q", stdout, tc.wantOutput)
			}
			if !strings.Contains(string(stderr), tc.wantWarn) {
				t.Fatalf("stderr = %q, want substring %q", stderr, tc.wantWarn)
			}
			if tc.json && strings.Contains(string(stderr), "v0.35") {
				t.Fatalf("kind suppression must not also emit the JSON omission notice: %q", stderr)
			}
			if diff := cmp.Diff(2, stub.calls); diff != "" {
				t.Fatalf("kind-filtered search must probe once (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_SearchKindSuppressionNoFalseAnnouncement(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &projectionSessionSearchStub{}
	_, stderr := executeSearchWithSessionStub(t, []string{"search", "--db-path", "/tmp/test-traceary.db", "--kind", "note", "needle"}, &eventUsecaseStub{}, stub)
	if diff := cmp.Diff("", string(stderr)); diff != "" {
		t.Fatalf("empty kind probe must be silent (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, stub.calls); diff != "" {
		t.Fatalf("kind-filtered search must probe once (-want +got):\n%s", diff)
	}
}

// The probe exists to answer "would these sessions have matched without
// --kind". If it drops any other filter, it announces sessions the user's own
// filters would have excluded. Assert it differs from the real criteria in
// exactly the kind field.
func TestRootCLI_SearchKindProbeKeepsEveryOtherFilter(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) { return "", nil })
	defer cli.ResetDetectRepoContextFunc()

	stub := &projectionSessionSearchStub{hits: []apptypes.SearchSessionHit{mustSessionHit(t, "session-old")}}
	_, _ = executeSearchWithSessionStub(t, []string{
		"search", "--db-path", "/tmp/test-traceary.db",
		"--kind", "note",
		"--workspace", "github.com/duck8823/traceary",
		"--session-id", "session-scoped",
		"--client", "cli",
		"--agent", "codex",
		"--from", "2026-01-01T00:00:00Z",
		"--to", "2026-02-01T00:00:00Z",
		"--limit", "7",
		"--failures",
		"needle",
	}, &eventUsecaseStub{}, stub)

	if diff := cmp.Diff(2, stub.calls); diff != "" {
		t.Fatalf("kind-filtered search must probe once (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(stub.criteria[0].WithoutKind(), stub.criteria[1], cmp.AllowUnexported(apptypes.EventSearchCriteria{}, apptypes.EventPageAnchor{})); diff != "" {
		t.Fatalf("probe criteria must differ only in kind (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", stub.criteria[1].Kind().String()); diff != "" {
		t.Fatalf("probe criteria kind (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SearchWithoutKindDoesNotProbe(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &projectionSessionSearchStub{}
	_, _ = executeSearchWithSessionStub(t, []string{"search", "--db-path", "/tmp/test-traceary.db", "needle"}, &eventUsecaseStub{}, stub)
	if diff := cmp.Diff(1, stub.calls); diff != "" {
		t.Fatalf("search without kind must query sessions once (-want +got):\n%s", diff)
	}
}

func executeSearchWithSessionStub(t *testing.T, args []string, event *eventUsecaseStub, session *projectionSessionSearchStub) ([]byte, []byte) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(event),
		cli.WithProjectionSessionSearch(session),
	).Command()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.Bytes(), errOut.Bytes()
}

func mustSessionHit(t *testing.T, id string) apptypes.SearchSessionHit {
	t.Helper()
	return apptypes.SearchSessionHitOf(types.SessionID(id), "older needle summary", 2, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}
