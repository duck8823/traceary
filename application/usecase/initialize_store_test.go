package usecase_test

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
)

type storeInitializerStub struct {
	receivedPath string
	called       bool
	err          error
}

func (s *storeInitializerStub) Initialize(_ context.Context, dbPath string) error {
	s.called = true
	s.receivedPath = dbPath
	return s.err
}

func TestInitializeStoreUsecase_Run(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dbPath     string
		stub       *storeInitializerStub
		wantCalled bool
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "DB パスを渡して初期化できる",
			dbPath:     "/tmp/traceary.db",
			stub:       &storeInitializerStub{},
			wantCalled: true,
			wantPath:   "/tmp/traceary.db",
			wantErr:    false,
		},
		{
			name:       "DB パスが空文字の場合はエラー",
			dbPath:     "   ",
			stub:       &storeInitializerStub{},
			wantCalled: false,
			wantErr:    true,
		},
		{
			name:   "初期化に失敗した場合はエラー",
			dbPath: "/tmp/traceary.db",
			stub: &storeInitializerStub{
				err: context.DeadlineExceeded,
			},
			wantCalled: true,
			wantPath:   "/tmp/traceary.db",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewInitializeStoreUsecase(tt.stub)

			err := sut.Run(context.Background(), tt.dbPath)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("Initialize() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
			if tt.stub.receivedPath != tt.wantPath {
				t.Fatalf("Initialize() path = %q, want %q", tt.stub.receivedPath, tt.wantPath)
			}
		})
	}
}
