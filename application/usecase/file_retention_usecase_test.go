package usecase_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestFileRetentionCreatePlanProtectsNewestVerifiedRecoveryPoint(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	inventory := &fileRetentionInventoryStub{snapshot: fileRetentionSnapshot(now)}
	executor := &fileRetentionExecutorStub{}
	workflow := usecase.NewFileRetentionUsecase(inventory, executor)
	maxCount := 1
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: "/tmp/live.db", ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "backup", Root: "/tmp/backups", Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	var plan apptypes.FileRetentionPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	classPlan := plan.CanonicalPayload.Classes[0]
	if classPlan.Status != "satisfied" || classPlan.Floor == nil || classPlan.Floor.Identity != digestOf('b') {
		t.Fatalf("class plan floor/status = %#v, want newest verified floor", classPlan)
	}
	if len(classPlan.Candidates) != 1 || classPlan.Candidates[0].Identity != digestOf('a') || len(classPlan.Batches) != 1 {
		t.Fatalf("candidates/batches = %#v/%#v, want older backup only", classPlan.Candidates, classPlan.Batches)
	}
	if inventory.calls != 1 {
		t.Fatalf("InspectFileRetention() calls = %d, want 1", inventory.calls)
	}

	result, err := workflow.Apply(context.Background(), planBytes, plan.PlanID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.PlanID != plan.PlanID || executor.calls != 1 {
		t.Fatalf("Apply() result/calls = %#v/%d", result, executor.calls)
	}
}

func TestFileRetentionApplyRejectsTamperAndExpiryBeforeExecutor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	executor := &fileRetentionExecutorStub{}
	workflow := usecase.NewFileRetentionUsecase(&fileRetentionInventoryStub{snapshot: fileRetentionSnapshot(now)}, executor)
	maxCount := 1
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: "/tmp/live.db", ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "backup", Root: "/tmp/backups", Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	var plan apptypes.FileRetentionPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	plan.CanonicalPayload.Classes[0].Candidates[0].RelativePath = "replacement.db"
	tampered, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := workflow.Apply(context.Background(), tampered, plan.PlanID, now); err == nil {
		t.Fatal("Apply(tampered) error = nil")
	}
	if _, err := workflow.Apply(context.Background(), planBytes, plan.PlanID, now.Add(2*time.Hour)); err == nil {
		t.Fatal("Apply(expired) error = nil")
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
}

func TestFileRetentionPlanWithoutCurrentGenerationFloorIsReportOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	snapshot := fileRetentionSnapshot(now)
	snapshot.Entries[0].Generation = digestOf('a')
	snapshot.Entries[1].Generation = digestOf('a')
	workflow := usecase.NewFileRetentionUsecase(&fileRetentionInventoryStub{snapshot: snapshot}, &fileRetentionExecutorStub{})
	maxCount := 1
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: "/tmp/live.db", ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "backup", Root: "/tmp/backups", Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	var plan apptypes.FileRetentionPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	classPlan := plan.CanonicalPayload.Classes[0]
	if classPlan.Status != "indeterminate" || classPlan.Floor != nil || len(classPlan.Candidates) != 0 {
		t.Fatalf("class plan = %#v, want report-only", classPlan)
	}
}

func TestFileRetentionInspectCapacitySummarizesReadOnlyEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	snapshot := fileRetentionSnapshot(now)
	snapshot.Entries[0].AllocatedKnown = false
	snapshot.Entries[0].AllocatedBytes = 0
	snapshot.Entries[0].Verified = false
	workflow := usecase.NewFileRetentionUsecase(&fileRetentionInventoryStub{snapshot: snapshot}, &fileRetentionExecutorStub{})
	statuses, err := workflow.InspectCapacity(context.Background(), apptypes.FileRetentionCapacityRequest{
		DatabasePath: "/tmp/live.db",
		Classes:      []apptypes.FileRetentionInventoryRequest{{Class: "backup", Root: "/tmp/backups"}},
	})
	if err != nil {
		t.Fatalf("InspectCapacity() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v, want one", statuses)
	}
	status := statuses[0]
	if status.State != "indeterminate" || status.FileCount != 2 || status.VerifiedCount != 1 || status.UnverifiedCount != 1 || status.BlockingCount != 0 {
		t.Fatalf("status counts/readiness = %#v", status)
	}
	if status.LogicalBytes != 20 || status.AllocatedBytes != 10 || status.AllocatedKnown || status.FloorRelativePath != "b.db" {
		t.Fatalf("status capacity/floor = %#v", status)
	}
}

