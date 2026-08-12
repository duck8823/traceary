package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchTextReportsSessionProjectionNotReadyOnlyForEmptyHits(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	ready := true
	notReady := false
	session := mustSessionHit(t, "session-old")
	cases := []struct {
		name           string
		args           []string
		stub           *projectionSessionSearchStub
		wantNotice     bool
		wantHit        bool
		wantOnlyNotice bool
	}{
		{name: "not ready and empty", stub: &projectionSessionSearchStub{ready: &notReady}, wantNotice: true},
		{name: "kind set, not ready and empty", stub: &projectionSessionSearchStub{ready: &notReady}, args: []string{"--kind", "note"}, wantNotice: true, wantOnlyNotice: true},
		{name: "ready and empty", stub: &projectionSessionSearchStub{ready: &ready}},
		{name: "ready and has hits", stub: &projectionSessionSearchStub{ready: &ready, hits: []apptypes.SearchSessionHit{session}}, wantHit: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"search", "--db-path", "/tmp/test-traceary.db"}, tc.args...)
			args = append(args, "needle")
			stdout, stderr := executeSearchWithSessionStub(t, args, &eventUsecaseStub{}, tc.stub)
			if tc.wantNotice {
				readyArgs := append([]string{"search", "--db-path", "/tmp/test-traceary.db"}, tc.args...)
				readyArgs = append(readyArgs, "needle")
				readyStdout, _ := executeSearchWithSessionStub(t, readyArgs, &eventUsecaseStub{}, &projectionSessionSearchStub{ready: &ready})
				if diff := cmp.Diff(readyStdout, stdout); diff != "" {
					t.Fatalf("not-ready stdout must match ready-empty stdout (-ready +not-ready):\n%s", diff)
				}
			}
			if got := strings.Contains(string(stderr), "session tier was not consulted"); got != tc.wantNotice {
				t.Fatalf("not-ready notice = %v, want %v; stderr = %q", got, tc.wantNotice, stderr)
			}
			// A refused tier makes the kind-less probe empty too, so --kind must
			// not also claim it suppressed anything. Counting the notices rather
			// than only checking for the wrong one catches a third notice slipping
			// in as well.
			if tc.wantOnlyNotice {
				if strings.Contains(string(stderr), "suppressed because --kind") {
					t.Fatalf("not-ready search must not emit kind-suppression notice: %q", stderr)
				}
				if got := strings.Count(string(stderr), "traceary:"); got != 1 {
					t.Fatalf("notice count = %d, want 1; stderr = %q", got, stderr)
				}
			}
			if tc.wantHit && !strings.Contains(string(stdout), "session-old") {
				t.Fatalf("session hit did not render: %q", stdout)
			}
		})
	}
}

func TestRootCLI_SearchJSONFieldsReportsSessionProjectionNotReadyOnlyForEmptyHits(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	ready := true
	notReady := false
	session := mustSessionHit(t, "session-old")
	cases := []struct {
		name       string
		stub       *projectionSessionSearchStub
		wantNotice bool
	}{
		{name: "not ready and empty", stub: &projectionSessionSearchStub{ready: &notReady}, wantNotice: true},
		{name: "ready and empty", stub: &projectionSessionSearchStub{ready: &ready}},
		{name: "ready and has hits", stub: &projectionSessionSearchStub{ready: &ready, hits: []apptypes.SearchSessionHit{session}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := executeSearchJSONFieldsWithSessionStub(t, tc.stub)
			readyStdout, _ := executeSearchJSONFieldsWithSessionStub(t, &projectionSessionSearchStub{ready: &ready})
			if diff := cmp.Diff(readyStdout, stdout); diff != "" {
				t.Fatalf("stdout must stay byte-identical (-ready-empty +got):\n%s", diff)
			}
			if got := strings.Contains(string(stderr), "session tier was not consulted"); got != tc.wantNotice {
				t.Fatalf("not-ready notice = %v, want %v; stderr = %q", got, tc.wantNotice, stderr)
			}
		})
	}
}

func TestRootCLI_SearchReadinessErrorReportsUnknownReadiness(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &projectionSessionSearchStub{readyErr: errors.New("readiness unavailable")}
	out, stderr := executeSearchWithSessionStub(t, []string{"search", "--db-path", "/tmp/test-traceary.db", "needle"}, &eventUsecaseStub{}, stub)
	ready := true
	want, _ := executeSearchWithSessionStub(t, []string{"search", "--db-path", "/tmp/test-traceary.db", "needle"}, &eventUsecaseStub{}, &projectionSessionSearchStub{ready: &ready})
	if diff := cmp.Diff(want, out); diff != "" {
		t.Fatalf("stdout changed on readiness error (-want +got):\n%s", diff)
	}
	if !strings.Contains(string(stderr), "could not determine whether the search projection is ready") {
		t.Fatalf("readiness error must report unknown readiness: %q", stderr)
	}
	if !strings.Contains(string(stderr), "traceary store search-projection status") {
		t.Fatalf("readiness error notice must point to status: %q", stderr)
	}
}

func executeSearchJSONFieldsWithSessionStub(t *testing.T, session *projectionSessionSearchStub) ([]byte, []byte) {
	t.Helper()
	metadata := &eventMetadataUsecaseStub{searchMetadata: []apptypes.EventMetadata{newCLIMetadataFixture(t, "event-search")}}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
		cli.WithEventMetadata(metadata),
		cli.WithProjectionSessionSearch(session),
	).Command()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs([]string{"search", "--db-path", "/tmp/test-traceary.db", "--json", "--fields", "id,kind", "needle"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.Bytes(), errOut.Bytes()
}
