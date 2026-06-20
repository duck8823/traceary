package usecase_test

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type storeInitializerStub struct {
	called bool
	err    error
}

func (s *storeInitializerStub) Initialize(_ context.Context) error {
	s.called = true
	return s.err
}
func (s *storeInitializerStub) CreateBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *storeInitializerStub) RestoreBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *storeInitializerStub) CollectGarbage(_ context.Context, _ time.Time, _ apptypes.GarbageCollectionTarget, _ bool) (int, error) {
	return 0, nil
}
func (s *storeInitializerStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool) (int, error) {
	return 0, nil
}
func (s *storeInitializerStub) DedupeContentEvents(_ context.Context, _ apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *storeInitializerStub) RestoreContentEventDedupeRun(_ context.Context, _ string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}

func TestStoreManagementUsecase_Initialize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stub       *storeInitializerStub
		wantCalled bool
		wantErr    bool
	}{
		{
			name:       "initializes successfully",
			stub:       &storeInitializerStub{},
			wantCalled: true,
			wantErr:    false,
		},
		{
			name: "returns error when initialization fails",
			stub: &storeInitializerStub{
				err: context.DeadlineExceeded,
			},
			wantCalled: true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewStoreManagementUsecase(tt.stub)

			err := sut.Initialize(context.Background())

			if (err != nil) != tt.wantErr {
				t.Fatalf("Initialize() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("Initialize() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
		})
	}
}
