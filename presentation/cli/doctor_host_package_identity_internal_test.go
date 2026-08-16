package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// trackingStoreStub records whether Initialize (SQLite open) was ever called.
type trackingStoreStub struct {
	minimalStoreStub
	initCalled bool
}

func (s *trackingStoreStub) Initialize(ctx context.Context) error {
	s.initCalled = true
	return s.minimalStoreStub.Initialize(ctx)
}

// trackingEventStub counts List calls so tests can assert the bounded
// large-store path never reads events.
type trackingEventStub struct {
	hydrateCallTrackingEventUC
	listCalls int
}

func (u *trackingEventStub) List(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	u.listCalls++
	return u.hydrateCallTrackingEventUC.List(ctx, criteria)
}

type panicLargeStoreCapacityInspector struct{ calls int }

func (p *panicLargeStoreCapacityInspector) InspectCapacity(context.Context) (apptypes.CapacityReport, error) {
	p.calls++
	panic("capacity inspector must not run for large store")
}

type panicLargeStorePayloadCodecInspector struct{ calls int }

func (p *panicLargeStorePayloadCodecInspector) InspectPayloadCodec(context.Context) (application.PayloadCodecState, error) {
	p.calls++
	panic("payload codec inspector must not run for large store")
}

// newLargeStoreDoctorRootCLI builds a RootCLI wired the same way production
// doctor runs are, plus store/event/capacity/codec stubs that panic or count
// calls so tests can assert the bounded path never touches the store.
func newLargeStoreDoctorRootCLI(store *trackingStoreStub, events *trackingEventStub, capacity *panicLargeStoreCapacityInspector, codec *panicLargeStorePayloadCodecInspector) *RootCLI {
	return NewRootCLI(
		WithStoreManagement(store),
		WithEvent(events),
		WithCapacityInspector(capacity),
		WithPayloadCodecInspector(codec),
		WithHooksOrchestrator(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
			"claude":      filesystem.NewClaudeHooksHandler(),
			"codex":       filesystem.NewCodexHooksHandlerWithHomeDirFunc(func() (string, error) { return CallUserHomeDirFunc() }),
			"gemini":      filesystem.NewGeminiHooksHandler(),
			"antigravity": filesystem.NewAntigravityHooksHandler(),
			"grok":        filesystem.NewGrokHooksHandler(),
			"kimi":        filesystem.NewKimiHooksHandler(),
		})),
		WithHooksInspector(filesystem.NewHooksInspector()),
		WithPluginCacheInspector(filesystem.NewPluginCacheInspector()),
		WithClaudePluginDetector(filesystem.NewClaudePluginDetectorAdapter()),
	)
}

// writeLargeStoreFixture creates a sparse >=2 GiB file that is a valid
// filesystem entry but not a valid SQLite file, exercising the bounded
// large-store decision path without allocating a multi-GB artifact.
// setLargeStoreDoctorPath puts a throwaway `traceary` on PATH so the
// bounded report's path check is pass, not fail. CI runners do not have
// the binary on PATH; --warnings-ok does not ignore fail.
func setLargeStoreDoctorPath(t *testing.T) {
	t.Helper()
	current, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "traceary")
	if err := os.Symlink(current, link); err != nil {
		if err := os.WriteFile(link, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("failed to create traceary test executable: %v", err)
		}
	}
	t.Setenv("PATH", dir)
}

func writeLargeStoreFixture(t *testing.T) string {
	t.Helper()
	largeStore := filepath.Join(t.TempDir(), "large-metadata-only.db")
	file, err := os.OpenFile(largeStore, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Truncate(2 << 30); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return largeStore
}

func decodeLargeStoreDoctorReport(t *testing.T, data []byte) doctorReport {
	t.Helper()
	var report doctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v (body=%s)", err, data)
	}
	return report
}

func largeStoreCheckByName(report doctorReport, name string) doctorCheck {
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	return doctorCheck{}
}

