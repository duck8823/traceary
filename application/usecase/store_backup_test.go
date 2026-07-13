package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type storeBackupCreatorStub struct {
	receivedOutputPath string
	receivedOverwrite  bool
	called             bool
	err                error
}

func (s *storeBackupCreatorStub) Initialize(_ context.Context) error { return nil }
func (s *storeBackupCreatorStub) CreateBackup(_ context.Context, outputPath string, overwrite bool) error {
	s.called = true
	s.receivedOutputPath = outputPath
	s.receivedOverwrite = overwrite

	return s.err
}
func (s *storeBackupCreatorStub) RestoreBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *storeBackupCreatorStub) CollectGarbage(_ context.Context, _ time.Time, _ apptypes.GarbageCollectionTarget, _ bool) (int, error) {
	return 0, nil
}
func (s *storeBackupCreatorStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool, _ []types.SessionID) (int, error) {
	return 0, nil
}
func (s *storeBackupCreatorStub) DedupeContentEvents(_ context.Context, _ apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *storeBackupCreatorStub) RestoreContentEventDedupeRun(_ context.Context, _ string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}

type storeBackupRestorerStub struct {
	receivedInputPath string
	receivedOverwrite bool
	called            bool
	err               error
}

func (s *storeBackupRestorerStub) Initialize(_ context.Context) error { return nil }
func (s *storeBackupRestorerStub) CreateBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *storeBackupRestorerStub) RestoreBackup(_ context.Context, inputPath string, overwrite bool) error {
	s.called = true
	s.receivedInputPath = inputPath
	s.receivedOverwrite = overwrite

	return s.err
}
func (s *storeBackupRestorerStub) CollectGarbage(_ context.Context, _ time.Time, _ apptypes.GarbageCollectionTarget, _ bool) (int, error) {
	return 0, nil
}
func (s *storeBackupRestorerStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool, _ []types.SessionID) (int, error) {
	return 0, nil
}
func (s *storeBackupRestorerStub) DedupeContentEvents(_ context.Context, _ apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *storeBackupRestorerStub) RestoreContentEventDedupeRun(_ context.Context, _ string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}

func TestStoreManagementUsecase_CreateBackup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		outputPath     string
		overwrite      bool
		stub           *storeBackupCreatorStub
		wantCalled     bool
		wantOutputPath string
		wantOverwrite  bool
		wantErr        bool
	}{
		{
			name:           "DB と出力先を渡してバックアップできる",
			outputPath:     "/tmp/traceary-backup.db",
			overwrite:      true,
			stub:           &storeBackupCreatorStub{},
			wantCalled:     true,
			wantOutputPath: "/tmp/traceary-backup.db",
			wantOverwrite:  true,
		},
		{
			name:       "出力先が空ならエラー",
			outputPath: " ",
			stub:       &storeBackupCreatorStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name:       "作成先が失敗したらエラー",
			outputPath: "/tmp/traceary-backup.db",
			stub: &storeBackupCreatorStub{
				err: context.DeadlineExceeded,
			},
			wantErr:        true,
			wantCalled:     true,
			wantOutputPath: "/tmp/traceary-backup.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewStoreManagementUsecase(tt.stub)

			err := sut.CreateBackup(context.Background(), tt.outputPath, tt.overwrite)

			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateBackup() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("CreateBackup() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
			if diff := cmp.Diff(tt.wantOutputPath, tt.stub.receivedOutputPath); diff != "" {
				t.Fatalf("CreateBackup() outputPath mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOverwrite, tt.stub.receivedOverwrite); diff != "" {
				t.Fatalf("CreateBackup() overwrite mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStoreManagementUsecase_RestoreBackup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		inputPath     string
		overwrite     bool
		stub          *storeBackupRestorerStub
		wantCalled    bool
		wantInputPath string
		wantOverwrite bool
		wantErr       bool
	}{
		{
			name:          "入力ファイルから DB を復元できる",
			inputPath:     "/tmp/traceary-backup.db",
			overwrite:     true,
			stub:          &storeBackupRestorerStub{},
			wantCalled:    true,
			wantInputPath: "/tmp/traceary-backup.db",
			wantOverwrite: true,
		},
		{
			name:       "入力ファイルが空ならエラー",
			inputPath:  " ",
			stub:       &storeBackupRestorerStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name:      "復元先が失敗したらエラー",
			inputPath: "/tmp/traceary-backup.db",
			stub: &storeBackupRestorerStub{
				err: context.DeadlineExceeded,
			},
			wantErr:       true,
			wantCalled:    true,
			wantInputPath: "/tmp/traceary-backup.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewStoreManagementUsecase(tt.stub)

			err := sut.RestoreBackup(context.Background(), tt.inputPath, tt.overwrite)

			if (err != nil) != tt.wantErr {
				t.Fatalf("RestoreBackup() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("RestoreBackup() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
			if diff := cmp.Diff(tt.wantInputPath, tt.stub.receivedInputPath); diff != "" {
				t.Fatalf("RestoreBackup() inputPath mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOverwrite, tt.stub.receivedOverwrite); diff != "" {
				t.Fatalf("RestoreBackup() overwrite mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
