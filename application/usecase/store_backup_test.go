package usecase_test

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
)

type storeBackupCreatorStub struct {
	receivedDBPath     string
	receivedOutputPath string
	receivedOverwrite  bool
	called             bool
	err                error
}

func (s *storeBackupCreatorStub) CreateBackup(_ context.Context, dbPath string, outputPath string, overwrite bool) error {
	s.called = true
	s.receivedDBPath = dbPath
	s.receivedOutputPath = outputPath
	s.receivedOverwrite = overwrite

	return s.err
}

type storeBackupRestorerStub struct {
	receivedInputPath string
	receivedDBPath    string
	receivedOverwrite bool
	called            bool
	err               error
}

func (s *storeBackupRestorerStub) RestoreBackup(_ context.Context, inputPath string, dbPath string, overwrite bool) error {
	s.called = true
	s.receivedInputPath = inputPath
	s.receivedDBPath = dbPath
	s.receivedOverwrite = overwrite

	return s.err
}

func TestCreateStoreBackupUsecase_Run(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          usecase.CreateStoreBackupInput
		stub           *storeBackupCreatorStub
		wantCalled     bool
		wantDBPath     string
		wantOutputPath string
		wantOverwrite  bool
		wantErr        bool
	}{
		{
			name: "DB と出力先を渡してバックアップできる",
			input: usecase.CreateStoreBackupInput{
				DBPath:     "/tmp/traceary.db",
				OutputPath: "/tmp/traceary-backup.db",
				Overwrite:  true,
			},
			stub:           &storeBackupCreatorStub{},
			wantCalled:     true,
			wantDBPath:     "/tmp/traceary.db",
			wantOutputPath: "/tmp/traceary-backup.db",
			wantOverwrite:  true,
		},
		{
			name: "DB パスが空ならエラー",
			input: usecase.CreateStoreBackupInput{
				DBPath:     " ",
				OutputPath: "/tmp/traceary-backup.db",
			},
			stub:       &storeBackupCreatorStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name: "出力先が空ならエラー",
			input: usecase.CreateStoreBackupInput{
				DBPath:     "/tmp/traceary.db",
				OutputPath: " ",
			},
			stub:       &storeBackupCreatorStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name: "作成先が失敗したらエラー",
			input: usecase.CreateStoreBackupInput{
				DBPath:     "/tmp/traceary.db",
				OutputPath: "/tmp/traceary-backup.db",
			},
			stub: &storeBackupCreatorStub{
				err: context.DeadlineExceeded,
			},
			wantErr:        true,
			wantCalled:     true,
			wantDBPath:     "/tmp/traceary.db",
			wantOutputPath: "/tmp/traceary-backup.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewCreateStoreBackupUsecase(tt.stub)

			err := sut.Run(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("CreateBackup() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
			if tt.stub.receivedDBPath != tt.wantDBPath {
				t.Fatalf("CreateBackup() dbPath = %q, want %q", tt.stub.receivedDBPath, tt.wantDBPath)
			}
			if tt.stub.receivedOutputPath != tt.wantOutputPath {
				t.Fatalf("CreateBackup() outputPath = %q, want %q", tt.stub.receivedOutputPath, tt.wantOutputPath)
			}
			if tt.stub.receivedOverwrite != tt.wantOverwrite {
				t.Fatalf("CreateBackup() overwrite = %v, want %v", tt.stub.receivedOverwrite, tt.wantOverwrite)
			}
		})
	}
}

func TestRestoreStoreBackupUsecase_Run(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         usecase.RestoreStoreBackupInput
		stub          *storeBackupRestorerStub
		wantCalled    bool
		wantInputPath string
		wantDBPath    string
		wantOverwrite bool
		wantErr       bool
	}{
		{
			name: "入力ファイルから DB を復元できる",
			input: usecase.RestoreStoreBackupInput{
				DBPath:    "/tmp/traceary.db",
				InputPath: "/tmp/traceary-backup.db",
				Overwrite: true,
			},
			stub:          &storeBackupRestorerStub{},
			wantCalled:    true,
			wantInputPath: "/tmp/traceary-backup.db",
			wantDBPath:    "/tmp/traceary.db",
			wantOverwrite: true,
		},
		{
			name: "DB パスが空ならエラー",
			input: usecase.RestoreStoreBackupInput{
				DBPath:    " ",
				InputPath: "/tmp/traceary-backup.db",
			},
			stub:       &storeBackupRestorerStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name: "入力ファイルが空ならエラー",
			input: usecase.RestoreStoreBackupInput{
				DBPath:    "/tmp/traceary.db",
				InputPath: " ",
			},
			stub:       &storeBackupRestorerStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name: "復元先が失敗したらエラー",
			input: usecase.RestoreStoreBackupInput{
				DBPath:    "/tmp/traceary.db",
				InputPath: "/tmp/traceary-backup.db",
			},
			stub: &storeBackupRestorerStub{
				err: context.DeadlineExceeded,
			},
			wantErr:       true,
			wantCalled:    true,
			wantInputPath: "/tmp/traceary-backup.db",
			wantDBPath:    "/tmp/traceary.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewRestoreStoreBackupUsecase(tt.stub)

			err := sut.Run(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("RestoreBackup() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
			if tt.stub.receivedInputPath != tt.wantInputPath {
				t.Fatalf("RestoreBackup() inputPath = %q, want %q", tt.stub.receivedInputPath, tt.wantInputPath)
			}
			if tt.stub.receivedDBPath != tt.wantDBPath {
				t.Fatalf("RestoreBackup() dbPath = %q, want %q", tt.stub.receivedDBPath, tt.wantDBPath)
			}
			if tt.stub.receivedOverwrite != tt.wantOverwrite {
				t.Fatalf("RestoreBackup() overwrite = %v, want %v", tt.stub.receivedOverwrite, tt.wantOverwrite)
			}
		})
	}
}
