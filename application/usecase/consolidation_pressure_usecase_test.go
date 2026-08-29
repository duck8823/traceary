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

type kindAfterCall struct {
	kind  types.EventKind
	after types.Optional[types.EventID]
}

type pressureRepoStub struct {
	commands    int64
	transcripts int64
	err         error
	calls       []kindAfterCall
}

func (s *pressureRepoStub) SumBodyBytesAfter(context.Context, types.SessionID, types.Optional[types.EventID]) (int64, error) {
	return 0, nil
}

func (s *pressureRepoStub) CountKindAfter(
	_ context.Context,
	_ types.SessionID,
	kind types.EventKind,
	after types.Optional[types.EventID],
) (int64, error) {
	s.calls = append(s.calls, kindAfterCall{kind: kind, after: after})
	if s.err != nil {
		return 0, s.err
	}
	if kind == types.EventKindCommandExecuted {
		return s.commands, nil
	}
	return s.transcripts, nil
}

type sessionKindStub struct {
	main  types.Optional[bool]
	err   error
	calls int
}

func (s *sessionKindStub) IsMainSession(context.Context, types.SessionID) (types.Optional[bool], error) {
	s.calls++
	if s.err != nil {
		return types.None[bool](), s.err
	}
	return s.main, nil
}

type latestRequestStub struct {
	latest *model.ConsolidationRequest
	err    error
}

func (s *latestRequestStub) Save(context.Context, *model.ConsolidationRequest) (bool, error) {
	return false, nil
}

func (s *latestRequestStub) FindLatestOpen(context.Context, types.SessionID) (types.Optional[*model.ConsolidationRequest], error) {
	return types.None[*model.ConsolidationRequest](), nil
}

func (s *latestRequestStub) FindLatest(context.Context, types.SessionID) (types.Optional[*model.ConsolidationRequest], error) {
	if s.err != nil {
		return types.None[*model.ConsolidationRequest](), s.err
	}
	if s.latest == nil {
		return types.None[*model.ConsolidationRequest](), nil
	}
	return types.Some(s.latest), nil
}