func writePluginManifest(t *testing.T, path, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{"name":"traceary","version":"` + version + `"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// TestDoctorLargeStore_HostPackageIdentitySurvivesEarlyReturn drives the
// shipped doctor command against a >=2 GiB sparse store fixture and asserts
// that the bounded metadata-only report still carries the per-host plugin
// version family, produced from host manifests only (#1970).
func TestDoctorLargeStore_HostPackageIdentitySurvivesEarlyReturn(t *testing.T) {
	tests := map[string]struct {
		manifestVersion string
		wantStatus      string
	}{
		"matching manifest version passes":   {manifestVersion: "0.39.0", wantStatus: doctorStatusPass},
		"stale manifest version still warns": {manifestVersion: "0.38.0", wantStatus: doctorStatusWarn},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TRACEARY_LANG", "en")
			setLargeStoreDoctorPath(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			SetUserHomeDirFunc(func() (string, error) { return home, nil })
			t.Cleanup(ResetUserHomeDirFunc)

			writePluginManifest(t, filepath.Join(home, ".codex", "plugins", "cache", "v"+tc.manifestVersion, "traceary", tc.manifestVersion, ".codex-plugin", "plugin.json"), tc.manifestVersion)
			writePluginManifest(t, filepath.Join(home, ".gemini", "extensions", "traceary", "gemini-extension.json"), tc.manifestVersion)
			writePluginManifest(t, filepath.Join(home, ".kimi-code", "plugins", "managed", "traceary", "kimi.plugin.json"), tc.manifestVersion)

			largeStore := writeLargeStoreFixture(t)
			store := &trackingStoreStub{}
			events := &trackingEventStub{}
			capacity := &panicLargeStoreCapacityInspector{}
			codec := &panicLargeStorePayloadCodecInspector{}
			rootCmd := newLargeStoreDoctorRootCLI(store, events, capacity, codec).Command()
			rootCmd.Version = "0.39.0"
			stdout := &bytes.Buffer{}
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"doctor", "--db-path", largeStore, "--json", "--warnings-ok"})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
			if report.Mode != "metadata_only_large_store" {
				t.Fatalf("report.Mode = %q, want metadata_only_large_store", report.Mode)
			}
			if store.initCalled {
				t.Fatal("large-store doctor initialized SQLite, want metadata-only outcome")
			}
			if events.listCalls != 0 {
				t.Fatalf("large-store doctor listed %d events, want no event/payload reads", events.listCalls)
			}
			for _, checkName := range []string{"codex-plugin-version", "gemini-plugin-version", "kimi-plugin-version"} {
				check := largeStoreCheckByName(report, checkName)
				if check.Status != tc.wantStatus {
					t.Fatalf("%s = %#v, want status %s", checkName, check, tc.wantStatus)
				}
			}
		})
	}
}

// TestDoctorLargeStore_FixStaysNonDestructive asserts that --fix against a
// large store applies only guided remediation to the plugin-version checks;
// it must never write a host file (#1970).
func TestDoctorLargeStore_FixStaysNonDestructive(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setLargeStoreDoctorPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	manifestPath := filepath.Join(home, ".gemini", "extensions", "traceary", "gemini-extension.json")
	writePluginManifest(t, manifestPath, "0.38.0")

	largeStore := writeLargeStoreFixture(t)
	store := &trackingStoreStub{}
	events := &trackingEventStub{}
	capacity := &panicLargeStoreCapacityInspector{}
	codec := &panicLargeStorePayloadCodecInspector{}
	rootCmd := newLargeStoreDoctorRootCLI(store, events, capacity, codec).Command()
	rootCmd.Version = "0.39.0"
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", largeStore, "--json", "--warnings-ok", "--fix"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
	if report.Mode != "metadata_only_large_store" {
		t.Fatalf("report.Mode = %q, want metadata_only_large_store", report.Mode)
	}
	var fixLog *doctorFixLog
	for i := range report.Fixes {
		if report.Fixes[i].Name == "gemini-plugin-version" {
			fixLog = &report.Fixes[i]
			break
		}
	}
	if fixLog == nil {
		t.Fatalf("fixes = %#v, want a gemini-plugin-version entry", report.Fixes)
	}
	if !strings.HasPrefix(fixLog.Action, "skip: guided remediation only; run `") {
		t.Fatalf("fix action = %q, want guided-remediation-only skip", fixLog.Action)
	}
	if info, err := os.Stat(manifestPath); err != nil || info.Size() == 0 {
		t.Fatalf("manifest at %s changed or vanished after --fix", manifestPath)
	}
}

