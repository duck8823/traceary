package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestConfirmBackupRestore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "yes で続行する",
			input:       "yes\n",
			expectError: false,
		},
		{
			name:        "y で続行する",
			input:       "y\n",
			expectError: false,
		},
		{
			name:        "空入力で中止する",
			input:       "\n",
			expectError: true,
		},
		{
			name:        "no で中止する",
			input:       "no\n",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			output := &bytes.Buffer{}
			prompter := &backupRestorePrompter{
				reader:      strings.NewReader(tt.input),
				interactive: true,
			}

			err := prompter.confirm(output, "/tmp/traceary.db")
			if tt.expectError && err == nil {
				t.Fatal("confirm() error = nil, want error")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("confirm() error = %v", err)
			}
			if !strings.Contains(output.String(), "/tmp/traceary.db") {
				t.Fatalf("prompt output = %q, want destination path", output.String())
			}
		})
	}
}

type restoreStoreBackupUsecaseForTest struct {
	called bool
}

func (s *restoreStoreBackupUsecaseForTest) Initialize(_ context.Context) error { return nil }
func (s *restoreStoreBackupUsecaseForTest) CreateBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *restoreStoreBackupUsecaseForTest) RestoreBackup(_ context.Context, _ string, _ bool) error {
	s.called = true
	return nil
}
func (s *restoreStoreBackupUsecaseForTest) CollectGarbage(_ context.Context, _ time.Time, _ apptypes.GarbageCollectionTarget, _ bool) (apptypes.CollectGarbageResult, error) {
	return apptypes.CollectGarbageResult{}, nil
}
func (s *restoreStoreBackupUsecaseForTest) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool) (apptypes.CloseStaleSessionsResult, error) {
	return apptypes.CloseStaleSessionsResult{}, nil
}
func (s *restoreStoreBackupUsecaseForTest) DedupeContentEvents(_ context.Context, _ apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *restoreStoreBackupUsecaseForTest) RestoreContentEventDedupeRun(_ context.Context, _ string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}

func TestRunBackupRestore_InteractiveConfirmation(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := os.WriteFile(dbPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	inputPath := filepath.Join(t.TempDir(), "traceary-backup.db")
	if err := os.WriteFile(inputPath, []byte("backup"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	restoreBackup := &restoreStoreBackupUsecaseForTest{}
	rootCLI := NewRootCLI(WithStoreManagement(restoreBackup))
	stdout := &bytes.Buffer{}

	err := rootCLI.runBackupRestore(context.Background(), stdout, backupRestoreCommandInput{
		dbPath:    dbPath,
		inputPath: inputPath,
		force:     true,
		prompter: &backupRestorePrompter{
			reader:      strings.NewReader("yes\n"),
			interactive: true,
		},
	})
	if err != nil {
		t.Fatalf("runBackupRestore() error = %v", err)
	}
	if !restoreBackup.called {
		t.Fatal("restore usecase was not called")
	}
	if !strings.Contains(stdout.String(), "Continue with restore?") {
		t.Fatalf("stdout = %q, want confirmation prompt", stdout.String())
	}
}

func TestRunBackupRestore_AssumeYesSkipsInteractiveConfirmation(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if err := os.WriteFile(dbPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	inputPath := filepath.Join(t.TempDir(), "traceary-backup.db")
	if err := os.WriteFile(inputPath, []byte("backup"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	restoreBackup := &restoreStoreBackupUsecaseForTest{}
	rootCLI := NewRootCLI(WithStoreManagement(restoreBackup))
	stdout := &bytes.Buffer{}

	err := rootCLI.runBackupRestore(context.Background(), stdout, backupRestoreCommandInput{
		dbPath:    dbPath,
		inputPath: inputPath,
		force:     true,
		assumeYes: true,
		prompter: &backupRestorePrompter{
			reader:      strings.NewReader("no\n"),
			interactive: true,
		},
	})
	if err != nil {
		t.Fatalf("runBackupRestore() error = %v", err)
	}
	if !restoreBackup.called {
		t.Fatal("restore usecase was not called")
	}
	if strings.Contains(stdout.String(), "Continue with restore?") {
		t.Fatalf("stdout = %q, want no confirmation prompt", stdout.String())
	}
}
