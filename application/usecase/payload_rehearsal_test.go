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
)

type rehearsalBackendFake struct {
	preview              apptypes.PayloadRehearsalMetrics
	run, scrub, rollback apptypes.PayloadRehearsalMetrics
	calls                []string
	fields               []apptypes.PayloadRehearsalField
	driftRunAdvance      bool
	driftRunComplete     bool
	driftScrubAdvance    bool
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
	return f.run, true, nil
}
func (f *rehearsalBackendFake) Pause(context.Context, application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	f.run.State = "paused"
	return f.run, nil
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
func (*rehearsalBackendFake) ReleaseScrub(context.Context, application.PayloadRehearsalScrubHandle) error {
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
