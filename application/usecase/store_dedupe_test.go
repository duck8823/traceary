package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

// dedupeStoreManagerStub captures the params the usecase forwards so tests can
// assert on the run-id/timestamp minting and pass-through behavior.
type dedupeStoreManagerStub struct {
	dedupeParams  []apptypes.ContentEventDedupeParams
	dedupeResult  apptypes.ContentEventDedupeResult
	dedupeErr     error
	restoreRunIDs []string
	restoreResult apptypes.ContentEventDedupeRestoreResult
	restoreErr    error
}

func (s *dedupeStoreManagerStub) Initialize(context.Context) error { return nil }
func (s *dedupeStoreManagerStub) CreateBackup(context.Context, string, bool) error {
	return nil
}
func (s *dedupeStoreManagerStub) RestoreBackup(context.Context, string, bool) error {
	return nil
}
func (s *dedupeStoreManagerStub) CollectGarbage(context.Context, time.Time, apptypes.GarbageCollectionTarget, bool) (int, error) {
	return 0, nil
}
func (s *dedupeStoreManagerStub) CloseStaleSessions(context.Context, time.Duration, bool, types.SessionID) (int, error) {
	return 0, nil
}
func (s *dedupeStoreManagerStub) DedupeContentEvents(_ context.Context, params apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	s.dedupeParams = append(s.dedupeParams, params)
	return s.dedupeResult, s.dedupeErr
}
func (s *dedupeStoreManagerStub) RestoreContentEventDedupeRun(_ context.Context, runID string) (apptypes.ContentEventDedupeRestoreResult, error) {
	s.restoreRunIDs = append(s.restoreRunIDs, runID)
	return s.restoreResult, s.restoreErr
}

func TestStoreManagementUsecase_DedupeContentEvents_DryRunPassesThrough(t *testing.T) {
	t.Parallel()
	stub := &dedupeStoreManagerStub{}
	uc := usecase.NewStoreManagementUsecase(stub)

	if _, err := uc.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: " codex "}); err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}
	got := stub.dedupeParams[0]
	if got.Apply {
		t.Fatalf("dry-run forwarded Apply=true")
	}
	if got.RunID != "" || !got.Now.IsZero() {
		t.Fatalf("dry-run minted run id/timestamp: %#v", got)
	}
	if got.Agent != "codex" {
		t.Fatalf("Agent = %q, want trimmed codex", got.Agent)
	}
}

func TestStoreManagementUsecase_DedupeContentEvents_ApplyMintsRunIDAndNow(t *testing.T) {
	t.Parallel()
	stub := &dedupeStoreManagerStub{}
	uc := usecase.NewStoreManagementUsecase(stub)

	if _, err := uc.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: "codex", Apply: true}); err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}
	got := stub.dedupeParams[0]
	if !got.Apply {
		t.Fatalf("apply not forwarded")
	}
	if !strings.HasPrefix(got.RunID, "dedupe-") {
		t.Fatalf("RunID = %q, want dedupe- prefix", got.RunID)
	}
	if got.Now.IsZero() {
		t.Fatalf("apply did not mint a timestamp")
	}
}

func TestStoreManagementUsecase_RestoreContentEventDedupeRun_RejectsEmpty(t *testing.T) {
	t.Parallel()
	stub := &dedupeStoreManagerStub{}
	uc := usecase.NewStoreManagementUsecase(stub)

	if _, err := uc.RestoreContentEventDedupeRun(context.Background(), "   "); err == nil {
		t.Fatalf("expected error for empty run id")
	}
	if len(stub.restoreRunIDs) != 0 {
		t.Fatalf("empty run id should not reach the port")
	}

	if _, err := uc.RestoreContentEventDedupeRun(context.Background(), " dedupe-abc "); err != nil {
		t.Fatalf("RestoreContentEventDedupeRun() error = %v", err)
	}
	if stub.restoreRunIDs[0] != "dedupe-abc" {
		t.Fatalf("run id not trimmed: %q", stub.restoreRunIDs[0])
	}
}
