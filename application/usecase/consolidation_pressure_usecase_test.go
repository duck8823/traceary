package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type pressureRepoStub struct {
	sum       int64
	err       error
	lastAfter types.Optional[types.EventID]
	calls     int
}

func (s *pressureRepoStub) SumBodyBytesAfter(
	_ context.Context,
	_ types.SessionID,
	coversTo types.Optional[types.EventID],
) (int64, error) {
	s.calls++
	s.lastAfter = coversTo
	return s.sum, s.err
}

func TestConsolidationPressureUsecase_Check(t *testing.T) {
	t.Parallel()

	sessionID := types.SessionID("sess-pressure")
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	refinement, err := model.NewSessionRefinement(
		sessionID, 1, "evt-from", "evt-to",
		"previous fold summary", "kw", "agent", now, false,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement() error = %v", err)
	}

	tests := []struct {
		name      string
		pressure  *pressureRepoStub
		repo      *sessionRefinementRepositoryStub
		threshold int64
		want      usecase.ConsolidationPressureResult
		wantAfter types.Optional[types.EventID]
		wantErr   bool
	}{
		{
			name:      "below threshold is not due",
			pressure:  &pressureRepoStub{sum: 63 * 1024},
			repo:      &sessionRefinementRepositoryStub{},
			threshold: 64 * 1024,
			want: usecase.ConsolidationPressureResult{
				PressureBytes: 63 * 1024,
				Due:           false,
			},
			wantAfter: types.None[types.EventID](),
		},
		{
			name:      "at threshold is due",
			pressure:  &pressureRepoStub{sum: 64 * 1024},
			repo:      &sessionRefinementRepositoryStub{},
			threshold: 64 * 1024,
			want: usecase.ConsolidationPressureResult{
				PressureBytes: 64 * 1024,
				Due:           true,
			},
			wantAfter: types.None[types.EventID](),
		},
		{
			name:     "previous refinement is returned and scopes pressure",
			pressure: &pressureRepoStub{sum: 65 * 1024},
			repo: &sessionRefinementRepositoryStub{
				bySession: map[types.SessionID]*model.SessionRefinement{
					sessionID: refinement,
				},
			},
			threshold: 64 * 1024,
			want: usecase.ConsolidationPressureResult{
				PressureBytes:    65 * 1024,
				Due:              true,
				PreviousSummary:  types.Some("previous fold summary"),
				PreviousCoversTo: types.Some(types.EventID("evt-to")),
			},
			wantAfter: types.Some(types.EventID("evt-to")),
		},
		{
			name:      "threshold zero disables due even with high pressure",
			pressure:  &pressureRepoStub{sum: 10 * 1024 * 1024},
			repo:      &sessionRefinementRepositoryStub{},
			threshold: 0,
			want: usecase.ConsolidationPressureResult{
				PressureBytes: 10 * 1024 * 1024,
				Due:           false,
			},
			wantAfter: types.None[types.EventID](),
		},
		{
			name:      "pressure repository error propagates",
			pressure:  &pressureRepoStub{err: errors.New("db locked")},
			repo:      &sessionRefinementRepositoryStub{},
			threshold: 64 * 1024,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewConsolidationPressureUsecase(tt.pressure, tt.repo)
			got, err := sut.Check(context.Background(), sessionID, tt.threshold)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Check() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty(), cmp.AllowUnexported(types.Optional[string]{}, types.Optional[types.EventID]{})); diff != "" {
				t.Fatalf("Check() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAfter, tt.pressure.lastAfter, cmp.AllowUnexported(types.Optional[types.EventID]{})); diff != "" {
				t.Fatalf("coversTo passed to SumBodyBytesAfter mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
