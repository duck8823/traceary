package cli

import (
	"context"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestInspectFileRetentionCapacityIsExplicitAndReadOnly(t *testing.T) {
	t.Parallel()

	stub := &fileRetentionUsecaseStub{statuses: []apptypes.FileRetentionCapacityStatus{{
		Class: "backup", Root: "/tmp/backups", State: "ready", FileCount: 2, VerifiedCount: 2,
		LogicalBytes: 20, AllocatedBytes: 32, AllocatedKnown: true, FloorRelativePath: "new.db",
	}}}
	rootCLI := NewRootCLI(WithFileRetention(stub))
	if checks := rootCLI.inspectFileRetentionCapacity(context.Background(), "/tmp/live.db", "", ""); len(checks) != 0 || stub.statusCalls != 0 {
		t.Fatalf("implicit checks/calls = %#v/%d, want none", checks, stub.statusCalls)
	}
	checks := rootCLI.inspectFileRetentionCapacity(context.Background(), "/tmp/live.db", "", t.TempDir())
	if len(checks) != 1 || checks[0].Name != "backup-capacity-retention" || checks[0].Status != doctorStatusPass {
		t.Fatalf("checks = %#v", checks)
	}
	if stub.statusCalls != 1 || len(stub.statusReq.Classes) != 1 || stub.statusReq.Classes[0].Class != "backup" {
		t.Fatalf("status request/calls = %#v/%d", stub.statusReq, stub.statusCalls)
	}
	for _, evidence := range []string{"state=ready", "files=2", "logical_bytes=20", "allocated_bytes=32", "floor=new.db", "automatic cleanup is disabled"} {
		if !strings.Contains(checks[0].Message, evidence) {
			t.Fatalf("message %q missing %q", checks[0].Message, evidence)
		}
	}
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
