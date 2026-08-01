package cli

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

type neverCalledRehearsalBackend struct{}

func (neverCalledRehearsalBackend) unexpected() {
	panic("invalid CLI config reached rehearsal backend")
}
func (b neverCalledRehearsalBackend) Preview(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (b neverCalledRehearsalBackend) Prepare(context.Context, apptypes.PayloadRehearsalConfig, apptypes.PayloadRehearsalRunCommand) (application.PayloadRehearsalRunHandle, apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return nil, apptypes.PayloadRehearsalMetrics{}, nil
}
func (b neverCalledRehearsalBackend) AdvanceField(context.Context, application.PayloadRehearsalRunHandle, apptypes.PayloadRehearsalField) (apptypes.PayloadRehearsalMetrics, bool, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, false, nil
}
func (b neverCalledRehearsalBackend) Pause(context.Context, application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (b neverCalledRehearsalBackend) Complete(context.Context, application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (b neverCalledRehearsalBackend) Close(application.PayloadRehearsalRunHandle) error {
	b.unexpected()
	return nil
}
func (b neverCalledRehearsalBackend) PrepareScrub(context.Context, apptypes.PayloadRehearsalConfig) (application.PayloadRehearsalScrubHandle, apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return nil, apptypes.PayloadRehearsalMetrics{}, nil
}
func (b neverCalledRehearsalBackend) AdvanceScrubField(context.Context, application.PayloadRehearsalScrubHandle, apptypes.PayloadRehearsalField) (apptypes.PayloadRehearsalMetrics, bool, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, false, nil
}
func (b neverCalledRehearsalBackend) CompleteScrub(context.Context, application.PayloadRehearsalScrubHandle) (apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (b neverCalledRehearsalBackend) ReleaseScrub(context.Context, application.PayloadRehearsalScrubHandle) error {
	b.unexpected()
	return nil
}
func (b neverCalledRehearsalBackend) CloseScrub(application.PayloadRehearsalScrubHandle) error {
	b.unexpected()
	return nil
}
func (b neverCalledRehearsalBackend) Rollback(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	b.unexpected()
	return apptypes.PayloadRehearsalMetrics{}, nil
}

type readinessRehearsalStub struct{}

func (readinessRehearsalStub) Preview(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (readinessRehearsalStub) Run(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (readinessRehearsalStub) Resume(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (readinessRehearsalStub) Scrub(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	return apptypes.PayloadRehearsalMetrics{}, nil
}
func (readinessRehearsalStub) Rollback(context.Context, apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	return apptypes.PayloadRehearsalMetrics{State: "rolled_back", RollbackVerified: true, ActivationReadiness: apptypes.PayloadActivationReadiness{ScrubStatus: apptypes.ReadinessUnknown}}, nil
}

func TestPayloadRehearsalExposesNoActivationCommand(t *testing.T) {
	group := NewRootCLI().newStorePayloadRehearsalCommand()
	want := map[string]bool{"preview": true, "run": true, "resume": true, "scrub": true, "rollback": true}
	for _, command := range group.Commands() {
		if command.Name() == "activate" {
			t.Fatal("v0.34 must not expose activation")
		}
		delete(want, command.Name())
		if command.Name() != "preview" && command.Name() != "scrub" && command.Flags().Lookup("backup") == nil {
			t.Fatalf("%s lacks rollback artifact flag", command.Name())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing rehearsal commands: %v", want)
	}
}

func TestRollbackJSONDoesNotClaimUnperformedScrub(t *testing.T) {
	root := NewRootCLI(WithPayloadRehearsal(readinessRehearsalStub{}))
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := root.runPayloadRehearsal(cmd, "rollback", apptypes.PayloadRehearsalConfig{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"scrub_status": "unknown"`) || strings.Contains(output.String(), `"scrub_passed": true`) {
		t.Fatalf("output=%s", output.String())
	}
}

func TestPayloadRehearsalCLIRejectsUnboundedBatchWithoutWritesOrPanic(t *testing.T) {
	dir := t.TempDir()
	target, live, backup := filepath.Join(dir, "target.db"), filepath.Join(dir, "live.db"), filepath.Join(dir, "backup.db")
	for _, path := range []string{target, live} {
		if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := neverCalledRehearsalBackend{}
	root := NewRootCLI(WithPayloadRehearsal(usecase.NewPayloadRehearsalUsecase(b, b, b, b)))
	cmd := root.newStorePayloadRehearsalCommand()
	cmd.SetArgs([]string{"run", "--target", target, "--live-db", live, "--backup", backup, "--batch-rows", strconv.Itoa(math.MaxInt)})
	if err := cmd.Execute(); !errors.Is(err, usecase.ErrInvalidPayloadRehearsalConfig) {
		t.Fatalf("error = %v", err)
	}
	for _, path := range []string{target, live} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "sentinel" {
			t.Fatalf("%s changed: %q %v", path, got, err)
		}
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup created: %v", err)
	}
}
