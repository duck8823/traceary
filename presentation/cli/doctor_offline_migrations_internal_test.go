package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/types"
)

type offlineMigrationStoreStub struct {
	pending []int64
}

func (s *offlineMigrationStoreStub) Initialize(context.Context) error { return nil }
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

func TestDoctorFixDoesNotCallInitializeAuthorizedOnLiveStore(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	dbPath := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(dbPath, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &offlineMigrationStoreStub{pending: []int64{35, 45, 76}}
	upgrade := &recordingOfflineUpgrade{receipt: application.PreparedStoreUpgradeReceipt{RollbackPath: dbPath + ".rollback-x"}}
	root := NewRootCLI(
		WithStoreManagement(store),
		WithPreparedStoreUpgradeFactory(func(string) application.PreparedStoreUpgradeUsecase { return upgrade }),
	)
	log, recorded := root.applyAuthorizedStoreInit(context.Background(), doctorCommandInput{dbPath: dbPath})
	if !recorded {
		t.Fatal("expected a recorded fix log")
	}
	if upgrade.starts != 1 {
		t.Fatalf("RunUpgrade starts = %d, want 1", upgrade.starts)
	}
	if !strings.Contains(log.Action, "forensic backup (not an interchangeable rollback target)") {
		t.Fatalf("action = %q, want forensic-backup wording", log.Action)
	}
	if !strings.Contains(log.Action, dbPath+".rollback-x") {
		t.Fatalf("action = %q, want rollback path", log.Action)
	}
}

func TestDoctorReportsPendingOfflineWork(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := NewRootCLI(WithStoreManagement(&offlineMigrationStoreStub{pending: []int64{76}}))
	check := root.inspectOfflineMigrations(context.Background())
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "76") {
		t.Fatalf("check = %#v", check)
	}
}

func TestDoctorReportsNoneAfterPublication(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := NewRootCLI(WithStoreManagement(&offlineMigrationStoreStub{}))
	check := root.inspectOfflineMigrations(context.Background())
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
}

type recordingOfflineUpgrade struct {
	starts  int
	receipt application.PreparedStoreUpgradeReceipt
}

func (r *recordingOfflineUpgrade) Plan(context.Context, application.PreparedStoreUpgradeCommand) (domain.PreparedStoreUpgradeRun, error) {
	return domain.PreparedStoreUpgradeRun{}, nil
}
func (r *recordingOfflineUpgrade) Prepare(context.Context, string) (domain.PreparedStoreUpgradeRun, error) {
	return domain.PreparedStoreUpgradeRun{}, nil
}
func (r *recordingOfflineUpgrade) Publish(context.Context, string) (application.PreparedStoreUpgradeReceipt, error) {
	return r.receipt, nil
}
func (r *recordingOfflineUpgrade) Resume(context.Context, string) (application.PreparedStoreUpgradeReceipt, error) {
	return r.receipt, nil
}
func (r *recordingOfflineUpgrade) Rollback(context.Context, string) (domain.PreparedStoreUpgradeRun, error) {
	return domain.PreparedStoreUpgradeRun{}, nil
}
func (r *recordingOfflineUpgrade) RunUpgrade(context.Context, application.PreparedStoreUpgradeCommand) (application.PreparedStoreUpgradeReceipt, error) {
	r.starts++
	return r.receipt, nil
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
