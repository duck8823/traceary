package cli

import (
	"context"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

type doctorAliasIdentityStub struct {
	report apptypes.WorkspaceIdentityReport
	limit  int
}

func (s *doctorAliasIdentityStub) Report(_ context.Context, limit int) (apptypes.WorkspaceIdentityReport, error) {
	s.limit = limit
	return s.report, nil
}
func (s *doctorAliasIdentityStub) AddAlias(context.Context, types.SessionID, types.Workspace, string, string) error {
	return nil
}
func (s *doctorAliasIdentityStub) RemoveAlias(context.Context, types.SessionID, types.Workspace) error {
	return nil
}

func TestInspectWorkspaceAliasesPassesWithConflictPairCounts(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &doctorAliasIdentityStub{report: apptypes.WorkspaceIdentityReport{
		ConflictPairCount: 2,
		ConflictSamples:   []apptypes.WorkspaceConflictSample{{SessionID: "session-1", Workspace: "/repo"}},
		Aliases:           []apptypes.WorkspaceAliasSummary{{SessionID: "session-0", Workspace: "/old"}},
	}}
	root := NewRootCLI(WithWorkspaceIdentity(stub))
	check := root.inspectWorkspaceAliases(context.Background(), nil)
	if stub.limit != doctorWorkspaceAliasConflictSampleLimit {
		t.Fatalf("Report limit = %d, want %d", stub.limit, doctorWorkspaceAliasConflictSampleLimit)
	}
	if check.Name != "workspace-aliases" || check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "conflict pairs=2") || !strings.Contains(check.Message, "session-1 /repo") {
		t.Fatalf("message = %q", check.Message)
	}
	if check.FixCommand != "" || check.AutoFixAvailable {
		t.Fatalf("FixCommand/auto = %q/%t, want empty guided-only hint", check.FixCommand, check.AutoFixAvailable)
	}
	if !strings.Contains(check.Hint, "doctor --alias-add") {
		t.Fatalf("hint = %q", check.Hint)
	}
}

func TestInspectWorkspaceAliasesPassesWithoutConflicts(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := NewRootCLI(WithWorkspaceIdentity(&doctorAliasIdentityStub{
		report: apptypes.WorkspaceIdentityReport{Aliases: []apptypes.WorkspaceAliasSummary{{SessionID: "s", Workspace: "/w"}}},
	}))
	check := root.inspectWorkspaceAliases(context.Background(), nil)
	if check.Status != doctorStatusPass || !strings.Contains(check.Message, "reviewed aliases=1") {
		t.Fatalf("check = %#v", check)
	}
}
