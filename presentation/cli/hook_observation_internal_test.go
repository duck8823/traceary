package cli

import (
	"bytes"
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type hookObservationClient struct {
	app.Daemon
	calls   int
	inspect func(app.SelectedObservation) error
}

func (c *hookObservationClient) ObserveSelected(_ context.Context, _ string, o app.SelectedObservation) error {
	c.calls++
	if c.inspect != nil {
		return c.inspect(o)
	}
	return nil
}
func hookObservationFixture() hookObservationConfig {
	return hookObservationConfig{root: "/explicit/runtime", flags: observationSelectionFlags{epoch: strings.Repeat("a", 32), run: strings.Repeat("b", 32), generation: strings.Repeat("c", 32), worktree: "w_" + strings.Repeat("d", 64), profile: "fixture", version: "fixture 1"}}
}
func TestHookObservationFollowsPrimaryAndSpoolClearWithoutSecondInput(t *testing.T) {
	state := t.TempDir()
	t.Setenv(hookStateDirEnvKey, state)
	payload := `{"prompt":"PRIVATE_BODY_NOT_TELEMETRY","session_id":"native-session","cwd":"/tmp"}`
	var output bytes.Buffer
	var receipt string
	primary := false
	client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
		if !primary {
			t.Error("optional delivery preceded primary capture")
		}
		entries, e := os.ReadDir(filepath.Join(state, "spool"))
		if e != nil || len(entries) != 0 {
			t.Error("optional delivery preceded current spool clearance", entries, e)
		}
		v := o.Metadata.Snapshot()
		if v.EventID != receipt || v.Host != "claude" || v.Kind != "prompt_boundary" || v.NativeSessionID != "native-session" {
			t.Error("derived metadata mismatch", v)
		}
		return app.ErrDaemonUnavailable
	}}
	cli := &RootCLI{runtimeDaemon: client, hookObservation: hookObservationFixture()}
	err := cli.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(payload), func(input io.Reader) error {
		primary = true
		reader, ok := input.(*explicitHookPayloadReader)
		if !ok {
			t.Fatal("original receipt reader replaced")
		}
		receipt = reader.receiptID
		_, e := io.Copy(&output, input)
		if e != nil {
			return xerrors.Errorf("synthetic hook fixture: %w", e)
		}
		return nil
	})
	if err != nil || output.String() != payload || client.calls != 1 || !strings.HasPrefix(receipt, "spool:") {
		t.Fatal("primary result/capture changed", err, client.calls)
	}
}
func TestHookObservationSkipsInvalidContextFailureDeferredAndLowHeadroom(t *testing.T) {
	for _, mode := range []string{"missing", "partial", "invalid", "unsupported_client", "unsupported_command", "failed_capture", "deferred_capture", "low_headroom", "clear_failure"} {
		t.Run(mode, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv(hookStateDirEnvKey, state)
			client := &hookObservationClient{}
			config := hookObservationFixture()
			spec := hookInvocationSpec{Command: "prompt", Client: "claude"}
			switch mode {
			case "missing":
				config = hookObservationConfig{}
			case "partial":
				config.flags.profile = ""
			case "invalid":
				config.flags.generation = "invalid"
			case "unsupported_client":
				spec.Client = "muse"
			case "unsupported_command":
				spec.Command = "usage"
			}
			ctx := context.Background()
			if mode == "low_headroom" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, hookSpoolDrainReserve+100*time.Millisecond)
				defer cancel()
			}
			cli := &RootCLI{runtimeDaemon: client, hookObservation: config}
			primaryCalls := 0
			_ = cli.runHookDurably(ctx, "prompt", spec, strings.NewReader(`{"prompt":"unchanged"}`), func(input io.Reader) error {
				primaryCalls++
				_, e := readHookPayload(input)
				if e != nil {
					return xerrors.Errorf("synthetic hook fixture: %w", e)
				}
				if mode == "failed_capture" {
					return errors.New("synthetic primary failure")
				}
				if mode == "deferred_capture" {
					return &apptypes.StoreMaintenancePendingError{}
				}
				if mode == "clear_failure" {
					entries, e := os.ReadDir(filepath.Join(state, "spool"))
					if e != nil {
						return xerrors.Errorf("synthetic hook fixture: %w", e)
					}
					for _, entry := range entries {
						path := filepath.Join(state, "spool", entry.Name())
						if e = os.Rename(path, path+".retained"); e != nil {
							return xerrors.Errorf("synthetic hook fixture: %w", e)
						}
						if e = os.Mkdir(path, 0700); e != nil {
							return xerrors.Errorf("synthetic hook fixture: %w", e)
						}
						if e = os.WriteFile(filepath.Join(path, "block-remove"), []byte("fixture"), 0600); e != nil {
							return xerrors.Errorf("synthetic hook fixture: %w", e)
						}
					}
				}
				return nil
			})
			if primaryCalls != 1 || client.calls != 0 {
				t.Fatal("optional path changed failure/context semantics", primaryCalls, client.calls)
			}
		})
	}
}
func TestHookObservationNoSpoolFallbackStillPreservesPrimary(t *testing.T) {
	state := filepath.Join(t.TempDir(), "not-a-directory")
	if e := os.WriteFile(state, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv(hookStateDirEnvKey, state)
	client := &hookObservationClient{}
	cli := &RootCLI{runtimeDaemon: client, hookObservation: hookObservationFixture()}
	primary := 0
	err := cli.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(`{"prompt":"unchanged"}`), func(input io.Reader) error { primary++; _, e := readHookPayload(input); return e })
	if err != nil || primary != 1 || client.calls != 1 {
		t.Fatal("no-spool primary/optional fallback changed", err, primary, client.calls)
	}
}

