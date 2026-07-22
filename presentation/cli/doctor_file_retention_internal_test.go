package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

type fileRetentionCapacityInspectorStub struct {
	statuses []apptypes.FileRetentionCapacityStatus
	err      error
	request  apptypes.FileRetentionCapacityRequest
	calls    int
}

func (stub *fileRetentionCapacityInspectorStub) InspectCapacity(_ context.Context, request apptypes.FileRetentionCapacityRequest) ([]apptypes.FileRetentionCapacityStatus, error) {
	stub.calls++
	stub.request = request
	return stub.statuses, stub.err
}

func TestInspectFileRetentionCapacityIsExplicitAndReadOnly(t *testing.T) {
	t.Parallel()

	stub := &fileRetentionCapacityInspectorStub{statuses: []apptypes.FileRetentionCapacityStatus{{
		Class: "backup", Root: "/tmp/backups", State: "ready", FileCount: 2, VerifiedCount: 2,
		LogicalBytes: 20, AllocatedBytes: 32, AllocatedKnown: true, FloorRelativePath: "new.db",
		RootAccess: apptypes.FileRetentionRootAccessEvidence{ApplyState: apptypes.FileRetentionRootApplyEligible, CallerOwned: true},
	}}}
	rootCLI := NewRootCLI(WithFileRetentionCapacityInspector(stub))
	if checks := rootCLI.inspectFileRetentionCapacity(context.Background(), "/tmp/live.db", "", ""); len(checks) != 0 || stub.calls != 0 {
		t.Fatalf("implicit checks/calls = %#v/%d, want none", checks, stub.calls)
	}
	checks := rootCLI.inspectFileRetentionCapacity(context.Background(), "/tmp/live.db", "", t.TempDir())
	if len(checks) != 1 || checks[0].Name != "backup-capacity-retention" || checks[0].Status != doctorStatusPass {
		t.Fatalf("checks = %#v", checks)
	}
	if stub.calls != 1 || len(stub.request.Classes) != 1 || stub.request.Classes[0].Class != "backup" {
		t.Fatalf("status request/calls = %#v/%d", stub.request, stub.calls)
	}
	for _, evidence := range []string{"state=ready", "apply_root=eligible", "caller_owned=true", "group_or_other_writable=false", "files=2", "logical_bytes=20", "allocated_bytes=32", "floor=new.db", "automatic cleanup is disabled"} {
		if !strings.Contains(checks[0].Message, evidence) {
			t.Fatalf("message %q missing %q", checks[0].Message, evidence)
		}
	}
}

func TestDoctorAndStatusCapacityRootsUseOnlyReadOnlyInspector(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("HOME", t.TempDir())
	projectDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "live.db")
	rootPath := filepath.Join(t.TempDir(), "backup root 'quoted'")

	for _, commandName := range []string{"doctor", "status"} {
		t.Run(commandName, func(t *testing.T) {
			stub := &fileRetentionCapacityInspectorStub{statuses: []apptypes.FileRetentionCapacityStatus{{
				Class: "backup", Root: rootPath, State: "ready", FileCount: 1, VerifiedCount: 1,
				LogicalBytes: 10, AllocatedBytes: 4096, AllocatedKnown: true, FloorRelativePath: "new\n.db",
				RootAccess: apptypes.FileRetentionRootAccessEvidence{ApplyState: apptypes.FileRetentionRootApplyEligible, CallerOwned: true},
			}}}
			root := newFileRetentionDoctorTestCLI(WithStoreManagement(&retentionStoreStub{}), WithFileRetentionCapacityInspector(stub)).Command()
			stdout := &bytes.Buffer{}
			root.SetOut(stdout)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs([]string{commandName, "--db-path", dbPath, "--backup-root", rootPath, "--client", "codex", "--project-dir", projectDir, "--json", "--warnings-ok", "--fix", "--dry-run"})
			if err := root.Execute(); err != nil && !strings.Contains(err.Error(), "doctor found") {
				t.Fatalf("Execute(%s) error = %v", commandName, err)
			}
			var report doctorReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("Unmarshal(%s report) error = %v\n%s", commandName, err, stdout.String())
			}
			check, ok := doctorCheckByName(report.Checks, "backup-capacity-retention")
			if !ok || check.Status != doctorStatusPass || check.Section != "Database" {
				t.Fatalf("%s capacity check = %#v, exists=%v", commandName, check, ok)
			}
			if strings.Contains(check.Message, "\n") || !strings.Contains(check.Message, "floor=new .db") {
				t.Fatalf("%s capacity message is not single-line safe: %q", commandName, check.Message)
			}
			if !strings.Contains(check.Hint, "backup root '\"'\"'quoted'\"'\"''") {
				t.Fatalf("%s capacity hint is not shell-quoted: %q", commandName, check.Hint)
			}
			if stub.calls != 2 {
				t.Fatalf("%s InspectCapacity calls = %d, want 2 before/after --fix dry-run", commandName, stub.calls)
			}
		})
	}
}

