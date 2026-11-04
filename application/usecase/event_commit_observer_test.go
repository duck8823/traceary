package usecase_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// This repository models only the committed-write contract; no native/SQL authority.
type observerEventRepository struct {
	commits    atomic.Int64
	failure    error
	redelivery bool
}

func (r *observerEventRepository) Save(_ context.Context, e *model.Event) error {
	if r.failure != nil {
		return r.failure
	}
	if r.redelivery {
		e.RecordPersistOutcome(false, "canonical-existing")
	}
	r.commits.Add(1)
	return nil
}
func (r *observerEventRepository) SaveWithAudit(ctx context.Context, e *model.Event, _ *model.CommandAudit) error {
	return r.Save(ctx, e)
}
func (r *observerEventRepository) DeleteTranscript(context.Context, types.EventID) error {
	if r.failure != nil {
		return r.failure
	}
	r.commits.Add(1)
	return nil
}

type eventObserverFunc func(context.Context)

func (f eventObserverFunc) NotifyCommittedHistory(ctx context.Context) { f(ctx) }
func observerLog(ctx context.Context, u usecase.EventUsecase) (apptypes.EventWriteResult, error) {
	result, err := u.Log(ctx, "synthetic body", types.EventKindNote, "cli", "fixture", "session", "/fixture", apptypes.LogRedaction{})
	if err != nil {
		return result, xerrors.Errorf("fixture log: %w", err)
	}
	return result, nil
}

func TestEventCommitObserverRunsOnlyAfterSuccessfulWritesAndPreservesCanonicalResults(t *testing.T) {
	r := &observerEventRepository{}
	var notifications atomic.Int64
	u := usecase.NewEventUsecase(r, nil, usecase.WithEventCommitObserver(eventObserverFunc(func(ctx context.Context) {
		if r.commits.Load() == 0 {
			t.Error("notification preceded commit")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 25*time.Millisecond {
			t.Error("unbounded notification context")
		}
		notifications.Add(1)
	})))
	result, err := observerLog(context.Background(), u)
	if err != nil || !result.Inserted() || notifications.Load() != 1 {
		t.Fatal("successful log changed", err)
	}
	r.redelivery = true
	result, err = observerLog(context.Background(), u)
	if err != nil || result.Inserted() || result.EventID() != "canonical-existing" || notifications.Load() != 2 {
		t.Fatal("canonical redelivery changed", err)
	}
	audit, command, err := u.Audit(context.Background(), apptypes.AuditInput{Command: "fixture", Client: "cli", Agent: "fixture", SessionID: "session", Workspace: "/fixture"}, apptypes.AuditRedaction{})
	if err != nil || audit.Inserted() || audit.EventID() != "canonical-existing" || command.EventID() != audit.EventID() || notifications.Load() != 3 {
		t.Fatal("audit identity changed", err)
	}
	if err = u.DeleteTranscript(context.Background(), "transcript"); err != nil || notifications.Load() != 4 {
		t.Fatal("delete not notified", err)
	}
	r.failure = errors.New("fixture rollback")
	if _, err = observerLog(context.Background(), u); !errors.Is(err, r.failure) {
		t.Fatal(err)
	}
	if _, _, err = u.Audit(context.Background(), apptypes.AuditInput{Command: "fixture", Client: "cli", Agent: "fixture", SessionID: "session", Workspace: "/fixture"}, apptypes.AuditRedaction{}); !errors.Is(err, r.failure) {
		t.Fatal(err)
	}
	if err = u.DeleteTranscript(context.Background(), "transcript"); !errors.Is(err, r.failure) || notifications.Load() != 4 {
		t.Fatal("failed write emitted hint", err)
	}
}

func TestEventCommitObserverCannotBlockOrFanOutSuccessfulCapture(t *testing.T) {
	r := &observerEventRepository{}
	entered := make(chan struct{})
	release := make(chan struct{})
	joined := make(chan struct{})
	var calls atomic.Int64
	u := usecase.NewEventUsecase(r, nil, usecase.WithEventCommitObserver(eventObserverFunc(func(context.Context) { calls.Add(1); close(entered); <-release; close(joined) })))
	result, err := observerLog(context.Background(), u)
	if err != nil || !result.Inserted() {
		close(release)
		t.Fatal("hung observer changed committed outcome", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("observer not admitted")
	}
	for range 20 {
		if _, err = observerLog(context.Background(), u); err != nil {
			close(release)
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || r.commits.Load() != 21 {
		close(release)
		t.Fatal("hung observer fanned out or blocked writes")
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("owned observer did not finish")
	}
}

func TestEventCommitObserverPanicAndCanceledContextNeverAlterCommittedWrite(t *testing.T) {
	r := &observerEventRepository{}
	var calls atomic.Int64
	u := usecase.NewEventUsecase(r, nil, usecase.WithEventCommitObserver(eventObserverFunc(func(context.Context) { calls.Add(1); panic("synthetic optional observer failure") })))
	if _, err := observerLog(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("panic fixture not exercised")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	// The fake repository deliberately models a commit that completed despite
	// caller cancellation. Optional delivery must not reinterpret it as failure.
	if result, err := observerLog(canceled, u); err != nil || !result.Inserted() || calls.Load() != 1 {
		t.Fatal("cancellation changed committed outcome", err)
	}
	if _, err := observerLog(context.Background(), usecase.NewEventUsecase(r, nil)); err != nil {
		t.Fatal("nil observer changed existing behavior", err)
	}
}
