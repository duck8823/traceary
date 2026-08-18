package cli_test

import (
	"bytes"
	"context"
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

func TestRootCLI_ReportWorkspaceIdentityIsUnknownSubcommand(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		args       []string
		wantErrHas string
	}{
		{name: "bare leaf", args: []string{"report", "workspace-identity"}, wantErrHas: `unknown subcommand "workspace-identity"`},
		{name: "json flag", args: []string{"report", "workspace-identity", "--json"}, wantErrHas: `unknown subcommand "workspace-identity"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want %q", tt.args, tt.wantErrHas)
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("Execute(%v) error = %q, want substring %q", tt.args, err.Error(), tt.wantErrHas)
			}
			if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice:\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRootCLI_WorkspaceAliasAddAndRemove(t *testing.T) {
	identity := &workspaceIdentityUsecaseStub{}
	store := &storeManagementUsecaseStub{}
	for _, args := range [][]string{
		{"doctor", "--alias-add", "--db-path", t.TempDir() + "/traceary.db", "--session", "session-1", "--workspace", "/repo", "--reviewed-by", "operator", "--note", "reviewed"},
		{"doctor", "--alias-remove", "--db-path", t.TempDir() + "/traceary.db", "--session", "session-1", "--workspace", "/repo"},
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

func TestRootCLI_DoctorAliasListDispatchesToWorkspaceIdentity(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	identity := &workspaceIdentityUsecaseStub{report: apptypes.WorkspaceIdentityReport{
		Aliases: []apptypes.WorkspaceAliasSummary{{SessionID: "session-1", Workspace: "/repo", ReviewedBy: "operator"}},
	}}
	root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithWorkspaceIdentity(identity)).Command()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"doctor", "--alias-list", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if identity.limit != 0 {
		t.Fatalf("Report limit = %d, want 0", identity.limit)
	}
	if !strings.Contains(stdout.String(), "session_id=session-1") || !strings.Contains(stdout.String(), "workspace=/repo") {
		t.Fatalf("stdout = %q, want listed alias", stdout.String())
	}
}

func TestRootCLI_DoctorRejectsAliasFlagsWithoutMode(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"doctor", "--session", "session-1", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want alias flag pairing error")
	}
	if !strings.Contains(err.Error(), "--session/--workspace/--reviewed-by/--note require --alias-add or --alias-remove") {
		t.Fatalf("error = %q", err.Error())
	}
}
