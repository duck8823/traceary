package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestRunStoreFileRetentionPlanWritesExclusivePlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "plan.json")
	stub := &fileRetentionUsecaseStub{plan: []byte(`{"plan_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}
	cli := NewRootCLI(WithFileRetention(stub))
	var output bytes.Buffer
	err := cli.runStoreFileRetentionPlan(context.Background(), &output, storeFileRetentionPlanInput{
		dbPath: root + "/live.db", backupRoot: root, backupMaxCount: 1,
		archiveMaxCount: -1, archiveMaxBytes: -1, backupMaxBytes: -1,
		expiresAfter: time.Hour, outputPath: outputPath,
	})
	if err != nil {
		t.Fatalf("runStoreFileRetentionPlan() error = %v", err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile(plan) error = %v", err)
	}
	if !bytes.Equal(data, stub.plan) || stub.createCalls != 1 {
		t.Fatalf("plan/calls = %s/%d", data, stub.createCalls)
	}
	if stub.request.Classes[0].Class != "backup" || stub.request.Classes[0].Budget.MaxCount == nil || *stub.request.Classes[0].Budget.MaxCount != 1 {
		t.Fatalf("request = %#v", stub.request)
	}
	if output.String() == "" {
		t.Fatal("plan output is empty")
	}

	if err := cli.runStoreFileRetentionPlan(context.Background(), &output, storeFileRetentionPlanInput{
		dbPath: root + "/live.db", backupRoot: root, backupMaxCount: 1,
		archiveMaxCount: -1, archiveMaxBytes: -1, backupMaxBytes: -1,
		expiresAfter: time.Hour, outputPath: outputPath,
	}); err == nil {
		t.Fatal("second plan write error = nil")
	}
}

func TestRunStoreFileRetentionApplyRequiresExactUsecaseResult(t *testing.T) {
	t.Parallel()

	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"plan_id":"a"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plan) error = %v", err)
	}
	stub := &fileRetentionUsecaseStub{result: apptypes.FileRetentionApplyResult{
		PlanID: "plan-id", CandidateCount: 2, DeletedCount: 1, AlreadyCommitted: 1,
	}}
	cli := NewRootCLI(WithFileRetention(stub))
	var output bytes.Buffer
	if err := cli.runStoreFileRetentionApply(context.Background(), &output, storeFileRetentionApplyInput{planPath: planPath, confirmedPlanID: "plan-id"}); err != nil {
		t.Fatalf("runStoreFileRetentionApply() error = %v", err)
	}
	if stub.applyCalls != 1 || stub.confirmedID != "plan-id" {
		t.Fatalf("apply calls/confirmation = %d/%q", stub.applyCalls, stub.confirmedID)
	}
	if got := output.String(); !bytes.Contains([]byte(got), []byte("Deleted: 1")) || !bytes.Contains([]byte(got), []byte("Already committed: 1")) {
		t.Fatalf("output = %q", got)
	}
}

func TestFileRetentionClassRequestsRejectsRootWithoutCeiling(t *testing.T) {
	t.Parallel()

	_, err := fileRetentionClassRequests(storeFileRetentionPlanInput{
		backupRoot: t.TempDir(), archiveMaxCount: -1, archiveMaxBytes: -1, backupMaxCount: -1, backupMaxBytes: -1,
	})
	if err == nil {
		t.Fatal("fileRetentionClassRequests() error = nil")
	}
}

type fileRetentionUsecaseStub struct {
	plan        []byte
	result      apptypes.FileRetentionApplyResult
	statuses    []apptypes.FileRetentionCapacityStatus
	request     apptypes.FileRetentionPlanRequest
	statusReq   apptypes.FileRetentionCapacityRequest
	confirmedID string
	createCalls int
	applyCalls  int
	statusCalls int
}

func (stub *fileRetentionUsecaseStub) InspectCapacity(_ context.Context, request apptypes.FileRetentionCapacityRequest) ([]apptypes.FileRetentionCapacityStatus, error) {
	stub.statusCalls++
	stub.statusReq = request
	return stub.statuses, nil
}

func (stub *fileRetentionUsecaseStub) CreatePlan(_ context.Context, request apptypes.FileRetentionPlanRequest, _ time.Time) ([]byte, error) {
	stub.createCalls++
	stub.request = request
	return stub.plan, nil
}

func (stub *fileRetentionUsecaseStub) Apply(_ context.Context, _ []byte, confirmedPlanID string, _ time.Time) (apptypes.FileRetentionApplyResult, error) {
	stub.applyCalls++
	stub.confirmedID = confirmedPlanID
	return stub.result, nil
}