// TestDoctorLargeStore_NativeGrokCheckSurvives asserts the native Grok
// plugin activation check survives the bounded early return, and that no
// store-backed check (grok-event-coverage) is present (#1970).
func TestDoctorLargeStore_NativeGrokCheckSurvives(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setLargeStoreDoctorPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	projectDir := t.TempDir()
	pluginHook := filepath.Join(projectDir, "integrations", "grok-plugin", "hooks", "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.99 (build)\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.39.0","path":"/Users/operator/.grok/plugins/traceary-grok"}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary-grok"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	largeStore := writeLargeStoreFixture(t)
	store := &trackingStoreStub{}
	events := &trackingEventStub{}
	capacity := &panicLargeStoreCapacityInspector{}
	codec := &panicLargeStorePayloadCodecInspector{}
	rootCmd := newLargeStoreDoctorRootCLI(store, events, capacity, codec).Command()
	rootCmd.Version = "0.39.0"
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "grok", "--project-dir", projectDir, "--db-path", largeStore, "--json", "--warnings-ok"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
	if report.Mode != "metadata_only_large_store" {
		t.Fatalf("report.Mode = %q, want metadata_only_large_store", report.Mode)
	}
	check := largeStoreCheckByName(report, "grok-plugin")
	if check.Status != doctorStatusPass {
		t.Fatalf("grok-plugin = %#v, want pass", check)
	}
	if store.initCalled {
		t.Fatal("large-store doctor initialized SQLite, want metadata-only outcome")
	}
	for _, forbidden := range []string{"grok-event-coverage", "grok-config"} {
		if largeStoreCheckByName(report, forbidden).Name != "" {
			t.Fatalf("bounded report contains store-backed check %q", forbidden)
		}
	}
}

