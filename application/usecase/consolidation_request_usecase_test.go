package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type fixedConsolidationClock struct{ at time.Time }

func (c fixedConsolidationClock) Now() time.Time { return c.at }

type consolidationRequestRepoStub struct {
	open     *model.ConsolidationRequest
	saved    []*model.ConsolidationRequest
	saveOK   bool
	saveErr  error
	stamp    model.ConsolidationRefineStamp
	stampOK  bool
	stampErr error
	findErr  error
}

func (s *consolidationRequestRepoStub) Save(_ context.Context, request *model.ConsolidationRequest) (bool, error) {
	if s.saveErr != nil {
		return false, s.saveErr
	}
	s.saved = append(s.saved, request)
	return s.saveOK, nil
}

func (s *consolidationRequestRepoStub) FindLatestOpen(
	_ context.Context,
	_ types.SessionID,
) (types.Optional[*model.ConsolidationRequest], error) {
	if s.findErr != nil {
		return types.None[*model.ConsolidationRequest](), s.findErr
	}
	if s.open == nil {
		return types.None[*model.ConsolidationRequest](), nil
	}
	return types.Some(s.open), nil
}

func (s *consolidationRequestRepoStub) FindLatest(
	ctx context.Context,
	sessionID types.SessionID,
) (types.Optional[*model.ConsolidationRequest], error) {
	return s.FindLatestOpen(ctx, sessionID)
}

func (s *consolidationRequestRepoStub) MarkRefineOutcome(_ context.Context, stamp model.ConsolidationRefineStamp) (bool, error) {
	s.stamp = stamp
	if s.stampErr != nil {
		return false, s.stampErr
	}
	return s.stampOK, nil
}

func TestConsolidationRequestUsecase_RecordDerivesReRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	open, err := model.NewConsolidationRequest(
		"sess-1", "claude", now.Add(-time.Minute), "evt-old", "body_bytes", 10, 100, false, types.ConsolidationDeliveryStopExit2,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		open      *model.ConsolidationRequest
		wantRe    bool
		saveOK    bool
		wantSaved bool
	}{
		{name: "no open row is first request", saveOK: true, wantSaved: true},
		{name: "existing open row is re-request", open: open, wantRe: true, saveOK: true, wantSaved: true},
		{name: "duplicate key reports Recorded false", saveOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &consolidationRequestRepoStub{open: tt.open, saveOK: tt.saveOK}
			sut := usecase.NewConsolidationRequestUsecase(repo, fixedConsolidationClock{at: now})
			got, err := sut.Record(ctx, usecase.ConsolidationRequestInput{
				SessionID:      "sess-1",
				Client:         "claude",
				AtEventID:      "evt-new",
				Signal:         usecase.ConsolidationSignalBodyBytes,
				PressureValue:  200,
				ThresholdValue: 100,
				Delivery:       types.ConsolidationDeliveryStopExit2,
			})
			if err != nil {
				t.Fatalf("Record() error = %v", err)
			}
			if got.ReRequest != tt.wantRe || got.Recorded != tt.wantSaved {
				t.Fatalf("got %+v, want recorded=%v re_request=%v", got, tt.wantSaved, tt.wantRe)
			}
			if tt.wantSaved && len(repo.saved) != 1 {
				t.Fatalf("saved %d rows, want 1", len(repo.saved))
			}
			if tt.wantSaved && repo.saved[0].ReRequest() != tt.wantRe {
				t.Fatalf("persisted re_request = %v, want %v", repo.saved[0].ReRequest(), tt.wantRe)
			}
		})
	}
}

func TestConsolidationRefineFromSessionOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome model.SessionRefineOutcome
		want    types.ConsolidationRefineOutcome
		reason  string
	}{
		{name: "created is accepted", outcome: model.SessionRefineOutcomeCreated, want: types.ConsolidationRefineAccepted, reason: usecase.ConsolidationReasonCreated},
		{name: "superseded is accepted", outcome: model.SessionRefineOutcomeSuperseded, want: types.ConsolidationRefineAccepted, reason: usecase.ConsolidationReasonSuperseded},
		{name: "unchanged is rejected", outcome: model.SessionRefineOutcomeUnchanged, want: types.ConsolidationRefineRejected, reason: usecase.ConsolidationReasonUnchanged},
		{name: "unknown maps to usecase_error", outcome: model.SessionRefineOutcome("boom"), want: types.ConsolidationRefineRejected, reason: usecase.ConsolidationReasonUsecaseError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, reason := usecase.ConsolidationRefineFromSessionOutcome(tt.outcome)
			if got != tt.want || reason != tt.reason {
				t.Fatalf("got %s/%s, want %s/%s", got, reason, tt.want, tt.reason)
			}
			if strings.Contains(reason, " ") || strings.Contains(reason, ":") {
				t.Fatalf("reason looks like an error string: %q", reason)
			}
		})
	}
}

func TestConsolidationRequestUsecase_RecordRefineOutcomeUsesClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	repo := &consolidationRequestRepoStub{stampOK: true}
	sut := usecase.NewConsolidationRequestUsecase(repo, fixedConsolidationClock{at: now})
	ok, err := sut.RecordRefineOutcome(context.Background(), model.ConsolidationRefineStamp{
		SessionID:  "sess-1",
		Outcome:    types.ConsolidationRefineAccepted,
		Reason:     usecase.ConsolidationReasonCreated,
		ProducedBy: "agent",
		Generation: types.Some(1),
	})
	if err != nil || !ok {
		t.Fatalf("RecordRefineOutcome() ok=%v err=%v", ok, err)
	}
	if !repo.stamp.At.Equal(now) {
		t.Fatalf("stamp.At = %s, want %s", repo.stamp.At, now)
	}
	if repo.stamp.Reason != usecase.ConsolidationReasonCreated {
		t.Fatalf("reason = %q", repo.stamp.Reason)
	}
}
