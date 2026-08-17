package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

type offlineMigrationStoreStub struct {
	pending []int64
}

func (s *offlineMigrationStoreStub) Initialize(context.Context) error { return nil }
func (s *offlineMigrationStoreStub) InitializeAuthorized(context.Context) error {
	return nil
}
func (s *offlineMigrationStoreStub) PreviewOfflineMigrations(context.Context) ([]int64, error) {
	return s.pending, nil
}
func (s *offlineMigrationStoreStub) CreateBackup(context.Context, string, bool) error {
	return nil
}
func (s *offlineMigrationStoreStub) RestoreBackup(context.Context, string, bool) error {
	return nil
}
func (s *offlineMigrationStoreStub) CollectGarbage(context.Context, time.Time, apptypes.GarbageCollectionTarget, bool) (apptypes.CollectGarbageResult, error) {
	return apptypes.CollectGarbageResult{}, nil
}
func (s *offlineMigrationStoreStub) CloseStaleSessions(context.Context, time.Duration, bool, []types.SessionID) (apptypes.CloseStaleSessionsResult, error) {
	return apptypes.CloseStaleSessionsResult{}, nil
}
func (s *offlineMigrationStoreStub) DedupeContentEvents(context.Context, apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *offlineMigrationStoreStub) RestoreContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}
func (s *offlineMigrationStoreStub) PurgeContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupePurgeResult, error) {
	return apptypes.ContentEventDedupePurgeResult{}, nil
}
func (s *offlineMigrationStoreStub) ListContentEventDedupeRuns(context.Context) ([]apptypes.ContentEventDedupeRun, error) {
	return nil, nil
}
func (s *offlineMigrationStoreStub) CreateStoreArchive(context.Context, apptypes.StoreArchiveCreateParams) (apptypes.StoreArchiveResult, error) {
	return apptypes.StoreArchiveResult{}, nil
}
func (s *offlineMigrationStoreStub) VerifyStoreArchive(context.Context, string, []byte) error {
	return nil
}
func (s *offlineMigrationStoreStub) RestoreStoreArchive(context.Context, string, []byte, bool) (apptypes.StoreArchiveRestoreResult, error) {
	return apptypes.StoreArchiveRestoreResult{}, nil
}

func TestSkippedOfflineMigrationsCheckIsSkipNotWarn(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	check := skippedOfflineMigrationsCheck()
	if check.Name != "offline-migrations" || check.Status != doctorStatusSkip {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "filesystem-metadata-only") {
		t.Fatalf("message = %q", check.Message)
	}
	if check.AutoFixAvailable {
		t.Fatal("skip check must not advertise auto-fix")
	}
	if check.FixCommand != "traceary doctor --fix" {
		t.Fatalf("FixCommand = %q", check.FixCommand)
	}
}

func TestInspectOfflineMigrationsPassesWhenNonePending(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := NewRootCLI(WithStoreManagement(&offlineMigrationStoreStub{}))
	check := root.inspectOfflineMigrations(context.Background())
	if check.Name != "offline-migrations" || check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "no pending data-dependent migrations") {
		t.Fatalf("message = %q", check.Message)
	}
	if check.AutoFixAvailable || check.FixCommand != "" {
		t.Fatalf("pass check must not advertise a fix: %#v", check)
	}
}

func TestInspectOfflineMigrationsWarnsWhenPending(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := NewRootCLI(WithStoreManagement(&offlineMigrationStoreStub{pending: []int64{35, 45}}))
	check := root.inspectOfflineMigrations(context.Background())
	if check.Status != doctorStatusWarn {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "35, 45") {
		t.Fatalf("message = %q", check.Message)
	}
	if check.AutoFixAvailable {
		t.Fatal("pending check must stay guided-only; --fix applies via applyAuthorizedStoreInit")
	}
	if check.FixCommand != "traceary doctor --fix" {
		t.Fatalf("FixCommand = %q", check.FixCommand)
	}
}
