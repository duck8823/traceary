package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/presentation/cli"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type observeCLIClient struct {
	app.Daemon
	calls int
	root  string
	value app.SelectedObservation
	err   error
}

func (c *observeCLIClient) ObserveSelected(_ context.Context, root string, value app.SelectedObservation) error {
	c.calls++
	c.root = root
	c.value = value
	return c.err
}

type observeUnreadInput struct{ reads atomic.Int32 }

func (r *observeUnreadInput) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, errors.New("observe must never consume stdin")
}
func observeArgs(root string) []string {
	return []string{"runtime", "observe", "--runtime-dir", root, "--epoch", strings.Repeat("a", 32), "--run", strings.Repeat("b", 32), "--generation", strings.Repeat("c", 32), "--worktree", "w_" + strings.Repeat("d", 64), "--profile", "fixture", "--host", "codex", "--host-version", "fixture 1", "--event-id", "spool:receipt", "--kind", "prompt_boundary"}
}
func TestRuntimeObserveCLIIsMetadataOnlyOptionalAndNeverBootstrapsCore(t *testing.T) {
	for _, tt := range []struct {
		name    string
		err     error
		outcome string
	}{{"queued", nil, "queued"}, {"absent", app.ErrDaemonUnavailable, "unavailable"}, {"unsafe", app.ErrUnsafeRuntime, "unsafe"}, {"old_peer", app.ErrRuntimeProtocol, "invalid"}, {"full", app.ErrRuntimeBusy, "busy"}, {"unsupported", app.ErrRuntimeUnsupported, "unsupported"}} {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "not-created")
			t.Setenv("HOME", filepath.Join(base, "no-home"))
			t.Setenv("TRACEARY_DB_PATH", filepath.Join(base, "no-core"))
			client := &observeCLIClient{err: tt.err}
			command := cli.NewRootCLI(cli.WithRuntimeDaemon(client)).Command()
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(io.Discard)
			input := &observeUnreadInput{}
			command.SetIn(input)
			command.SetArgs(observeArgs(root))
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result) != 4 || result["schema_version"] != float64(1) || result["outcome"] != tt.outcome || result["binding"] != "unbound" || result["authority"] != "admission_only" {
				t.Fatal("unexpected optional output", result)
			}
			if input.reads.Load() != 0 {
				t.Fatal("observation consumed stdin")
			}
			if client.calls != 1 || client.root != root || client.value.Metadata.Snapshot().EventID != "spool:receipt" {
				t.Fatal("explicit typed metadata not delivered", client.calls)
			}
			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 0 {
				t.Fatal("optional command created HOME/core/runtime state", entries, err)
			}
		})
	}
}
func TestRuntimeObserveCLIRejectsIncompleteOrInvalidOperatorFlagsBeforeTransport(t *testing.T) {
	for _, tt := range []struct{ name, flag, value string }{{"relative_root", "--runtime-dir", "relative"}, {"epoch", "--epoch", "not-an-epoch"}, {"profile", "--profile", "../../foreign"}, {"kind", "--kind", "approval_request"}, {"body", "--event-id", "raw prompt body"}, {"host", "--host", "muse"}} {
		t.Run(tt.name, func(t *testing.T) {
			client := &observeCLIClient{}
			command := cli.NewRootCLI(cli.WithRuntimeDaemon(client)).Command()
			args := observeArgs("/explicit/runtime")
			for i := range args {
				if args[i] == tt.flag {
					args[i+1] = tt.value
				}
			}
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(args)
			if err := command.Execute(); err == nil || client.calls != 0 {
				t.Fatal("operator misuse reached transport", err, client.calls)
			}
		})
	}
}

func TestRuntimeObserveCLIRequiresEveryExplicitFlagAndPreservesWriteErrors(t *testing.T) {
	for _, flag := range []string{"--runtime-dir", "--epoch", "--run", "--generation", "--worktree", "--profile", "--host", "--host-version", "--event-id", "--kind"} {
		t.Run(flag, func(t *testing.T) {
			client := &observeCLIClient{}
			cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(client)).Command()
			args := observeArgs("/explicit/runtime")
			for i := range args {
				if args[i] == flag {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || client.calls != 0 {
				t.Fatal("missing flag reached transport", err, client.calls)
			}
		})
	}
	client := &observeCLIClient{}
	cmd := cli.NewRootCLI(cli.WithRuntimeDaemon(client)).Command()
	cmd.SetOut(observeWriteFailure{})
	cmd.SetErr(io.Discard)
	cmd.SetArgs(observeArgs("/explicit/runtime"))
	if err := cmd.Execute(); err == nil {
		t.Fatal("output failure reported success")
	}
}

type observeWriteFailure struct{}

func (observeWriteFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