func TestHookObservationAppendedInvocationFlagsPreserveOneLogicalCapture(t *testing.T) {
	for _, mode := range []string{"baseline", "queued", "absent", "unsafe", "full", "old_peer", "partial"} {
		t.Run(mode, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv(hookStateDirEnvKey, state)
			events := &spoolEventUsecaseStub{}
			client := &hookObservationClient{}
			switch mode {
			case "absent":
				client.inspect = func(app.SelectedObservation) error { return app.ErrDaemonUnavailable }
			case "unsafe":
				client.inspect = func(app.SelectedObservation) error { return app.ErrUnsafeRuntime }
			case "full":
				client.inspect = func(app.SelectedObservation) error { return app.ErrRuntimeBusy }
			case "old_peer":
				client.inspect = func(app.SelectedObservation) error { return app.ErrRuntimeProtocol }
			}
			root := NewRootCLI(WithRuntimeDaemon(client), WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events)).Command()
			payload := `{"prompt":"original prompt bytes","session_id":"native-session","cwd":"/tmp"}`
			root.SetIn(strings.NewReader(payload))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			args := []string{"hook", "prompt", "claude"}
			if mode != "baseline" {
				args = append(args, "--observe-runtime-dir", "/explicit/runtime", "--observe-epoch", strings.Repeat("a", 32), "--observe-run", strings.Repeat("b", 32), "--observe-generation", strings.Repeat("c", 32), "--observe-worktree", "w_"+strings.Repeat("d", 64), "--observe-profile", "fixture", "--observe-host-version", "fixture 1")
			}
			if mode == "partial" {
				args = args[:len(args)-2]
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal("existing hook decision failed", err)
			}
			if events.logCalls != 1 || events.lastMessage != "original prompt bytes" || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatal("appended flags changed existing capture/decision", events.logCalls, events.lastMessage, stdout.String(), stderr.String())
			}
			expected := 1
			if mode == "baseline" || mode == "partial" {
				expected = 0
			}
			if client.calls != expected {
				t.Fatal("optional delivery count", client.calls, expected)
			}
			paths, err := listHookSpoolRecordPaths()
			if err != nil || len(paths) != 0 {
				t.Fatal("successful hook changed primary spool ownership", paths, err)
			}
		})
	}
}
func TestHookObservationRunsBeforeBacklogAndNeverRedeliversReplay(t *testing.T) {
	state := t.TempDir()
	t.Setenv(hookStateDirEnvKey, state)
	events := &spoolEventUsecaseStub{}
	backlog := hookSpoolRecord{SchemaVersion: hookSpoolSchemaVersion, Command: "prompt", Client: "claude", Payload: `{"prompt":"backlog prompt","session_id":"backlog-session","cwd":"/tmp"}`, CreatedAt: time.Now().UTC().Add(-time.Minute), ReceiptID: "spool:backlog"}
	if _, err := persistHookSpoolRecord(backlog); err != nil {
		t.Fatal(err)
	}
	client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
		if events.logCalls != 0 {
			t.Error("backlog drained before optional current delivery")
		}
		if o.Metadata.Snapshot().EventID == "spool:backlog" {
			t.Error("replay observation delivered")
		}
		return nil
	}}
	root := NewRootCLI(WithRuntimeDaemon(client), WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
	root.hookObservation = hookObservationFixture()
	primary := 0
	err := root.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(`{"prompt":"current"}`), func(input io.Reader) error { primary++; _, e := readHookPayload(input); return e })
	if err != nil || primary != 1 || client.calls != 1 || events.logCalls != 1 || events.lastMessage != "backlog prompt" {
		t.Fatal("primary/optional/replay ownership changed", err, primary, client.calls, events.logCalls, events.lastMessage)
	}
}
func TestHookObservationBoundaryMappingIsClosedAndNeverMeansProcessExit(t *testing.T) {
	for _, host := range []string{"codex", "claude", "antigravity", "grok"} {
		for _, item := range []struct{ command, action, kind string }{{"prompt", "", "prompt_boundary"}, {"audit", "", "tool_boundary"}, {"transcript", "", "turn_boundary"}, {"session", "start", "session_boundary"}, {"session", "end", "session_boundary"}, {"session", "stop", "turn_boundary"}} {
			got, kind, ok := hookObservationBoundary(hookInvocationSpec{Client: host, Command: item.command, Action: item.action})
			if !ok || got != host || string(kind) != item.kind {
				t.Fatal("closed boundary mapping", got, kind, ok)
			}
		}
	}
	for _, host := range []string{"muse", "kimi", "gemini", "unknown"} {
		if _, _, ok := hookObservationBoundary(hookInvocationSpec{Client: host, Command: "prompt"}); ok {
			t.Fatal("unsupported native host promoted", host)
		}
	}
}
