package usecase_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/xerrors"
)

type rehearsalBackendFake struct {
	preview              apptypes.PayloadRehearsalMetrics
	run, scrub, rollback apptypes.PayloadRehearsalMetrics
	calls                []string
	fields               []apptypes.PayloadRehearsalField
	driftRunAdvance      bool
	driftRunComplete     bool
	driftScrubAdvance    bool
	advancesRemaining    int
	cancelAfterAdvance   func()
	pauseContextErr      error
	pauseBlocks          bool
	releaseBlocks        bool
	cleanupStarted       chan struct{}
}
type fakeRunHandle struct{}
type fakeScrubHandle struct{}

func (*fakeRunHandle) PayloadRehearsalRunHandle()     {}
func (*fakeScrubHandle) PayloadRehearsalScrubHandle() {}

func (f *rehearsalBackendFake) Preview(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	f.calls = append(f.calls, "preview")
	return f.preview, nil
}
func (f *rehearsalBackendFake) Prepare(_ context.Context, _ apptypes.PayloadRehearsalConfig, command apptypes.PayloadRehearsalRunCommand) (application.PayloadRehearsalRunHandle, apptypes.PayloadRehearsalMetrics, error) {
	if command.IsResume() {
		f.calls = append(f.calls, "resume")
	} else {
		f.calls = append(f.calls, "run")
	}
	return &fakeRunHandle{}, f.run, nil
}
func (f *rehearsalBackendFake) AdvanceField(_ context.Context, _ application.PayloadRehearsalRunHandle, field apptypes.PayloadRehearsalField) (apptypes.PayloadRehearsalMetrics, bool, error) {
	f.fields = append(f.fields, field)
	if f.driftRunAdvance {
		f.run.LiveIdentityOnly = false
	}
	if f.advancesRemaining > 0 {
		f.advancesRemaining--
		f.run.BatchCount++
		f.run.EncodedRows++
		if f.cancelAfterAdvance != nil {
			f.cancelAfterAdvance()
		}
		return f.run, false, nil
	}
	return f.run, true, nil
}

func TestPayloadRehearsalStopsAfterCommittedBatchBoundary(t *testing.T) {
	backend := &rehearsalBackendFake{
		preview: apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: true, LiveIdentityOnly: true, FreeBytes: 100, EstimatedHeadroom: 50},
		run:     apptypes.PayloadRehearsalMetrics{State: "running", LiveIdentityOnly: true},
	}
	backend.advancesRemaining = 3
	config := validRehearsalConfig()
	config.StopAfterBatches = 2
	result, err := usecase.NewPayloadRehearsalUsecase(backend, backend, backend, backend).Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "paused" || result.BatchCount != 2 || result.EncodedRows != 2 || !result.MorePending {
		t.Fatalf("unexpected deterministic stop result: %+v", result)
	}
}
func (f *rehearsalBackendFake) Pause(ctx context.Context, _ application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	if f.pauseBlocks {
		close(f.cleanupStarted)
		<-ctx.Done()
		return f.run, xerrors.Errorf("pause cleanup context: %w", ctx.Err())
	}
	f.pauseContextErr = ctx.Err()
	f.run.State = "paused"
	return f.run, nil
}

func TestPayloadRehearsalCommittedStopPersistsPauseAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &rehearsalBackendFake{
		preview:            apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: true, LiveIdentityOnly: true, FreeBytes: 100, EstimatedHeadroom: 50},
		run:                apptypes.PayloadRehearsalMetrics{State: "running", LiveIdentityOnly: true},
		advancesRemaining:  2,
		cancelAfterAdvance: cancel,
	}
	config := validRehearsalConfig()
	config.StopAfterBatches = 1
	result, err := usecase.NewPayloadRehearsalUsecase(backend, backend, backend, backend).Run(ctx, config)
	if err != nil || result.State != "paused" || !result.MorePending || backend.pauseContextErr != nil {
		t.Fatalf("result=%+v pause_ctx=%v err=%v", result, backend.pauseContextErr, err)
	}
}
func (f *rehearsalBackendFake) Complete(context.Context, application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	f.run.State = "completed"
	if f.driftRunComplete {
		f.run.LiveIdentityOnly = false
	}
	return f.run, nil
}
func (f *rehearsalBackendFake) Close(application.PayloadRehearsalRunHandle) error {
	f.calls = append(f.calls, "close")
	return nil
}
func (f *rehearsalBackendFake) PrepareScrub(context.Context, apptypes.PayloadRehearsalConfig) (application.PayloadRehearsalScrubHandle, apptypes.PayloadRehearsalMetrics, error) {
	f.calls = append(f.calls, "scrub")
	return &fakeScrubHandle{}, f.scrub, nil
}
func (f *rehearsalBackendFake) AdvanceScrubField(context.Context, application.PayloadRehearsalScrubHandle, apptypes.PayloadRehearsalField) (apptypes.PayloadRehearsalMetrics, bool, error) {
	if f.driftScrubAdvance {
		f.scrub.LiveIdentityOnly = false
	}
	return f.scrub, true, nil
}
func (f *rehearsalBackendFake) CompleteScrub(context.Context, application.PayloadRehearsalScrubHandle) (apptypes.PayloadRehearsalMetrics, error) {
	f.scrub.State = "scrubbed"
	f.scrub.ActivationReadiness.ScrubPassed = true
	return f.scrub, nil
}
func (f *rehearsalBackendFake) ReleaseScrub(ctx context.Context, _ application.PayloadRehearsalScrubHandle) error {
	if f.releaseBlocks {
		close(f.cleanupStarted)
		<-ctx.Done()
		return xerrors.Errorf("release cleanup context: %w", ctx.Err())
	}
	return nil
}
func (*rehearsalBackendFake) CloseScrub(application.PayloadRehearsalScrubHandle) error { return nil }
func (f *rehearsalBackendFake) Rollback(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	f.calls = append(f.calls, "rollback")
	return f.rollback, nil
}

