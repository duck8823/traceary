package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestSearchProjectionStatusRoutingKeepsMeasurementsOffControlPaths(t *testing.T) {
	t.Parallel()
	budget := apptypes.SearchProjectionBudget{
		Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1,
		DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1,
	}
	tests := []struct {
		name              string
		run               func(*usecase.SearchProjectionUsecase, apptypes.SearchProjectionBudget) error
		wantControlReads  int
		wantMeasuredReads int
	}{
		{
			name: "start uses control status",
			run: func(u *usecase.SearchProjectionUsecase, b apptypes.SearchProjectionBudget) error {
				_, err := u.StartGeneration(context.Background(), b, time.Now())
				if err != nil {
					return xerrors.Errorf("start generation: %w", err)
				}
				return nil
			},
			wantControlReads: 1,
		},
		{
			name: "inspect uses measured status",
			run: func(u *usecase.SearchProjectionUsecase, _ apptypes.SearchProjectionBudget) error {
				_, err := u.Inspect(context.Background())
				if err != nil {
					return xerrors.Errorf("inspect projection: %w", err)
				}
				return nil
			},
			wantMeasuredReads: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &wallBudgetStore{budget: budget, state: "idle"}
			if err := tt.run(usecase.NewSearchProjectionUsecase(store), budget); err != nil {
				t.Fatal(err)
			}
			got := [2]int{store.controlReads, store.measuredReads}
			want := [2]int{tt.wantControlReads, tt.wantMeasuredReads}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("status reads (-want +got):\n%s", diff)
			}
		})
	}
}