func TestDoctorCapacityInventoryFailureIsWarningWithoutMutationCapability(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("HOME", t.TempDir())
	stub := &fileRetentionCapacityInspectorStub{err: errors.New("injected inventory failure")}
	root := newFileRetentionDoctorTestCLI(WithStoreManagement(&retentionStoreStub{}), WithFileRetentionCapacityInspector(stub)).Command()
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"doctor", "--db-path", filepath.Join(t.TempDir(), "live.db"), "--archive-root", t.TempDir(), "--client", "codex", "--project-dir", t.TempDir(), "--json"})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute(doctor inventory failure) error = nil")
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal(report) error = %v\n%s", err, stdout.String())
	}
	check, ok := doctorCheckByName(report.Checks, "archive-capacity-retention")
	if !ok || check.Status != doctorStatusWarn || !strings.Contains(check.Message, "injected inventory failure") {
		t.Fatalf("capacity failure check = %#v, exists=%v", check, ok)
	}
	if stub.calls != 1 {
		t.Fatalf("InspectCapacity calls = %d, want 1", stub.calls)
	}
}

func TestDoctorWithoutCapacityRootsSkipsReadOnlyInspector(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("HOME", t.TempDir())
	stub := &fileRetentionCapacityInspectorStub{err: errors.New("must not be called")}
	root := newFileRetentionDoctorTestCLI(WithStoreManagement(&retentionStoreStub{}), WithFileRetentionCapacityInspector(stub)).Command()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"doctor", "--db-path", filepath.Join(t.TempDir(), "live.db"), "--client", "codex", "--project-dir", t.TempDir(), "--json", "--warnings-ok"})
	if err := root.Execute(); err != nil && !strings.Contains(err.Error(), "doctor found") {
		t.Fatalf("Execute(doctor without capacity roots) error = %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("InspectCapacity calls = %d, want 0", stub.calls)
	}
}

func doctorCheckByName(checks []doctorCheck, name string) (doctorCheck, bool) {
	for _, check := range checks {
		if check.Name == name {
			return check, true
		}
	}
	return doctorCheck{}, false
}

func newFileRetentionDoctorTestCLI(options ...RootCLIOption) *RootCLI {
	homeDirFunc := func() (string, error) { return CallUserHomeDirFunc() }
	base := []RootCLIOption{
		WithHooksOrchestrator(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
			"claude": filesystem.NewClaudeHooksHandler(),
			"codex":  filesystem.NewCodexHooksHandlerWithHomeDirFunc(homeDirFunc),
			"gemini": filesystem.NewGeminiHooksHandler(),
		})),
		WithHooksInspector(filesystem.NewHooksInspector()),
		WithPluginCacheInspector(filesystem.NewPluginCacheInspector()),
		WithClaudePluginDetector(filesystem.NewClaudePluginDetectorAdapter()),
	}
	return NewRootCLI(append(base, options...)...)
}

func TestFileRetentionCapacityDoctorCheckWarnsOnUncertainEvidence(t *testing.T) {
	t.Parallel()

	check := fileRetentionCapacityDoctorCheck("archive-capacity-retention", apptypes.FileRetentionCapacityStatus{
		Class: "archive", Root: "/tmp/archives", State: "indeterminate", FileCount: 3,
		VerifiedCount: 1, UnverifiedCount: 1, BlockingCount: 1, LogicalBytes: 30,
		AllocatedBytes: 16, AllocatedKnown: false,
	})
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "allocated_bytes=partially-known:16") || !strings.Contains(check.Message, "floor=none") {
		t.Fatalf("check = %#v", check)
	}
	if section := doctorSectionNameForCheck(check.Name); section != "Database" {
		t.Fatalf("section = %q, want Database", section)
	}
}

func TestFileRetentionCapacityDoctorCheckWarnsOnOverflow(t *testing.T) {
	t.Parallel()

	check := fileRetentionCapacityDoctorCheck("backup-capacity-retention", apptypes.FileRetentionCapacityStatus{
		Class: "backup", Root: "/tmp/backups", State: "indeterminate", FileCount: 2, VerifiedCount: 2,
		LogicalOverflow: true, AllocatedOverflow: true, AllocatedKnown: true, FloorRelativePath: "new.db",
	})
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "logical_bytes=overflow") || !strings.Contains(check.Message, "allocated_bytes=overflow") {
		t.Fatalf("check = %#v", check)
	}
}

func TestFileRetentionCapacityDoctorCheckWarnsWhenApplyRootIsUnsafeOrUnsupported(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		rootAccess apptypes.FileRetentionRootAccessEvidence
		want       string
	}{
		{
			name: "group writable",
			rootAccess: apptypes.FileRetentionRootAccessEvidence{
				ApplyState: apptypes.FileRetentionRootApplyUnsafePermissions, CallerOwned: true, GroupOrOtherWritable: true,
			},
			want: "apply_root=unsafe_permissions",
		},
		{
			name:       "unknown defaults to unsupported",
			rootAccess: apptypes.FileRetentionRootAccessEvidence{},
			want:       "apply_root=unsupported",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			check := fileRetentionCapacityDoctorCheck("backup-capacity-retention", apptypes.FileRetentionCapacityStatus{
				Class: "backup", Root: "/tmp/backups", State: "ready", FileCount: 1, VerifiedCount: 1,
				LogicalBytes: 10, AllocatedBytes: 4096, AllocatedKnown: true, FloorRelativePath: "new.db", RootAccess: test.rootAccess,
			})
			if check.Status != doctorStatusWarn || !strings.Contains(check.Message, test.want) {
				t.Fatalf("check = %#v", check)
			}
		})
	}
}
