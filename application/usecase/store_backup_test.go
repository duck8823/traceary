package usecase_test

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
)

type storeBackupCreatorStub struct {
	receivedOutputPath string
	receivedOverwrite  bool
	called             bool
	err                error
}

func (s *storeBackupCreatorStub) CreateBackup(_ context.Context, outputPath string, overwrite bool) error {
	s.called = true
	s.receivedOutputPath = outputPath
	s.receivedOverwrite = overwrite

	return s.err
}

type storeBackupRestorerStub struct {
	receivedInputPath string
	receivedOverwrite bool
	called            bool
	err               error
}

func (s *storeBackupRestorerStub) RestoreBackup(_ context.Context, inputPath string, overwrite bool) error {
	s.called = true
	s.receivedInputPath = inputPath
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
		wantOutputPath string
		wantOverwrite  bool
		wantErr        bool
	}{
		{
			name: "DB と出力先を渡してバックアップできる",
			input: usecase.CreateStoreBackupInput{
				OutputPath: "/tmp/traceary-backup.db",
				Overwrite:  true,
			},
			stub:           &storeBackupCreatorStub{},
			wantCalled:     true,
			wantOutputPath: "/tmp/traceary-backup.db",
			wantOverwrite:  true,
		},
		{
			name: "出力先が空ならエラー",
			input: usecase.CreateStoreBackupInput{
				OutputPath: " ",
			},
			stub:       &storeBackupCreatorStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name: "作成先が失敗したらエラー",
			input: usecase.CreateStoreBackupInput{
				OutputPath: "/tmp/traceary-backup.db",
			},
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

			sut := usecase.NewCreateStoreBackupUsecase(tt.stub)

			err := sut.Run(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("CreateBackup() called = %v, want %v", tt.stub.called, tt.wantCalled)
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
		wantOverwrite bool
		wantErr       bool
	}{
		{
			name: "入力ファイルから DB を復元できる",
			input: usecase.RestoreStoreBackupInput{
				InputPath: "/tmp/traceary-backup.db",
				Overwrite: true,
			},
			stub:          &storeBackupRestorerStub{},
			wantCalled:    true,
			wantInputPath: "/tmp/traceary-backup.db",
			wantOverwrite: true,
		},
		{
			name: "入力ファイルが空ならエラー",
			input: usecase.RestoreStoreBackupInput{
				InputPath: " ",
			},
			stub:       &storeBackupRestorerStub{},
			wantErr:    true,
			wantCalled: false,
		},
		{
			name: "復元先が失敗したらエラー",
			input: usecase.RestoreStoreBackupInput{
				InputPath: "/tmp/traceary-backup.db",
			},
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
			if tt.stub.receivedOverwrite != tt.wantOverwrite {
				t.Fatalf("RestoreBackup() overwrite = %v, want %v", tt.stub.receivedOverwrite, tt.wantOverwrite)
			}
		})
	}
}
