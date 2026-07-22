package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type workspaceIdentityUsecaseStub struct {
	report  apptypes.WorkspaceIdentityReport
	limit   int
	added   [4]string
	removed [2]string
}

func (s *workspaceIdentityUsecaseStub) Report(_ context.Context, limit int) (apptypes.WorkspaceIdentityReport, error) {
	s.limit = limit
	return s.report, nil
}
func (s *workspaceIdentityUsecaseStub) AddAlias(_ context.Context, sessionID types.SessionID, workspace types.Workspace, reviewedBy, note string) error {
	s.added = [4]string{sessionID.String(), workspace.String(), reviewedBy, note}
	return nil
}
func (s *workspaceIdentityUsecaseStub) RemoveAlias(_ context.Context, sessionID types.SessionID, workspace types.Workspace) error {
	s.removed = [2]string{sessionID.String(), workspace.String()}
	return nil
}

func TestRootCLI_WorkspaceIdentityReportSeparatesExactAndHeuristicRates(t *testing.T) {
	t.Parallel()
	identity := &workspaceIdentityUsecaseStub{report: apptypes.WorkspaceIdentityReport{
		Coverage: apptypes.WorkspaceIdentityCoverage{EventCount: 2, CoveredEvents: 2, CoverageRate: 1},
		Sources:  []apptypes.WorkspaceIdentitySourceReport{{Client: "codex", SourceHook: "user_prompt_submit", DeliveryAttemptCount: 200, RuntimeAttemptCount: 200, ExactRedeliveryCount: 1}},
	}}
	store := &storeManagementUsecaseStub{dedupeResult: apptypes.ContentEventDedupeResult{
		ScannedCount: 10,
		Groups:       []apptypes.ContentEventDedupeGroup{{DuplicateEventIDs: []string{"candidate-1"}}},
		Sources:      []apptypes.ContentEventDedupeSourceStat{{Agent: "codex", SourceHook: "user_prompt_submit", ScannedCount: 10, CandidateCount: 1, CandidateRate: 0.1}},
	}}
	root := cli.NewRootCLI(cli.WithStoreManagement(store), cli.WithWorkspaceIdentity(identity)).Command()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := os.WriteFile(dbPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	root.SetArgs([]string{"report", "workspace-identity", "--db-path", dbPath, "--conflict-sample-limit", "3", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, output.String())
	}
	for _, want := range []string{`"sample_available": true`, `"target_met": true`, `"candidate_rate": 0.1`, `"heuristic_candidates"`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
	if identity.limit != 3 || len(store.dedupeParams) != 1 || store.dedupeParams[0].Apply {
		t.Fatalf("limit/dedupe params = %d/%#v", identity.limit, store.dedupeParams)
	}
	if store.initCalled {
		t.Fatal("read-only report called store Initialize")
	}
}

func TestRootCLI_WorkspaceIdentityReportDoesNotTreatBackfillAsLiveSample(t *testing.T) {
	t.Parallel()
	identity := &workspaceIdentityUsecaseStub{report: apptypes.WorkspaceIdentityReport{
		Sources: []apptypes.WorkspaceIdentitySourceReport{{Client: "codex", DeliveryAttemptCount: 200, BackfilledAttemptCount: 200}},
	}}
	store := &storeManagementUsecaseStub{}
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := os.WriteFile(dbPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	root := cli.NewRootCLI(cli.WithStoreManagement(store), cli.WithWorkspaceIdentity(identity)).Command()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"report", "workspace-identity", "--db-path", dbPath, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, output.String())
	}
	for _, want := range []string{`"attempt_count": 0`, `"sample_available": false`, `"target_met": false`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
}

func TestRootCLI_WorkspaceAliasAddAndRemove(t *testing.T) {
	t.Parallel()
	identity := &workspaceIdentityUsecaseStub{}
	store := &storeManagementUsecaseStub{}
	for _, args := range [][]string{
		{"store", "workspace-alias", "add", "--db-path", t.TempDir() + "/traceary.db", "--session", "session-1", "--workspace", "/repo", "--reviewed-by", "operator", "--note", "reviewed"},
		{"store", "workspace-alias", "remove", "--db-path", t.TempDir() + "/traceary.db", "--session", "session-1", "--workspace", "/repo"},
	} {
		root := cli.NewRootCLI(cli.WithStoreManagement(store), cli.WithWorkspaceIdentity(identity)).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}
	if identity.added != [4]string{"session-1", "/repo", "operator", "reviewed"} || identity.removed != [2]string{"session-1", "/repo"} {
		t.Fatalf("added/removed = %#v/%#v", identity.added, identity.removed)
	}
}