func TestPayloadRehearsalUsecaseOwnsPreflightAndTransitionPolicy(t *testing.T) {
	f := &rehearsalBackendFake{preview: apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: true, LiveIdentityOnly: true, FreeBytes: 100, EstimatedHeadroom: 50}, run: apptypes.PayloadRehearsalMetrics{State: "completed", LiveIdentityOnly: true}, scrub: apptypes.PayloadRehearsalMetrics{State: "scrubbed", LiveIdentityOnly: true, ActivationReadiness: apptypes.PayloadActivationReadiness{ScrubPassed: true}}, rollback: apptypes.PayloadRehearsalMetrics{State: "rolled_back", LiveIdentityOnly: true, RollbackVerified: true}}
	u := usecase.NewPayloadRehearsalUsecase(f, f, f, f)
	c := validRehearsalConfig()
	if _, err := u.Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 || f.calls[0] != "preview" || f.calls[1] != "run" || f.calls[2] != "close" {
		t.Fatalf("calls=%v", f.calls)
	}
	if want := apptypes.OrderedPayloadRehearsalFields(); !reflect.DeepEqual(f.fields, want) {
		t.Fatalf("field order=%v want=%v", f.fields, want)
	}
	if _, err := u.Scrub(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Rollback(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func TestPayloadRehearsalUsecaseRejectsUnsafeBackendTransitions(t *testing.T) {
	f := &rehearsalBackendFake{preview: apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: false, LiveIdentityOnly: true}}
	u := usecase.NewPayloadRehearsalUsecase(f, f, f, f)
	if _, err := u.Preview(context.Background(), validRehearsalConfig()); !errors.Is(err, usecase.ErrUnsafePayloadRehearsalTransition) {
		t.Fatalf("error=%v", err)
	}
}

func TestPayloadRehearsalUsecaseRejectsUnboundedBatchBeforeBackend(t *testing.T) {
	f := &rehearsalBackendFake{}
	c := validRehearsalConfig()
	c.BatchRows = math.MaxInt
	if _, err := usecase.NewPayloadRehearsalUsecase(f, f, f, f).Run(context.Background(), c); !errors.Is(err, usecase.ErrInvalidPayloadRehearsalConfig) {
		t.Fatalf("error = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("backend calls = %v", f.calls)
	}
}

func TestPayloadRehearsalBoundsFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(*rehearsalBackendFake)
		act        func(*rehearsalBackendFake) (apptypes.PayloadRehearsalMetrics, error)
		wantResult apptypes.PayloadRehearsalMetrics
	}{
		{
			name: "run prepare live result failure",
			setup: func(f *rehearsalBackendFake) {
				f.run.LiveIdentityOnly = false
				f.pauseBlocks = true
			},
			act: func(f *rehearsalBackendFake) (apptypes.PayloadRehearsalMetrics, error) {
				return usecase.NewPayloadRehearsalUsecase(f, f, f, f).Run(context.Background(), validRehearsalConfig())
			},
			wantResult: apptypes.PayloadRehearsalMetrics{State: "running", LiveIdentityOnly: false},
		},
		{
			name: "run advance live result failure",
			setup: func(f *rehearsalBackendFake) {
				f.driftRunAdvance = true
				f.pauseBlocks = true
			},
			act: func(f *rehearsalBackendFake) (apptypes.PayloadRehearsalMetrics, error) {
				return usecase.NewPayloadRehearsalUsecase(f, f, f, f).Resume(context.Background(), validRehearsalConfig())
			},
			wantResult: apptypes.PayloadRehearsalMetrics{State: "running", LiveIdentityOnly: false},
		},
		{
			name: "scrub prepare live result failure",
			setup: func(f *rehearsalBackendFake) {
				f.scrub.LiveIdentityOnly = false
				f.releaseBlocks = true
			},
			act: func(f *rehearsalBackendFake) (apptypes.PayloadRehearsalMetrics, error) {
				return usecase.NewPayloadRehearsalUsecase(f, f, f, f).Scrub(context.Background(), validRehearsalConfig())
			},
			wantResult: apptypes.PayloadRehearsalMetrics{State: "scrubbing", LiveIdentityOnly: false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &rehearsalBackendFake{
				preview:        apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: true, LiveIdentityOnly: true, FreeBytes: 100, EstimatedHeadroom: 50},
				run:            apptypes.PayloadRehearsalMetrics{State: "running", LiveIdentityOnly: true},
				scrub:          apptypes.PayloadRehearsalMetrics{State: "scrubbing", LiveIdentityOnly: true},
				cleanupStarted: make(chan struct{}),
			}
			tc.setup(f)
			done := make(chan struct{})
			var got apptypes.PayloadRehearsalMetrics
			var err error
			go func() {
				got, err = tc.act(f)
				close(done)
			}()
			select {
			case <-f.cleanupStarted:
			case <-time.After(100 * time.Millisecond):
				t.Fatal("cleanup call was not reached")
			}
			select {
			case <-done:
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("cleanup call did not return within the one-second bound")
			}
			want := tc.wantResult
			want.ActivationReadiness = application.EvaluatePayloadActivationReadiness(want)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("result mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(usecase.ErrUnsafePayloadRehearsalTransition, err, cmpopts.EquateErrors()); diff != "" {
				t.Fatalf("error mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

//nolint:wrapcheck // table closures intentionally preserve sentinel identity.
func TestPayloadRehearsalUsecaseStopsWhenLiveIdentityDrifts(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(*rehearsalBackendFake) error
	}{
		{"run advance", func(f *rehearsalBackendFake) error {
			f.driftRunAdvance = true
			_, err := usecase.NewPayloadRehearsalUsecase(f, f, f, f).Run(context.Background(), validRehearsalConfig())
			return err
		}},
		{"resume complete", func(f *rehearsalBackendFake) error {
			f.driftRunComplete = true
			_, err := usecase.NewPayloadRehearsalUsecase(f, f, f, f).Resume(context.Background(), validRehearsalConfig())
			return err
		}},
		{"scrub advance", func(f *rehearsalBackendFake) error {
			f.driftScrubAdvance = true
			_, err := usecase.NewPayloadRehearsalUsecase(f, f, f, f).Scrub(context.Background(), validRehearsalConfig())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &rehearsalBackendFake{
				preview: apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: true, LiveIdentityOnly: true, FreeBytes: 100, EstimatedHeadroom: 50},
				run:     apptypes.PayloadRehearsalMetrics{State: "running", LiveIdentityOnly: true},
				scrub:   apptypes.PayloadRehearsalMetrics{State: "scrubbing", LiveIdentityOnly: true},
			}
			if err := tc.act(f); !errors.Is(err, usecase.ErrUnsafePayloadRehearsalTransition) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPayloadActivationReadinessRequiresExternalEvidence(t *testing.T) {
	m := apptypes.PayloadRehearsalMetrics{State: "scrubbed", LiveIdentityOnly: true, RollbackVerified: true, FreeBytes: 100, EstimatedHeadroom: 1}
	r := application.EvaluatePayloadActivationReadiness(m)
	if r.MinimumReaderStatus != apptypes.ReadinessUnknown || r.OldProcessesStoppedStatus != apptypes.ReadinessUnknown || r.CompatibleReader || r.ActivationAllowed {
		t.Fatalf("missing external evidence passed readiness: %#v", r)
	}
}

func validRehearsalConfig() apptypes.PayloadRehearsalConfig {
	return apptypes.PayloadRehearsalConfig{TargetPath: "target", LivePath: "live", BackupPath: "backup", BatchRows: 1, StoredByteLimit: 1, DecodedByteLimit: 1, WallTimeLimit: time.Second, LockTimeLimit: time.Second, ScrubByteLimit: 1, ScrubTimeLimit: time.Second, MaxWALBytes: 1}
}
