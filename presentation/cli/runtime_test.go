package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/presentation/cli"
)

type runtimeClientFixture struct {
	calls   int
	root    string
	request app.LaunchRequest
}

func (c *runtimeClientFixture) Launch(_ context.Context, root string, r app.LaunchRequest) (domain.RunSnapshot, error) {
	c.calls++
	c.root = root
	c.request = r
	return domain.RunSnapshot{}, app.ErrLaunchUnknown
}
func (c *runtimeClientFixture) ListRuns(_ context.Context, root string, r app.RunPageRequest) (app.RunPage, error) {
	c.calls++
	c.root = root
	return app.RunPage{Epoch: r.Epoch, Revision: 1, Runs: []domain.RunSnapshot{}}, nil
}
func TestRuntimeCLIUsesTypedClientAndDoesNotBootstrapCoreStore(t *testing.T) {
	client := &runtimeClientFixture{}
	cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	epoch := strings.Repeat("e", 32)
	cmd.SetArgs([]string{"runtime", "list", "--runtime-dir", "/private/runtime", "--epoch", epoch})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || client.root != "/private/runtime" || got["epoch"] != epoch {
		t.Fatal("typed runtime list lost binding")
	}
}
func TestRuntimeCLILaunchUnknownKeepsOriginalRequestIdentity(t *testing.T) {
	client := &runtimeClientFixture{}
	cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	epoch := strings.Repeat("e", 32)
	worktree := "w_" + strings.Repeat("1", 64)
	cmd.SetArgs([]string{"runtime", "launch", "--runtime-dir", "/private/runtime", "--epoch", epoch, "--worktree", worktree, "--profile", "codex", "--request-id", "original-request"})
	if err := cmd.Execute(); !errors.Is(err, app.ErrLaunchUnknown) {
		t.Fatalf("unknown outcome: %v", err)
	}
	if client.calls != 1 || client.request.RequestID != "original-request" || client.request.Worktree != domain.WorktreeID(worktree) {
		t.Fatal("unknown launch was replaced or replayed")
	}
	if !strings.Contains(out.String(), "original-request") {
		t.Fatal("unknown response lost explicit receipt correlation")
	}
}
