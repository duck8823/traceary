package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type compactionCLIStub struct {
	compacted string
	forced    bool
}

func (s *compactionCLIStub) Compact(_ context.Context, in application.CompactInput) (application.CompactResult, error) {
	s.compacted = in.Source
	s.forced = in.Force
	return application.CompactResult{Run: domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted}}, nil
}
func (*compactionCLIStub) Plan(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactionCLIStub) Apply(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactionCLIStub) Resume(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactionCLIStub) Status(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactionCLIStub) Rollback(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}

func TestStoreCompactUsesDedicatedPathBoundComposition(t *testing.T) {
	stub := &compactionCLIStub{}
	root := NewRootCLI(WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
	path := t.TempDir() + "/store.db"
	root.SetArgs([]string{"store", "compact", "--db-path", path})
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if stub.compacted == "" {
		t.Fatal("Compact was not called")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["run_id"] != "run" {
		t.Fatalf("run_id = %v", payload["run_id"])
	}
}

func TestStoreCompactPlanIsUnknown(t *testing.T) {
	root := NewRootCLI(WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return &compactionCLIStub{} })).Command()
	root.SetArgs([]string{"store", "compact", "plan"})
	if err := root.Execute(); err == nil {
		t.Fatal("store compact plan must be unknown")
	}
}
