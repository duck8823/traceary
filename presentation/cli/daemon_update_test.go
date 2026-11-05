package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type updateCLIClient struct {
	daemonStub
	status                    app.UpdateStatus
	binding                   domain.ShutdownBindingState
	target                    domain.UpdateTarget
	reads, attempts, handoffs int
	failure                   error
}

func (c *updateCLIClient) UpdateStatus(_ context.Context, root, epoch string) (app.UpdateStatus, error) {
	c.root = root
	c.reads++
	if epoch != c.status.Epoch {
		return app.UpdateStatus{}, domain.ErrChanged
	}
	return c.status, nil
}
func (c *updateCLIClient) AttemptUpdate(_ context.Context, root string, r app.UpdateAttemptRequest) (app.UpdateAttemptResult, error) {
	c.root = root
	c.attempts++
	c.target = r.Target
	result := app.UpdateAttemptResult{Target: r.Target, Status: c.status, Notice: true}
	if c.status.Quiescent {
		result.Preview = types.Some(c.binding)
	}
	return result, c.failure
}
func (c *updateCLIClient) HandoffUpdate(_ context.Context, root string, r app.UpdateHandoffRequest) (app.ShutdownResult, error) {
	c.root = root
	c.handoffs++
	if !r.Valid() {
		return app.ShutdownResult{}, domain.ErrInvalid
	}
	outcome := domain.ShutdownDraining
	if c.failure != nil {
		outcome = domain.ShutdownUnknown
	}
	return app.ShutdownResult{Binding: r.Binding, Outcome: outcome}, c.failure
}
func updateCLIFixture(t *testing.T) *updateCLIClient {
	t.Helper()
	epoch := strings.Repeat("a", 32)
	digest, err := app.ShutdownRosterDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &updateCLIClient{status: app.UpdateStatus{Epoch: epoch, Quiescent: true, Blockers: []string{}}, binding: domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), Revision: 3, RosterDigest: digest}}
}
func TestDaemonUpdateCLIStatusAttemptAndUnknownHandoffNeverTouchCoreOrReplay(t *testing.T) {
	client := updateCLIFixture(t)
	for _, kind := range []string{"status", "attempt", "handoff"} {
		cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(client), cli.WithDatabasePathSetter(func(string) { t.Fatal("update touched core selection") })).Command()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		args := []string{"daemon", "update", kind, "--runtime-dir", "/private/runtime", "--epoch", client.status.Epoch}
		if kind != "status" {
			args = append(args, "--target-version", "v2", "--target-protocol", "2")
		}
		if kind == "handoff" {
			args = append(args, "--preview-id", client.binding.PreviewID, "--revision", "3", "--roster-digest", client.binding.RosterDigest, "--total-runs", "0", "--pending-launches", "0", "--pending-controls", "0", "--pending-reconciliations", "0", "--acknowledge-daemon-exit")
			client.failure = app.ErrShutdownUnknown
		}
		cmd.SetArgs(args)
		err := cmd.Execute()
		if kind == "handoff" {
			if !errors.Is(err, app.ErrShutdownUnknown) || !strings.Contains(out.String(), `"unknown"`) {
				t.Fatal("unknown handoff hidden", err, out.String())
			}
		} else if err != nil {
			t.Fatal(err, out.String())
		}
		if !strings.Contains(out.String(), "not_installation") {
			t.Fatal("update output lacks authority boundary", out.String())
		}
	}
	if client.reads != 1 || client.attempts != 1 || client.handoffs != 1 || client.serves != 0 || client.statuses != 0 {
		t.Fatal("update auto-started/retried/queried unrelated transport")
	}
}
func TestDaemonUpdateHandoffMissingConsentAndNoncanonicalCountersRefuseBeforePort(t *testing.T) {
	for _, tc := range []struct{ ack, revision string }{{"", "3"}, {"--acknowledge-daemon-exit=false", "3"}, {"--acknowledge-daemon-exit", "03"}} {
		client := updateCLIFixture(t)
		cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(client)).Command()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		args := []string{"daemon", "update", "handoff", "--epoch", client.status.Epoch, "--target-version", "v2", "--target-protocol", "2", "--preview-id", client.binding.PreviewID, "--revision", tc.revision, "--roster-digest", client.binding.RosterDigest, "--total-runs", "0", "--pending-launches", "0", "--pending-controls", "0", "--pending-reconciliations", "0"}
		if tc.ack != "" {
			args = append(args, tc.ack)
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || client.handoffs != 0 {
			t.Fatal("invalid/cancelled consent reached port", err)
		}
	}
}

func TestDaemonUpdateAttemptKnownBusyStillPrintsNoticeWithoutReadyPreview(t *testing.T) {
	client := updateCLIFixture(t)
	client.failure = app.ErrRuntimeBusy
	client.status.Quiescent = false
	client.status.Blockers = []string{"lifecycle_busy"}
	cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(client)).Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"daemon", "update", "attempt", "--epoch", client.status.Epoch, "--target-version", "v2", "--target-protocol", "2"})
	if err := cmd.Execute(); !errors.Is(err, app.ErrRuntimeBusy) || !strings.Contains(out.String(), `"notice":true`) || strings.Contains(out.String(), `"preview"`) {
		t.Fatal("known busy hid notice or granted readiness", err, out.String())
	}
}
