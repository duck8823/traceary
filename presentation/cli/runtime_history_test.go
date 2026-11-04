package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/presentation/cli"
)

type historyCLIClient struct {
	app.Client
	calls, hints int
	request      app.RemoteHistoryRequest
	result       app.HistoryResult
}

func (c *historyCLIClient) HistoryPage(_ context.Context, _ string, r app.RemoteHistoryRequest) (app.HistoryResult, error) {
	c.calls++
	c.request = r
	return c.result, nil
}
func (c *historyCLIClient) HintHistory(context.Context, string) error { c.hints++; return nil }
func TestRuntimeHistoryCLIIsBoundedMetadataOnlyAndNeverTouchesCore(t *testing.T) {
	p, err := domain.ProjectOf("/project", "/project/.git", "project")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), "/project", "/project/.git", "/core", "original")
	if err != nil {
		t.Fatal(err)
	}
	source, err := domain.HistorySourceOf(w, "current")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	v, err := domain.HistorySelectionOf(base, base.Add(time.Hour), 2, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := domain.HistoryPageOf(source, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &historyCLIClient{result: app.HistoryResult{Epoch: strings.Repeat("a", 32), Page: page}}
	run := func(extra ...string) (string, error) {
		cmd := cli.NewRootCLI(cli.WithRuntimeClient(c), cli.WithDatabasePathSetter(func(string) { t.Fatal("history touched core bootstrap") })).Command()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		args := []string{"runtime", "history", "page", "--runtime-dir", "/private/runtime", "--epoch", c.result.Epoch, "--worktree", string(w.ID()), "--from", base.Format(time.RFC3339Nano), "--to", base.Add(time.Hour).Format(time.RFC3339Nano), "--limit", "2"}
		cmd.SetArgs(append(args, extra...))
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run()
	if err != nil {
		t.Fatal(err, out)
	}
	var view map[string]any
	if err = json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 || c.hints != 0 || view["native_target_revalidated"] != false || view["authority"] != "committed_page_not_commit_cursor" || view["consistency"] != "single_page_only" {
		t.Fatal("history invented authority or effects", view)
	}
	for _, args := range [][]string{{"--before-id", "partial"}, {"--source-witness", "unpaired"}, {"--limit", "65"}, {"--from", "2026-11-04T00:00:00+00:00"}} {
		if _, err = run(args...); err == nil || c.calls != 1 {
			t.Fatal("invalid CLI reached history port", args, err)
		}
	}
}
