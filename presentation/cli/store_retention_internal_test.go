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

type rawBodyRetentionStub struct {
	plan []byte
}

type retentionStoreStub struct {
	minimalStoreStub
	initCalled bool
}

func (s *retentionStoreStub) Initialize(context.Context) error {
	s.initCalled = true
	return nil
}

func (s *rawBodyRetentionStub) CreatePlan(context.Context, time.Time, string, time.Time) ([]byte, error) {
	return s.plan, nil
}

func (*rawBodyRetentionStub) Apply(context.Context, []byte, string, string, time.Time) (apptypes.RawBodyApplyResult, error) {
	return apptypes.RawBodyApplyResult{}, nil
}

func (*rawBodyRetentionStub) Restore(context.Context, []byte, string, string, time.Time) (apptypes.RawBodyRestoreResult, error) {
	return apptypes.RawBodyRestoreResult{}, nil
}

func TestStoreRetentionPlanDoesNotInitializeOrMigrateStore(t *testing.T) {
	t.Parallel()

	store := &retentionStoreStub{}
	root := NewRootCLI(
		WithStoreManagement(store),
		WithRawBodyRetention(&rawBodyRetentionStub{plan: []byte(`{"plan_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}),
	)
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "plan.json")
	if err := root.runStoreRetentionPlan(context.Background(), &bytes.Buffer{}, storeRetentionPlanInput{
		dbPath: filepath.Join(dir, "old.db"), keepDays: 30,
		recoveryPath: filepath.Join(dir, "recovery.tar"), outputPath: outputPath,
	}); err != nil {
		t.Fatalf("runStoreRetentionPlan() error = %v", err)
	}
	if store.initCalled {
		t.Fatal("Initialize() called during retention plan")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.db")); !os.IsNotExist(err) {
		t.Fatalf("old DB stat error = %v, want not exist", err)
	}
}

func TestStoreRetentionApplyReadsAndValidatesBeforeStoreOpen(t *testing.T) {
	t.Parallel()

	store := &retentionStoreStub{}
	root := NewRootCLI(
		WithStoreManagement(store),
		WithRawBodyRetention(&rawBodyRetentionStub{}),
	)
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"plan_id":"invalid-on-purpose"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plan) error = %v", err)
	}
	if _, err := root.readRetentionExecutionInput(storeRetentionExecutionInput{
		dbPath: filepath.Join(dir, "old.db"), planPath: planPath,
	}); err != nil {
		t.Fatalf("readRetentionExecutionInput() error = %v", err)
	}
	if store.initCalled {
		t.Fatal("Initialize() called before retention plan validation")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.db")); !os.IsNotExist(err) {
		t.Fatalf("old DB stat error = %v, want not exist", err)
	}
}

func TestStoreRetentionCommandsAreVisibleButRemainExplicit(t *testing.T) {
	t.Parallel()

	rootCLI := NewRootCLI()
	command := rootCLI.newStoreRetentionCommand()
	if command.Hidden {
		t.Fatal("store retention command is hidden after copied-store dogfood")
	}
	files, _, err := command.Find([]string{"files"})
	if err != nil {
		t.Fatalf("Find(files) error = %v", err)
	}
	if files.Hidden {
		t.Fatal("store retention files command is hidden after copied-store dogfood")
	}
	apply, _, err := command.Find([]string{"apply"})
	if err != nil {
		t.Fatalf("Find(apply) error = %v", err)
	}
	for _, required := range []string{"plan", "recovery", "confirm-plan-id"} {
		if flag := apply.Flag(required); flag == nil {
			t.Fatalf("apply missing explicit %s flag", required)
		}
	}
}
