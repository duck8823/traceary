package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
)

func TestParseArchiveAutoInterval(t *testing.T) {
	t.Parallel()
	d, err := parseArchiveAutoInterval("")
	if err != nil || d != defaultArchiveAutoInterval {
		t.Fatalf("default: got %v %v", d, err)
	}
	d, err = parseArchiveAutoInterval("24h")
	if err != nil || d != 24*time.Hour {
		t.Fatalf("24h: got %v %v", d, err)
	}
	if _, err := parseArchiveAutoInterval("nope"); err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

func TestRunOpportunisticArchiveThenGC_disabledByDefault(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	t.Setenv("HOME", home)

	stub := &minimalStoreStub{}
	root := NewRootCLI(WithStoreManagement(stub))
	dbPath := filepath.Join(home, "traceary.db")
	root.runOpportunisticArchiveThenGC(context.Background(), dbPath)
	if stub.archiveCalls != 0 {
		t.Fatalf("archive calls = %d, want 0 when disabled", stub.archiveCalls)
	}
}

func TestRunOpportunisticArchiveThenGC_missingPassphraseEnv(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{
  "retention": {
    "mode": "archive_then_gc",
    "archive_then_gc": {
      "interval": "1h",
      "keep_days": 90,
      "target": "events",
      "passphrase_env": "TRACEARY_TEST_MISSING_PASSPHRASE"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("TRACEARY_TEST_MISSING_PASSPHRASE")

	stub := &minimalStoreStub{}
	root := NewRootCLI(WithStoreManagement(stub))
	dbPath := filepath.Join(home, "db.sqlite")
	root.runOpportunisticArchiveThenGC(context.Background(), dbPath)
	if stub.archiveCalls != 0 {
		t.Fatalf("CreateStoreArchive calls = %d, want 0 when passphrase missing", stub.archiveCalls)
	}
	status, err := readArchiveAutoStatus(dbPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.OK {
		t.Fatalf("status.OK = true, want false")
	}
	if !strings.Contains(status.Error, "passphrase") && !strings.Contains(status.Error, "TRACEARY_TEST_MISSING") {
		t.Fatalf("error = %q, want passphrase env mention", status.Error)
	}
}

func TestInspectArchiveRetention_disabledPass(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	t.Setenv("HOME", home)

	root := NewRootCLI(WithStoreManagement(&minimalStoreStub{}))
	check := root.inspectArchiveRetention(context.Background(), filepath.Join(home, "db"))
	if check.Name != "archive-retention" || check.Status != doctorStatusPass {
		t.Fatalf("check = %+v", check)
	}
	if !strings.Contains(check.Message, "disabled") && !strings.Contains(check.Message, "無効") {
		t.Fatalf("message = %q", check.Message)
	}
}

func TestLoadConfig_retentionArchiveThenGC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"retention": map[string]any{
			"mode": "archive_then_gc",
			"archive_then_gc": map[string]any{
				"interval":       "72h",
				"keep_days":      30,
				"target":         "memories",
				"output_dir":     "~/archives",
				"passphrase_env": "TRACEARY_ARCHIVE_PASSPHRASE",
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := presentation.LoadConfig()
	if cfg.Retention.Mode != presentation.RetentionModeArchiveThenGC {
		t.Fatalf("mode = %q", cfg.Retention.Mode)
	}
	if cfg.Retention.Interval != "72h" || cfg.Retention.KeepDays != 30 || cfg.Retention.Target != "memories" {
		t.Fatalf("retention = %+v", cfg.Retention)
	}
	if cfg.Retention.PassphraseEnv != "TRACEARY_ARCHIVE_PASSPHRASE" {
		t.Fatalf("passphrase_env = %q", cfg.Retention.PassphraseEnv)
	}
}

// minimalStoreStub is a tiny StoreManagementUsecase for archive auto tests.
type minimalStoreStub struct {
	archiveCalls int
	lastParams   apptypes.StoreArchiveCreateParams
}

func (s *minimalStoreStub) Initialize(context.Context) error { return nil }
func (s *minimalStoreStub) CreateBackup(context.Context, string, bool) error {
	return nil
}
func (s *minimalStoreStub) RestoreBackup(context.Context, string, bool) error {
	return nil
}
func (s *minimalStoreStub) CollectGarbage(context.Context, time.Time, apptypes.GarbageCollectionTarget, bool) (apptypes.CollectGarbageResult, error) {
	return apptypes.CollectGarbageResult{}, nil
}
func (s *minimalStoreStub) CloseStaleSessions(context.Context, time.Duration, bool, []types.SessionID) (apptypes.CloseStaleSessionsResult, error) {
	return apptypes.CloseStaleSessionsResult{}, nil
}
func (s *minimalStoreStub) DedupeContentEvents(context.Context, apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *minimalStoreStub) RestoreContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}
func (s *minimalStoreStub) CreateStoreArchive(_ context.Context, params apptypes.StoreArchiveCreateParams) (apptypes.StoreArchiveResult, error) {
	s.archiveCalls++
	s.lastParams = params
	return apptypes.StoreArchiveResult{TotalRows: 0, DeletedCount: 0, Path: params.OutputPath, Verified: true, DeletedAfterVerify: true}, nil
}
func (s *minimalStoreStub) VerifyStoreArchive(context.Context, string, []byte) error {
	return nil
}
func (s *minimalStoreStub) RestoreStoreArchive(context.Context, string, []byte, bool) (apptypes.StoreArchiveRestoreResult, error) {
	return apptypes.StoreArchiveRestoreResult{}, nil
}
