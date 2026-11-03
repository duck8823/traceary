package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/presentation/cli"
)

type daemonStub struct {
	root             string
	serves, statuses int
	err              error
}

func (d *daemonStub) Serve(_ context.Context, root string) error {
	d.root = root
	d.serves++
	return d.err
}
func (d *daemonStub) Status(_ context.Context, root string) (app.DaemonStatus, error) {
	d.root = root
	d.statuses++
	return app.DaemonStatus{ProtocolVersion: 1, Epoch: "fixture-epoch", State: "listening", Capabilities: []string{"status"}, ObservationDelivery: "unavailable"}, d.err
}
func (d *daemonStub) Observe(context.Context, string, app.ObservationMetadata) error {
	return app.ErrRuntimeUnsupported
}
func TestDaemonCLIUsesTypedPortAndNoCoreStoreSetter(t *testing.T) {
	for _, command := range []string{"serve", "status"} {
		t.Run(command, func(t *testing.T) {
			d := &daemonStub{}
			output := &bytes.Buffer{}
			root := cli.NewRootCLI(cli.WithRuntimeDaemon(d), cli.WithDatabasePathSetter(func(string) { t.Fatal("daemon touched core store selection") })).Command()
			runtimeDir := filepath.Join(t.TempDir(), "runtime")
			args := []string{"daemon", command, "--runtime-dir", runtimeDir}
			if command == "status" {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			root.SetOut(output)
			root.SetErr(output)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if d.root != runtimeDir || d.serves+d.statuses != 1 {
				t.Fatal("wrong typed dependency")
			}
			if command == "status" {
				var value map[string]any
				if err := json.Unmarshal(output.Bytes(), &value); err != nil || value["state"] != "listening" {
					t.Fatal("status is not JSON readiness", err)
				}
			}
		})
	}
}
func TestDaemonCLIRejectsRelativeRootAndUnknownCommand(t *testing.T) {
	for _, args := range [][]string{{"daemon", "status", "--runtime-dir", "relative"}, {"daemon", "execute", "arbitrary"}, {"daemon", "serve", "extra"}} {
		d := &daemonStub{}
		root := cli.NewRootCLI(cli.WithRuntimeDaemon(d)).Command()
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err == nil {
			t.Fatalf("accepted invalid args %v", args)
		}
		if d.serves+d.statuses != 0 {
			t.Fatal("invalid request invoked transport")
		}
	}
}
func TestDaemonCLIDefaultStateRootAndUnavailableError(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	d := &daemonStub{err: app.ErrDaemonUnavailable}
	root := cli.NewRootCLI(cli.WithRuntimeDaemon(d)).Command()
	root.SetArgs([]string{"daemon", "status"})
	if err := root.Execute(); !errors.Is(err, app.ErrDaemonUnavailable) {
		t.Fatalf("lost error identity: %v", err)
	}
	if d.root != filepath.Join(state, "traceary", "desktop", "runtime") {
		t.Fatal("wrong XDG root")
	}
}
