package cli

import (
	"bytes"
	"context"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

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