func TestFileRetentionInspectCapacityReportsEmptyAndIndeterminate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		snapshot  apptypes.FileRetentionInventorySnapshot
		wantState string
	}{
		{name: "empty", snapshot: apptypes.FileRetentionInventorySnapshot{Class: "archive", Root: "/tmp/archives", LiveGeneration: digestOf('d')}, wantState: "empty"},
		{name: "missing floor", snapshot: func() apptypes.FileRetentionInventorySnapshot {
			snapshot := fileRetentionSnapshot(now)
			snapshot.Entries[0].Generation = digestOf('x')
			snapshot.Entries[1].Generation = digestOf('x')
			return snapshot
		}(), wantState: "indeterminate"},
		{name: "blocker", snapshot: func() apptypes.FileRetentionInventorySnapshot {
			snapshot := fileRetentionSnapshot(now)
			snapshot.Entries[0].BlockingReason = "hard_link"
			return snapshot
		}(), wantState: "indeterminate"},
		{name: "logical overflow", snapshot: func() apptypes.FileRetentionInventorySnapshot {
			snapshot := fileRetentionSnapshot(now)
			snapshot.Entries[0].LogicalBytes = math.MaxInt64
			return snapshot
		}(), wantState: "indeterminate"},
		{name: "allocated overflow", snapshot: func() apptypes.FileRetentionInventorySnapshot {
			snapshot := fileRetentionSnapshot(now)
			snapshot.Entries[0].AllocatedBytes = math.MaxInt64
			return snapshot
		}(), wantState: "indeterminate"},
		{name: "unsupported root evidence remains visible", snapshot: func() apptypes.FileRetentionInventorySnapshot {
			snapshot := fileRetentionSnapshot(now)
			snapshot.RootAccess = apptypes.FileRetentionRootAccessEvidence{ApplyState: apptypes.FileRetentionRootApplyUnsupported}
			return snapshot
		}(), wantState: "ready"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workflow := usecase.NewFileRetentionUsecase(&fileRetentionInventoryStub{snapshot: test.snapshot}, &fileRetentionExecutorStub{})
			statuses, err := workflow.InspectCapacity(context.Background(), apptypes.FileRetentionCapacityRequest{
				DatabasePath: "/tmp/live.db", Classes: []apptypes.FileRetentionInventoryRequest{{Class: test.snapshot.Class, Root: test.snapshot.Root}},
			})
			if err != nil {
				t.Fatalf("InspectCapacity() error = %v", err)
			}
			if len(statuses) != 1 || statuses[0].State != test.wantState {
				t.Fatalf("statuses = %#v, want state %s", statuses, test.wantState)
			}
			if test.name == "logical overflow" && !statuses[0].LogicalOverflow {
				t.Fatalf("status = %#v, want logical overflow", statuses[0])
			}
			if test.name == "allocated overflow" && !statuses[0].AllocatedOverflow {
				t.Fatalf("status = %#v, want allocated overflow", statuses[0])
			}
			if test.name == "unsupported root evidence remains visible" && statuses[0].RootAccess.ApplyState != apptypes.FileRetentionRootApplyUnsupported {
				t.Fatalf("status = %#v, want unsupported root evidence", statuses[0])
			}
		})
	}
}

type fileRetentionInventoryStub struct {
	snapshot apptypes.FileRetentionInventorySnapshot
	calls    int
}

func (stub *fileRetentionInventoryStub) InspectFileRetention(context.Context, apptypes.FileRetentionInventoryRequest) (apptypes.FileRetentionInventorySnapshot, error) {
	stub.calls++
	return stub.snapshot, nil
}

type fileRetentionExecutorStub struct {
	calls int
}

func (stub *fileRetentionExecutorStub) ApplyFileRetention(_ context.Context, plan apptypes.FileRetentionPlan, _ string, _ time.Time) (apptypes.FileRetentionApplyResult, error) {
	stub.calls++
	return apptypes.FileRetentionApplyResult{PlanID: plan.PlanID, CandidateCount: len(plan.CanonicalPayload.Classes[0].Candidates)}, nil
}

func fileRetentionSnapshot(now time.Time) apptypes.FileRetentionInventorySnapshot {
	return apptypes.FileRetentionInventorySnapshot{
		Class: "backup", Root: "/tmp/backups", RootIdentity: digestOf('c'), LiveGeneration: digestOf('d'),
		RootAccess: apptypes.FileRetentionRootAccessEvidence{ApplyState: apptypes.FileRetentionRootApplyEligible, CallerOwned: true},
		Entries: []apptypes.FileRetentionInventoryEntry{
			{Identity: digestOf('a'), RelativePath: "a.db", Device: 1, Inode: 1, LinkCount: 1, LogicalBytes: 10, AllocatedBytes: 10, AllocatedKnown: true, ModifiedAt: now.Add(-2 * time.Hour), GenerationCreatedAt: now.Add(-2 * time.Hour), GenerationProvenance: "catalog", Generation: digestOf('d'), ContentSHA256: digestOf('1'), Verified: true, VerificationDigest: digestOf('e'), MetadataRelativePath: apptypes.BackupRetentionManifestName("a.db"), MetadataSHA256: digestOf('3')},
			{Identity: digestOf('b'), RelativePath: "b.db", Device: 1, Inode: 2, LinkCount: 1, LogicalBytes: 10, AllocatedBytes: 10, AllocatedKnown: true, ModifiedAt: now.Add(-time.Hour), GenerationCreatedAt: now.Add(-time.Hour), GenerationProvenance: "catalog", Generation: digestOf('d'), ContentSHA256: digestOf('2'), Verified: true, VerificationDigest: digestOf('f'), MetadataRelativePath: apptypes.BackupRetentionManifestName("b.db"), MetadataSHA256: digestOf('4')},
		},
	}
}

func digestOf(value byte) string {
	result := make([]byte, 64)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
