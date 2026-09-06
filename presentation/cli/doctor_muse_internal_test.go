package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMuseDoctorChecks(t *testing.T) {
	healthy := museDoctorState{
		CLIAvailable: true, HostVersion: "Muse Code 1.0.3",
		PluginInstalled: true, PluginEnabled: true, PluginRecordKnown: true,
		PluginVersion: "0.49.0", NativeHooks: true, Skills: 4,
	}
	tests := []struct {
		name       string
		mutate     func(*museDoctorState)
		check      string
		status     string
		messageSub string
	}{
		{name: "absent CLI", mutate: func(s *museDoctorState) { *s = museDoctorState{} }, check: "muse-cli", status: doctorStatusFail, messageSub: "not installed"},
		{name: "absent plugin", mutate: func(s *museDoctorState) { s.PluginInstalled = false }, check: "muse-plugin", status: doctorStatusWarn, messageSub: "not installed"},
		{name: "hooks mismatch", mutate: func(s *museDoctorState) { s.NativeHooks = false }, check: "muse-hooks", status: doctorStatusWarn, messageSub: "incomplete"},
		{name: "declared-but-unobserved SessionEnd and compact", mutate: func(*museDoctorState) {}, check: "muse-hooks", status: doctorStatusWarn, messageSub: "SessionEnd"},
		{name: "skills shortfall", mutate: func(s *museDoctorState) { s.Skills = 2 }, check: "muse-skills", status: doctorStatusWarn, messageSub: "2"},
		{name: "healthy plugin", mutate: func(*museDoctorState) {}, check: "muse-plugin", status: doctorStatusPass, messageSub: "enabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := healthy
			tc.mutate(&state)
			checks := buildMuseDoctorChecks(state, "0.49.0")
			var found *doctorCheck
			for i := range checks {
				if checks[i].Name == tc.check {
					found = &checks[i]
					break
				}
			}
			if found == nil || found.Status != tc.status || !strings.Contains(found.Message, tc.messageSub) {
				t.Fatalf("checks = %+v, want %s %s containing %q", checks, tc.check, tc.status, tc.messageSub)
			}
			home, _ := os.UserHomeDir()
			for _, check := range checks {
				combined := check.Message + check.Hint
				if strings.Contains(combined, "/private/") {
					t.Fatalf("check exposed private path: %+v", check)
				}
				if home != "" && home != "/" && strings.Contains(combined, home) {
					t.Fatalf("check exposed home layout: %+v", check)
				}
			}
		})
	}
}

func TestBuildMuseDoctorChecksHealthyStateWarnsHooks(t *testing.T) {
	state := museDoctorState{
		CLIAvailable: true, HostVersion: "Muse Code 1.0.3",
		PluginInstalled: true, PluginEnabled: true, PluginRecordKnown: true,
		PluginVersion: "0.49.0", NativeHooks: true, Skills: 4,
	}
	for _, check := range buildMuseDoctorChecks(state, "0.49.0") {
		if check.Name == "muse-hooks" {
			if check.Status != doctorStatusWarn {
				t.Fatalf("muse-hooks status = %s, want WARN: %s", check.Status, check.Message)
			}
			continue
		}
		if check.Status != doctorStatusPass {
			t.Fatalf("healthy state produced %s %s: %s", check.Name, check.Status, check.Message)
		}
	}
}

func TestProbeMuseDoctorState(t *testing.T) {
	originalLookPath, originalOutput := museDoctorLookPath, museDoctorOutput
	t.Cleanup(func() { museDoctorLookPath, museDoctorOutput = originalLookPath, originalOutput })

	t.Run("missing CLI binary", func(t *testing.T) {
		museDoctorLookPath = func(string) (string, error) { return "", errors.New("not found") }
		state, err := probeMuseDoctorState(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("probeMuseDoctorState() error = %v", err)
		}
		if state != (museDoctorState{}) {
			t.Fatalf("state = %+v, want zero", state)
		}
	})

	t.Run("empty plugins list", func(t *testing.T) {
		museDoctorLookPath = func(string) (string, error) { return "/usr/bin/muse", nil }
		museDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("Muse Code 1.0.3\n"), nil
			}
			return []byte(`{"plugins":[]}`), nil
		}
		state, err := probeMuseDoctorState(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("probeMuseDoctorState() error = %v", err)
		}
		if state.PluginInstalled {
			t.Fatalf("PluginInstalled = true, want false: %+v", state)
		}
		if !state.CLIAvailable || !state.PluginRecordKnown {
			t.Fatalf("state = %+v", state)
		}
	})

	t.Run("record-nested plugins list", func(t *testing.T) {
		museDoctorLookPath = func(string) (string, error) { return "/usr/bin/muse", nil }
		museDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("Muse Code 1.0.3\n"), nil
			}
			return []byte(`{"plugins":[{"record":{"id":"traceary-muse","display_name":"Traceary Muse","version":"0.50.0","enabled":true},"plugin":{"id":"traceary-muse"},"valid":true,"active":true,"active_scope":"user"}]}`), nil
		}
		state, err := probeMuseDoctorState(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("probeMuseDoctorState() error = %v", err)
		}
		if !state.PluginInstalled || !state.PluginEnabled || !state.PluginRecordKnown {
			t.Fatalf("state = %+v, want installed+enabled+known", state)
		}
		if state.PluginVersion != "0.50.0" {
			t.Fatalf("PluginVersion = %q, want 0.50.0", state.PluginVersion)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		museDoctorLookPath = func(string) (string, error) { return "/usr/bin/muse", nil }
		museDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("Muse Code 1.0.3\n"), nil
			}
			return []byte("{"), nil
		}
		_, err := probeMuseDoctorState(context.Background(), t.TempDir())
		if err == nil {
			t.Fatal("expected parse error")
		}
		if strings.Contains(err.Error(), "/private/") || strings.Contains(err.Error(), filepath.Join("Users")) {
			t.Fatalf("error leaked path: %v", err)
		}
	})
}