func (s *latestRequestStub) MarkRefineOutcome(context.Context, model.ConsolidationRefineStamp) (bool, error) {
	return false, nil
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
	stamped, err := model.NewConsolidationRequest(
		sessionID, "claude", now, "evt-asked", usecase.ConsolidationSignalWork, 20, 20, false, types.ConsolidationDeliveryStopExit2,
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := usecase.ConsolidationPolicy{MinCommands: 20, StopCadence: 8}

	tests := []struct {
		name     string
		pressure *pressureRepoStub
		kind     *sessionKindStub
		repo     *sessionRefinementRepositoryStub
		requests *latestRequestStub
		policy   usecase.ConsolidationPolicy
		want     usecase.ConsolidationPressureResult
		wantKind int
		wantErr  bool
		assert   func(t *testing.T, pressure *pressureRepoStub)
	}{
		{
			name:     "subagent session is never due",
			pressure: &pressureRepoStub{commands: 100},
			kind:     &sessionKindStub{main: types.Some(false)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{},
			policy:   policy,
			want:     usecase.ConsolidationPressureResult{Skipped: "subagent"},
			wantKind: 1,
			assert: func(t *testing.T, pressure *pressureRepoStub) {
				t.Helper()
				if len(pressure.calls) != 0 {
					t.Fatalf("CountKindAfter calls = %d, want 0", len(pressure.calls))
				}
			},
		},
		{
			name:     "missing session row is treated as main",
			pressure: &pressureRepoStub{commands: 20},
			kind:     &sessionKindStub{main: types.None[bool]()},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Due:      true,
				Signal:   usecase.ConsolidationSignalWork,
				Commands: 20,
			},
			wantKind: 1,
		},
		{
			name:     "below min_commands is not due",
			pressure: &pressureRepoStub{commands: 19},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Commands: 19,
				Skipped:  "min_commands",
			},
			wantKind: 1,
		},
		{
			name:     "commands reached and no prior request is due on first eligible stop",
			pressure: &pressureRepoStub{commands: 20},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Due:      true,
				Signal:   usecase.ConsolidationSignalWork,
				Commands: 20,
			},
			wantKind: 1,
		},
		{
			name:     "commands reached but fewer than stop_cadence transcripts since last request is not due",
			pressure: &pressureRepoStub{commands: 20, transcripts: 3},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{latest: stamped},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Commands: 20,
				Stops:    3,
				Skipped:  "cadence",
			},
			wantKind: 1,
		},
		{
			name:     "stop_cadence reached asks again",
			pressure: &pressureRepoStub{commands: 20, transcripts: 8},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{latest: stamped},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Due:      true,
				Signal:   usecase.ConsolidationSignalWork,
				Commands: 20,
				Stops:    8,
			},
			wantKind: 1,
		},
		{
			name:     "cadence counts transcripts strictly after the last request whatever its refine outcome",
			pressure: &pressureRepoStub{commands: 20, transcripts: 8},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{latest: stamped},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Due:      true,
				Signal:   usecase.ConsolidationSignalWork,
				Commands: 20,
				Stops:    8,
			},
			wantKind: 1,
			assert: func(t *testing.T, pressure *pressureRepoStub) {
				t.Helper()
				if len(pressure.calls) != 2 {
					t.Fatalf("CountKindAfter calls = %d, want 2", len(pressure.calls))
				}
				if pressure.calls[1].kind != types.EventKindTranscript {
					t.Fatalf("second kind = %s, want transcript", pressure.calls[1].kind)
				}
				got, _ := pressure.calls[1].after.Value()
				if got != types.EventID("evt-asked") {
					t.Fatalf("transcript after = %s, want evt-asked", got)
				}
			},
		},
		{
			name:     "min_commands 0 disables",
			pressure: &pressureRepoStub{commands: 100},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{},
			policy:   usecase.ConsolidationPolicy{MinCommands: 0, StopCadence: 8},
			want:     usecase.ConsolidationPressureResult{Skipped: "disabled"},
			assert: func(t *testing.T, pressure *pressureRepoStub) {
				t.Helper()
				if len(pressure.calls) != 0 {
					t.Fatalf("CountKindAfter calls = %d, want 0", len(pressure.calls))
				}
			},
		},
		{
			name:     "stop_cadence 0 disables",
			pressure: &pressureRepoStub{commands: 100},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{},
			policy:   usecase.ConsolidationPolicy{MinCommands: 20, StopCadence: 0},
			want:     usecase.ConsolidationPressureResult{Skipped: "disabled"},
		},
		{
			name:     "refinement covers_to resets the command count",
			pressure: &pressureRepoStub{commands: 19},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo: &sessionRefinementRepositoryStub{
				bySession: map[types.SessionID]*model.SessionRefinement{
					sessionID: refinement,
				},
			},
			requests: &latestRequestStub{},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Commands:         19,
				Skipped:          "min_commands",
				PreviousSummary:  types.Some("previous fold summary"),
				PreviousCoversTo: types.Some(types.EventID("evt-to")),
			},
			wantKind: 1,
			assert: func(t *testing.T, pressure *pressureRepoStub) {
				t.Helper()
				if len(pressure.calls) != 1 {
					t.Fatalf("CountKindAfter calls = %d, want 1", len(pressure.calls))
				}
				got, ok := pressure.calls[0].after.Value()
				if !ok || got != types.EventID("evt-to") {
					t.Fatalf("command after = %v, want evt-to", pressure.calls[0].after)
				}
			},
		},
		{
			name:     "previous summary and covers_to travel in the result",
			pressure: &pressureRepoStub{commands: 20},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo: &sessionRefinementRepositoryStub{
				bySession: map[types.SessionID]*model.SessionRefinement{
					sessionID: refinement,
				},
			},
			requests: &latestRequestStub{},
			policy:   policy,
			want: usecase.ConsolidationPressureResult{
				Due:              true,
				Signal:           usecase.ConsolidationSignalWork,
				Commands:         20,
				PreviousSummary:  types.Some("previous fold summary"),
				PreviousCoversTo: types.Some(types.EventID("evt-to")),
			},
			wantKind: 1,
		},
		{
			name:     "a request lookup failure returns an error",
			pressure: &pressureRepoStub{commands: 20},
			kind:     &sessionKindStub{main: types.Some(true)},
			repo:     &sessionRefinementRepositoryStub{},
			requests: &latestRequestStub{err: errors.New("locked")},
			policy:   policy,
			wantErr:  true,
			wantKind: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sut := usecase.NewConsolidationPressureUsecase(tt.pressure, tt.repo, tt.kind, tt.requests)
			got, err := sut.Check(context.Background(), sessionID, tt.policy)
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
			if tt.wantKind != 0 && tt.kind.calls != tt.wantKind {
				t.Fatalf("IsMainSession calls = %d, want %d", tt.kind.calls, tt.wantKind)
			}
			if tt.assert != nil {
				tt.assert(t, tt.pressure)
			}
		})
	}
}
