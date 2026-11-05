package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/presentation/cli"
)

type autoStartCLIFixture struct {
	app.OperatorFactory
	service                         *app.AutoStart
	root                            string
	opens, creates, writes, renders int
	enabled                         bool
	failure                         error
}

func (f *autoStartCLIFixture) OpenAutoStart(_ context.Context, root string) (*app.AutoStart, error) {
	f.root = root
	f.opens++
	return f.service, f.failure
}
func (f *autoStartCLIFixture) CreateAutoStart(_ context.Context, root string) (*app.AutoStart, error) {
	f.root = root
	f.creates++
	return f.service, f.failure
}
func (f *autoStartCLIFixture) ReadLifecycle(context.Context) (app.LifecycleSnapshot, error) {
	return app.LifecycleSnapshot{AutoStartOptIn: f.enabled}, f.failure
}
func (f *autoStartCLIFixture) SetAutoStartOptIn(_ context.Context, enabled bool) error {
	f.writes++
	f.enabled = enabled
	return f.failure
}
func (f *autoStartCLIFixture) RenderAutoStartDescriptor(context.Context) (string, error) {
	f.renders++
	return "<?xml descriptor-only?>\n", f.failure
}
func TestDaemonAutoStartCLIExplicitPreferenceAndReadBoundaries(t *testing.T) {
	f := &autoStartCLIFixture{}
	var err error
	f.service, err = app.NewAutoStart(f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := cli.NewRootCLI(cli.WithRuntimeOperators(f), cli.WithDatabasePathSetter(func(string) { t.Fatal("auto-start touched core store") })).Command()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"daemon", "autostart", "--runtime-dir", "/private/runtime"}, args...))
		err := cmd.Execute()
		return out.String(), err
	}
	if _, err = run("opt-in"); err == nil || f.creates != 0 || f.opens != 0 || f.writes != 0 {
		t.Fatal("missing explicit flag accepted", err)
	}
	out, err := run("status")
	var status app.AutoStartStatus
	if err != nil || json.Unmarshal([]byte(out), &status) != nil || status.OptIn || status.InstallationState != "unknown" || f.creates != 0 || f.opens != 1 {
		t.Fatal(out, err)
	}
	if _, err = run("descriptor"); err == nil || f.renders != 0 || f.creates != 0 {
		t.Fatal("disabled descriptor rendered", err)
	}
	for _, enabled := range []string{"false", "true"} {
		out, err = run("opt-in", "--enabled="+enabled)
		if err != nil || json.Unmarshal([]byte(out), &status) != nil || status.OptIn != (enabled == "true") || status.StartsAgents || status.HostInspection != "not_performed" {
			t.Fatal(out, err)
		}
	}
	out, err = run("descriptor")
	if err != nil || !strings.HasPrefix(out, "<?xml") || f.creates != 2 || f.writes != 2 || f.renders != 1 || f.root != "/private/runtime" {
		t.Fatal(out, err, f)
	}
	f.failure = app.ErrLifecycleCommitUncertain
	if _, err = run("opt-in", "--enabled=true"); !errors.Is(err, f.failure) || f.creates != 3 || f.writes != 2 {
		t.Fatal("factory failure retried", err)
	}
}

func TestDaemonAutoStartCLIRejectsUnsafeArgumentsAndMissingCapabilities(t *testing.T) {
	for _, args := range [][]string{
		{"opt-in"}, {"opt-in", "--enabled=invalid"},
		{"opt-in", "--enabled=true", "--runtime-dir", "/private/runtime/../other"},
		{"opt-in", "--enabled=false", "--runtime-dir", "relative"},
		{"status", "unexpected"},
	} {
		f := &autoStartCLIFixture{}
		cmd := cli.NewRootCLI(cli.WithRuntimeOperators(f)).Command()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append([]string{"daemon", "autostart"}, args...))
		if err := cmd.Execute(); err == nil || f.opens != 0 || f.creates != 0 || f.writes != 0 || f.renders != 0 {
			t.Fatal("invalid command reached capabilities", args, err)
		}
	}
	for _, factory := range []app.OperatorFactory{nil, &autoStartCLIFixture{}} {
		cmd := cli.NewRootCLI(cli.WithRuntimeOperators(factory)).Command()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"daemon", "autostart", "status", "--runtime-dir", "/private/runtime"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("missing service accepted")
		}
	}
}
