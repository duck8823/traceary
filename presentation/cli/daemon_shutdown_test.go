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

type shutdownCLIClient struct {
	daemonStub
	binding                                      domain.ShutdownBindingState
	rows                                         []app.ShutdownPreviewRun
	previews, pages, confirms, cancels, receipts int
	confirmErr                                   error
	ack                                          bool
}

func (c *shutdownCLIClient) PreviewShutdown(_ context.Context, root, epoch string) (app.ShutdownPreviewPage, error) {
	if epoch != c.binding.Epoch {
		return app.ShutdownPreviewPage{}, domain.ErrChanged
	}
	c.root = root
	c.previews++
	return app.ShutdownPreviewPage{Binding: c.binding, Runs: c.rows[:2], Next: c.rows[1].Selection.Identity.Run}, nil
}
func (c *shutdownCLIClient) ShutdownPage(_ context.Context, root string, r app.ShutdownPageRequest) (app.ShutdownPreviewPage, error) {
	c.root = root
	c.pages++
	return app.ShutdownPreviewPage{Binding: c.binding, Offset: r.Offset, Runs: c.rows[2:]}, nil
}
func (c *shutdownCLIClient) CancelShutdown(_ context.Context, root string, b domain.ShutdownBindingState) error {
	if b != c.binding {
		return domain.ErrChanged
	}
	c.root = root
	c.cancels++
	return nil
}
func (c *shutdownCLIClient) ConfirmShutdown(_ context.Context, root string, r app.ShutdownConfirmation) (app.ShutdownResult, error) {
	c.root = root
	c.confirms++
	c.ack = r.WorkLossAcknowledged
	outcome := domain.ShutdownDraining
	if c.confirmErr != nil {
		outcome = domain.ShutdownUnknown
	}
	return app.ShutdownResult{Binding: r.Binding, Outcome: outcome, ClosedTerminals: r.Binding.TotalRuns}, c.confirmErr
}
func (c *shutdownCLIClient) ShutdownReceipt(_ context.Context, root string, b domain.ShutdownBindingState) (app.ShutdownResult, error) {
	c.root = root
	c.receipts++
	return app.ShutdownResult{Binding: b, Outcome: domain.ShutdownPending}, nil
}
func shutdownCLIFixture(t *testing.T) *shutdownCLIClient {
	t.Helper()
	epoch := strings.Repeat("1", 32)
	rows := []app.ShutdownPreviewRun{}
	for _, id := range []string{"2", "3", "4"} {
		rows = append(rows, app.ShutdownPreviewRun{Selection: app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: epoch, Run: strings.Repeat(id, 32), Generation: strings.Repeat(id, 32)}, Worktree: domain.WorktreeID("w_" + strings.Repeat("a", 64)), Profile: "codex"}, State: domain.RunLive})
	}
	digest, err := app.ShutdownRosterDigest(rows)
	if err != nil {
		t.Fatal(err)
	}
	return &shutdownCLIClient{binding: domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("a", 32), Revision: 7, RosterDigest: digest, TotalRuns: 3}, rows: rows}
}
func shutdownCLIArgs(c *shutdownCLIClient, kind string) []string {
	return []string{"daemon", "shutdown", kind, "--runtime-dir", "/private/runtime", "--epoch", c.binding.Epoch, "--preview-id", c.binding.PreviewID, "--revision", "7", "--roster-digest", c.binding.RosterDigest, "--total-runs", "3", "--pending-launches", "0", "--pending-controls", "0", "--pending-reconciliations", "0"}
}
func TestDaemonShutdownCLIFullPreviewAndUnknownConfirmationNoCoreOrRetry(t *testing.T) {
	c := shutdownCLIFixture(t)
	cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(c), cli.WithDatabasePathSetter(func(string) { t.Fatal("shutdown touched core selection") })).Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"daemon", "shutdown", "preview", "--runtime-dir", "/private/runtime", "--epoch", c.binding.Epoch})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if c.previews != 1 || c.pages != 1 || c.confirms != 0 || c.serves != 0 || c.statuses != 0 || !strings.Contains(out.String(), "SIGHUP") || !strings.Contains(out.String(), c.rows[2].Selection.Identity.Run) {
		t.Fatal("preview omitted risks/pages or performed mutation")
	}
	c.confirmErr = app.ErrShutdownUnknown
	out.Reset()
	cmd = cli.NewRootCLI(cli.WithRuntimeDaemon(c), cli.WithDatabasePathSetter(func(string) { t.Fatal("shutdown touched core selection") })).Command()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append(shutdownCLIArgs(c, "confirm"), "--acknowledge-work-loss"))
	if err := cmd.Execute(); !errors.Is(err, app.ErrShutdownUnknown) {
		t.Fatal("unknown confirmation hidden", err)
	}
	if c.confirms != 1 || !c.ack || !strings.Contains(out.String(), `"outcome":"unknown"`) {
		t.Fatal("unknown not explicit or replayed")
	}
}
func TestDaemonShutdownCLIRejectsMissingAcknowledgmentAndNoncanonicalCounters(t *testing.T) {
	for _, mode := range []string{"missing-ack", "false-ack", "noncanonical", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			c := shutdownCLIFixture(t)
			args := shutdownCLIArgs(c, "confirm")
			if mode != "missing-ack" {
				args = append(args, "--acknowledge-work-loss")
			}
			if mode == "false-ack" {
				args[len(args)-1] = "--acknowledge-work-loss=false"
			}
			if mode == "noncanonical" {
				for i := range args {
					if args[i] == "--revision" {
						args[i+1] = "07"
					}
				}
			}
			if mode == "oversize" {
				for i := range args {
					if args[i] == "--total-runs" {
						args[i+1] = "1025"
					}
				}
			}
			cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(c)).Command()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || c.confirms != 0 {
				t.Fatal("invalid confirmation forwarded")
			}
		})
	}
}

type shutdownHistoryFixture struct{ reads int }

func (h *shutdownHistoryFixture) ReadLifecycle(context.Context) (app.LifecycleSnapshot, error) {
	h.reads++
	return app.LifecycleSnapshot{Epoch: strings.Repeat("1", 32), Clean: true, Interrupted: []domain.Interruption{}, Shutdowns: []app.ShutdownResult{}}, nil
}

type shutdownHistoryOperators struct {
	history        *shutdownHistoryFixture
	opens, creates int
}

func (f *shutdownHistoryOperators) Open(context.Context, string) (app.OperatorServices, error) {
	f.opens++
	return app.OperatorServices{Lifecycle: f.history}, nil
}
func (f *shutdownHistoryOperators) Create(context.Context, string) (app.OperatorServices, error) {
	f.creates++
	return app.OperatorServices{}, app.ErrRuntimeUnsupported
}
func TestDaemonShutdownHistoryReadsExistingMetadataWithoutDaemonOrCore(t *testing.T) {
	history := &shutdownHistoryFixture{}
	factory := &shutdownHistoryOperators{history: history}
	cmd := cli.NewRootCLI(cli.WithRuntimeOperators(factory), cli.WithDatabasePathSetter(func(string) { t.Fatal("history touched core selection") })).Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"daemon", "shutdown", "history", "--runtime-dir", "/private/runtime"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if factory.opens != 1 || factory.creates != 0 || history.reads != 1 || !strings.Contains(out.String(), "history_only_not_resume_permission") {
		t.Fatal("history created state or invented native authority")
	}
}
