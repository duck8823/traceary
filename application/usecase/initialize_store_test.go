package usecase_test

import (
	"context"
	"testing"

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

func TestInitializeStoreUsecase_Run(t *testing.T) {
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

			sut := usecase.NewInitializeStoreUsecase(tt.stub)

			err := sut.Run(context.Background())

			if (err != nil) != tt.wantErr {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.stub.called != tt.wantCalled {
				t.Fatalf("Initialize() called = %v, want %v", tt.stub.called, tt.wantCalled)
			}
		})
	}
}
