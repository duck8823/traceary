package runtime_test

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
	"testing"
)

type shutdownPageFixture struct {
	pages []app.ShutdownPreviewPage
	calls int
}

func (c *shutdownPageFixture) PreviewShutdown(context.Context, string, string) (app.ShutdownPreviewPage, error) {
	c.calls++
	return c.pages[0], nil
}
func (c *shutdownPageFixture) ShutdownPage(context.Context, string, app.ShutdownPageRequest) (app.ShutdownPreviewPage, error) {
	c.calls++
	return c.pages[c.calls-1], nil
}
func (c *shutdownPageFixture) CancelShutdown(context.Context, string, domain.ShutdownBindingState) error {
	return nil
}
func (c *shutdownPageFixture) ConfirmShutdown(context.Context, string, app.ShutdownConfirmation) (app.ShutdownResult, error) {
	return app.ShutdownResult{}, nil
}
func (c *shutdownPageFixture) ShutdownReceipt(context.Context, string, domain.ShutdownBindingState) (app.ShutdownResult, error) {
	return app.ShutdownResult{}, nil
}
func TestShutdownCompletePlanRejectsPartialMixedAndForgedPages(t *testing.T) {
	epoch := strings.Repeat("1", 32)
	rows := []app.ShutdownPreviewRun{}
	for _, id := range []string{"2", "3", "4"} {
		rows = append(rows, app.ShutdownPreviewRun{Selection: app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: epoch, Run: strings.Repeat(id, 32), Generation: strings.Repeat(id, 32)}, Worktree: domain.WorktreeID("w_" + strings.Repeat("a", 64)), Profile: "codex"}, State: domain.RunLive})
	}
	digest, err := app.ShutdownRosterDigest(rows)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("a", 32), Revision: 7, RosterDigest: digest, TotalRuns: 3}
	pages := []app.ShutdownPreviewPage{{Binding: binding, Runs: rows[:2], Next: rows[1].Selection.Identity.Run}, {Binding: binding, Offset: 2, Runs: rows[2:]}}
	client := &shutdownPageFixture{pages: pages}
	plan, err := app.ReadShutdownPlan(context.Background(), client, "/private/runtime", epoch)
	if err != nil || len(plan.Runs) != 3 || plan.Binding != binding || client.calls != 2 {
		t.Fatal("complete plan not assembled", err)
	}
	for _, mutate := range []func([]app.ShutdownPreviewPage){
		func(p []app.ShutdownPreviewPage) { p[0].Next = "" },
		func(p []app.ShutdownPreviewPage) { p[1].Offset = 1 },
		func(p []app.ShutdownPreviewPage) { p[1].Binding.Revision++ },
		func(p []app.ShutdownPreviewPage) { p[1].Runs = []app.ShutdownPreviewRun{rows[0]} },
		func(p []app.ShutdownPreviewPage) {
			p[0].Binding.RosterDigest = strings.Repeat("f", 64)
			p[1].Binding = p[0].Binding
		},
	} {
		copyPages := append([]app.ShutdownPreviewPage{}, pages...)
		mutate(copyPages)
		if _, err = app.ReadShutdownPlan(context.Background(), &shutdownPageFixture{pages: copyPages}, "/private/runtime", epoch); !errors.Is(err, app.ErrRuntimeProtocol) {
			t.Fatal("incomplete/forged plan accepted", err)
		}
	}
	rows[1], rows[0] = rows[0], rows[1]
	if _, err = app.ShutdownRosterDigest(rows); err == nil {
		t.Fatal("unordered roster fingerprint accepted")
	}
}
