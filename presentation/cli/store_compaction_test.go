package cli

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type compactionCLIStub struct{ planned string }

func (s *compactionCLIStub) Plan(_ context.Context, path string) (domain.CompactionRun, error) {
	s.planned = path
	return domain.CompactionRun{ID: "run"}, nil
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

func TestStoreCompactionPlanUsesDedicatedPathBoundComposition(t *testing.T) {
	stub := &compactionCLIStub{}
	root := NewRootCLI(WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
	root.SetArgs([]string{"store", "compact", "plan", "--db-path", t.TempDir() + "/store.db"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if stub.planned == "" {
		t.Fatal("Plan was not called")
	}
}
