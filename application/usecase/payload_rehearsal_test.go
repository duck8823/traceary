package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type rehearsalBackendFake struct {
	preview              apptypes.PayloadRehearsalMetrics
	run, scrub, rollback apptypes.PayloadRehearsalMetrics
	calls                []string
}

func (f *rehearsalBackendFake) Preview(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	f.calls = append(f.calls, "preview")
	return f.preview, nil
}
func (f *rehearsalBackendFake) Run(_ context.Context, _ apptypes.PayloadRehearsalConfig, command apptypes.PayloadRehearsalRunCommand) (apptypes.PayloadRehearsalMetrics, error) {
	if command.IsResume() {
		f.calls = append(f.calls, "resume")
	} else {
		f.calls = append(f.calls, "run")
	}
	return f.run, nil
}
func (f *rehearsalBackendFake) Scrub(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	f.calls = append(f.calls, "scrub")
	return f.scrub, nil
}
func (f *rehearsalBackendFake) Rollback(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	f.calls = append(f.calls, "rollback")
	return f.rollback, nil
}

func TestPayloadRehearsalUsecaseOwnsPreflightAndTransitionPolicy(t *testing.T) {
	f := &rehearsalBackendFake{preview: apptypes.PayloadRehearsalMetrics{State: "planned", DryRunZeroWrite: true, LiveIdentityOnly: true, FreeBytes: 100, EstimatedHeadroom: 50}, run: apptypes.PayloadRehearsalMetrics{State: "completed"}, scrub: apptypes.PayloadRehearsalMetrics{State: "scrubbed", ActivationReadiness: apptypes.PayloadActivationReadiness{ScrubPassed: true}}, rollback: apptypes.PayloadRehearsalMetrics{State: "rolled_back", RollbackVerified: true}}
	u := usecase.NewPayloadRehearsalUsecase(f, f)
	c := validRehearsalConfig()
	if _, err := u.Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[0] != "preview" || f.calls[1] != "run" {
		t.Fatalf("calls=%v", f.calls)
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
	u := usecase.NewPayloadRehearsalUsecase(f, f)
	if _, err := u.Preview(context.Background(), validRehearsalConfig()); !errors.Is(err, usecase.ErrUnsafePayloadRehearsalTransition) {
		t.Fatalf("error=%v", err)
	}
}

func validRehearsalConfig() apptypes.PayloadRehearsalConfig {
	return apptypes.PayloadRehearsalConfig{TargetPath: "target", LivePath: "live", BackupPath: "backup", BatchRows: 1, StoredByteLimit: 1, DecodedByteLimit: 1, WallTimeLimit: time.Second, LockTimeLimit: time.Second, ScrubByteLimit: 1, ScrubTimeLimit: time.Second, MaxWALBytes: 1}
}