// TestDoctorLargeStore_NativeKimiCheckSurvives asserts the native Kimi
// plugin activation checks survive the bounded early return (#1970).
func TestDoctorLargeStore_NativeKimiCheckSurvives(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setLargeStoreDoctorPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	originalLookPath, originalOutput := kimiDoctorLookPath, kimiDoctorOutput
	t.Cleanup(func() { kimiDoctorLookPath, kimiDoctorOutput = originalLookPath, originalOutput })
	kimiDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/kimi", nil }
	kimiDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.39.0\n"), nil
		}
		return nil, nil
	}

	manifestDir := filepath.Join(home, ".kimi-code", "plugins", "managed", "traceary")
	for _, skill := range []string{"traceary-memory-remember", "traceary-memory-review", "traceary-session-history", "traceary-session-refine"} {
		if err := os.MkdirAll(filepath.Join(manifestDir, "skills", skill), 0o755); err != nil {
			t.Fatalf("mkdir skill %s: %v", skill, err)
		}
		if err := os.WriteFile(filepath.Join(manifestDir, "skills", skill, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}
	manifest := `{
  "name": "traceary",
  "version": "0.39.0",
  "hooks": [
    {"event": "SessionStart", "command": "traceary hook kimi session-start", "timeout": 10},
    {"event": "SessionEnd", "command": "traceary hook kimi session-end", "timeout": 10},
    {"event": "UserPromptSubmit", "command": "traceary hook kimi user-prompt-submit", "timeout": 10},
    {"event": "PreToolUse", "matcher": "Agent", "command": "traceary hook kimi pre-tool-use", "timeout": 10},
    {"event": "PostToolUse", "command": "traceary hook kimi post-tool-use", "timeout": 10},
    {"event": "PostToolUseFailure", "command": "traceary hook kimi post-tool-use-failure", "timeout": 10},
    {"event": "Stop", "command": "traceary hook kimi stop", "timeout": 10},
    {"event": "SubagentStop", "command": "traceary hook kimi subagent-stop", "timeout": 10},
    {"event": "PreCompact", "command": "traceary hook kimi pre-compact", "timeout": 10},
    {"event": "PostCompact", "command": "traceary hook kimi post-compact", "timeout": 10}
  ]
}`
	if err := os.WriteFile(filepath.Join(manifestDir, "kimi.plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	record := `{"plugins": [{"id": "traceary", "root": "` + manifestDir + `", "source": "local-path", "enabled": true, "state": "ok", "installedAt": "2026-07-19T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(home, ".kimi-code", "plugins", "installed.json"), []byte(record), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}

	largeStore := writeLargeStoreFixture(t)
	store := &trackingStoreStub{}
	events := &trackingEventStub{}
	capacity := &panicLargeStoreCapacityInspector{}
	codec := &panicLargeStorePayloadCodecInspector{}
	rootCmd := newLargeStoreDoctorRootCLI(store, events, capacity, codec).Command()
	rootCmd.Version = "0.39.0"
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "kimi", "--db-path", largeStore, "--json", "--warnings-ok"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
	if report.Mode != "metadata_only_large_store" {
		t.Fatalf("report.Mode = %q, want metadata_only_large_store", report.Mode)
	}
	if check := largeStoreCheckByName(report, "kimi-plugin"); check.Status != doctorStatusPass {
		t.Fatalf("kimi-plugin = %#v, want pass", check)
	}
	if check := largeStoreCheckByName(report, "kimi-plugin-version"); check.Status != doctorStatusPass {
		t.Fatalf("kimi-plugin-version = %#v, want pass", check)
	}
	if store.initCalled {
		t.Fatal("large-store doctor initialized SQLite, want metadata-only outcome")
	}
	if largeStoreCheckByName(report, "kimi-event-coverage").Name != "" {
		t.Fatal("bounded report contains store-backed check kimi-event-coverage")
	}
}

// TestDoctorLargeStore_BoundedSetStaysBounded asserts the additive host
// package identity family does not pull any store-backed check into the
// bounded report (#1970).
func TestDoctorLargeStore_BoundedSetStaysBounded(t *testing.T) {
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
	codec := &panicLargeStorePayloadCodecInspector{}
	rootCmd := newLargeStoreDoctorRootCLI(store, events, capacity, codec).Command()
	rootCmd.Version = "0.39.0"
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", largeStore, "--json", "--warnings-ok"})

	started := time.Now()
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded metadata-only doctor took %s, want <= 1s", elapsed)
	}
	report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
	if report.Mode != "metadata_only_large_store" {
		t.Fatalf("report.Mode = %q, want metadata_only_large_store", report.Mode)
	}
	forbidden := []string{
		"claude-config", "codex-config", "gemini-config",
		"claude-event-coverage", "codex-event-coverage", "gemini-event-coverage",
		"memory-inbox", "store-growth-budget", "version",
	}
	for _, name := range forbidden {
		if got := largeStoreCheckByName(report, name); got.Name != "" {
			t.Fatalf("bounded report unexpectedly contains store-backed check %q: %#v", name, got)
		}
	}
	diagnostics := largeStoreCheckByName(report, "large-store-diagnostics")
	if diagnostics.Status != doctorStatusWarn {
		t.Fatalf("large-store-diagnostics = %#v, want warn", diagnostics)
	}
}
