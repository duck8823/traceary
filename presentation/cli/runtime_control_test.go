package cli_test

import (
	"bytes"
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/presentation/cli"
	"strings"
	"testing"
)

type terminalClientFixture struct {
	runtimeClientFixture
	controlCalls, receiptCalls int
	received                   []byte
	intent                     domain.ControlIntentState
	outcome                    domain.ControlOutcome
	written                    int
	err                        error
}

func (c *terminalClientFixture) Control(_ context.Context, _ string, r app.ControlRequest) (app.ControlResult, error) {
	c.controlCalls++
	c.received = append([]byte(nil), r.Data...)
	c.intent = r.Intent.Snapshot()
	outcome := c.outcome
	if outcome == "" {
		outcome = domain.ControlDelivered
	}
	return app.ControlResult{Selection: r.Selection, Receipt: domain.ControlSnapshot{Identity: r.Selection.Identity, Intent: r.Intent.Snapshot(), Outcome: outcome, WrittenBytes: c.written}}, c.err
}
func (c *terminalClientFixture) Receipt(_ context.Context, _ string, s app.OutputSelection, id string) (app.ControlResult, error) {
	c.receiptCalls++
	return app.ControlResult{Selection: s, Receipt: domain.ControlSnapshot{Identity: s.Identity, Intent: domain.ControlIntentState{RequestID: id, Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 5}, Outcome: domain.ControlUnknown, WrittenBytes: 1}}, nil
}
func terminalArgs(kind string) []string {
	return []string{"runtime", kind, "--runtime-dir", "/private/runtime", "--epoch", strings.Repeat("1", 32), "--run", strings.Repeat("2", 32), "--generation", strings.Repeat("3", 32), "--worktree", "w_" + strings.Repeat("a", 64), "--profile", "codex", "--request-id", "manual-1"}
}
func TestRuntimeControlCLIStdinUnknownPrefixNeverEchoesInput(t *testing.T) {
	client := &terminalClientFixture{outcome: domain.ControlUnknown, written: 1, err: app.ErrControlUnknown}
	cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	raw := []byte("synthetic-private-input\x1b\xff")
	cmd.SetIn(bytes.NewReader(raw))
	cmd.SetArgs(append(terminalArgs("input"), "--manual-terminal"))
	if err := cmd.Execute(); !errors.Is(err, app.ErrControlUnknown) {
		t.Fatalf("unknown input result: %v", err)
	}
	if client.controlCalls != 1 || !bytes.Equal(client.received, raw) || client.intent.InputBytes != len(raw) || !client.intent.ManualTerminal {
		t.Fatal("stdin bytes changed or intent lost")
	}
	if strings.Contains(output.String(), "synthetic-private-input") || !strings.Contains(output.String(), `"written_bytes":"1"`) || !strings.Contains(output.String(), `"outcome":"unknown"`) {
		t.Fatal("unknown receipt lost safe prefix or echoed input")
	}
}
func TestRuntimeControlCLIRejectsMissingIntentEmptyOversizeAndPayloadArgv(t *testing.T) {
	for _, mode := range []string{"missing_manual", "false_manual", "empty", "oversize", "payload_argv"} {
		t.Run(mode, func(t *testing.T) {
			client := &terminalClientFixture{}
			cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			args := terminalArgs("input")
			data := []byte("synthetic-private-stdin")
			if mode != "missing_manual" {
				args = append(args, "--manual-terminal")
			}
			if mode == "false_manual" {
				args[len(args)-1] = "--manual-terminal=false"
			}
			if mode == "empty" {
				data = nil
			}
			if mode == "oversize" {
				data = bytes.Repeat([]byte("x"), domain.MaxControlInputBytes+1)
			}
			if mode == "payload_argv" {
				args = append(args, "synthetic-private-argv")
			}
			cmd.SetArgs(args)
			cmd.SetIn(bytes.NewReader(data))
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid explicit input accepted")
			}
			if client.controlCalls != 0 || strings.Contains(out.String(), "synthetic-private-stdin") || strings.Contains(out.String(), "synthetic-private-argv") {
				t.Fatal("invalid input forwarded or echoed")
			}
		})
	}
}
func TestRuntimeControlCLIResizeStopAndReadOnlyReceiptContracts(t *testing.T) {
	for _, kind := range []string{"resize", "stop", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			client := &terminalClientFixture{}
			cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			args := terminalArgs(kind)
			if kind == "resize" {
				args = append(args, "--sequence", "9007199254740993", "--rows", "31", "--cols", "101")
			}
			if kind == "stop" {
				args = append(args, "--acknowledge-work-loss")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if kind == "receipt" {
				if client.controlCalls != 0 || client.receiptCalls != 1 {
					t.Fatal("receipt query caused mutation")
				}
			} else if client.controlCalls != 1 {
				t.Fatal("explicit control not sent")
			}
			if kind == "resize" && client.intent.ResizeSequence != 9007199254740993 {
				t.Fatal("resize counter rounded")
			}
			if kind == "stop" && !client.intent.WorkLossAcknowledged {
				t.Fatal("work-loss acknowledgment lost")
			}
		})
	}
}