func TestNativeHostPackageChecks_Muse(t *testing.T) {
	originalLookPath, originalOutput := museDoctorLookPath, museDoctorOutput
	t.Cleanup(func() { museDoctorLookPath, museDoctorOutput = originalLookPath, originalOutput })
	museDoctorLookPath = func(string) (string, error) { return "", errors.New("not found") }

	cli := &RootCLI{}
	checks, handled := cli.nativeHostPackageChecks(context.Background(), "muse", t.TempDir(), "0.49.0")
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(checks) == 0 || checks[0].Name != "muse-cli" {
		t.Fatalf("checks = %+v, want muse-cli from buildMuseDoctorChecks", checks)
	}
}

func TestResolveDoctorClients_Muse(t *testing.T) {
	cli := newLargeStoreDoctorRootCLI(&trackingStoreStub{}, &trackingEventStub{}, &panicLargeStoreCapacityInspector{})
	got, err := resolveDoctorClients(cli, "muse")
	if err != nil {
		t.Fatalf("resolveDoctorClients(muse) error = %v", err)
	}
	if len(got) != 1 || got[0] != "muse" {
		t.Fatalf("got = %v, want [muse]", got)
	}
	if _, err := resolveDoctorClients(cli, "not-a-client"); err == nil {
		t.Fatal("unknown client must error")
	}
}

func TestDoctor_MuseClientReachable(t *testing.T) {
	originalLookPath, originalOutput := museDoctorLookPath, museDoctorOutput
	t.Cleanup(func() { museDoctorLookPath, museDoctorOutput = originalLookPath, originalOutput })
	museDoctorLookPath = func(string) (string, error) { return "/usr/bin/muse", nil }
	museDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("Muse Code 1.0.3\n"), nil
		}
		return []byte(`{"plugins":[]}`), nil
	}

	t.Setenv("TRACEARY_LANG", "en")
	setLargeStoreDoctorPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	largeStore := writeLargeStoreFixture(t)
	store := &trackingStoreStub{}
	events := &trackingEventStub{}
	capacity := &panicLargeStoreCapacityInspector{}
	rootCmd := newLargeStoreDoctorRootCLI(store, events, capacity).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "muse", "--project-dir", t.TempDir(), "--db-path", largeStore, "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stdout.String())
	}
	out := stdout.String()
	if strings.Contains(out, "failed to normalize client") {
		t.Fatalf("doctor --client muse normalize error:\n%s", out)
	}
	report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
	if largeStoreCheckByName(report, "muse-cli").Name == "" {
		t.Fatalf("missing muse-cli:\n%s", out)
	}
	if largeStoreCheckByName(report, "muse-plugin").Name == "" {
		t.Fatalf("missing muse-plugin:\n%s", out)
	}
}

func TestReplayHookSpoolRecord_MuseActions(t *testing.T) {
	cli := &RootCLI{}
	ctx := context.Background()

	err := cli.replayHookSpoolRecord(ctx, hookSpoolRecord{
		Command: "muse",
		Action:  "pre-tool-use",
		Payload: `{}`,
	})
	if err != nil {
		t.Fatalf("pre-tool-use replay error = %v", err)
	}

	err = cli.replayHookSpoolRecord(ctx, hookSpoolRecord{
		Command: "muse",
		Action:  "session-start",
		Payload: `{}`,
	})
	if err != nil {
		t.Fatalf("session-start replay error = %v", err)
	}

	err = cli.replayHookSpoolRecord(ctx, hookSpoolRecord{
		Command: "muse",
		Action:  "not-an-action",
		Payload: `{}`,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported muse spool action") {
		t.Fatalf("unknown action error = %v, want unsupported muse spool action", err)
	}

	for _, action := range []string{
		"session-start", "user-prompt-submit", "pre-tool-use", "post-tool-use",
		"post-tool-use-failure", "stop", "pre-compact", "post-compact",
	} {
		err := cli.replayMuseSpoolRecord(ctx, strings.NewReader(`{}`), action, "")
		if err != nil && strings.Contains(err.Error(), "unsupported muse spool action") {
			t.Fatalf("action %s was not dispatched: %v", action, err)
		}
		if err != nil && strings.Contains(err.Error(), "unsupported hook spool command") {
			t.Fatalf("action %s hit command dispatcher: %v", action, err)
		}
	}
}
