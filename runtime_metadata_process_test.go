//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"golang.org/x/xerrors"
)

func TestRuntimeMetadataCLIActualProcessWithoutHomePreservesCoreRegistryAndProfiles(t *testing.T) {
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tmp-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(root, "rt")
	core := filepath.Join(root, "core.db")
	coreBytes := []byte("synthetic existing core identity; must never be opened as SQLite")
	if err = os.WriteFile(core, coreBytes, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := domain.ProjectOf(filepath.Join(root, "project"), filepath.Join(root, "project/.git"), "synthetic-project")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), p.Root(), p.CommonDir(), core, "synthetic-worktree")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := infra.NewRegistryStore(metadata, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = registry.Update(ctx, func(r *domain.Registry) error {
		if e := r.AddProject(p); e != nil {
			return xerrors.Errorf("register synthetic project: %w", e)
		}
		return r.AddWorktree(w)
	}); err != nil {
		t.Fatal(err)
	}
	target, err := domain.TargetOf(w, "codex-fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/synthetic/never-executed", ExecutableWitness: "synthetic-profile", Version: "fixture1"})
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}
	descriptor, err := domain.LaunchDescriptorOf(identity, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := infra.OpenExecutionStore(metadata, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = execution.RegisterProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err = execution.ReserveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	receipt, err := domain.ControlReceiptFrom(domain.ControlSnapshot{Identity: identity, Intent: domain.ControlIntentState{RequestID: "manual-fixture", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 5}, Outcome: domain.ControlPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = execution.ReserveControl(ctx, app.OutputSelection{Identity: identity, Worktree: w.ID(), Profile: profile.ID()}, receipt); err != nil {
		t.Fatal(err)
	}
	registryBefore, err := os.ReadFile(filepath.Join(metadata, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(root, "must-not-create", "core.db")
	run := func(args ...string) ([]byte, error) {
		arguments, _ := json.Marshal(append([]string{"runtime", "metadata"}, args...))
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestDaemonCommandProcessHelper$")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACEARY_LANG=en", "LOG_LEVEL=error", "TRACEARY_TEST_DAEMON_HELPER=1", "TRACEARY_TEST_DAEMON_ARGS=" + string(arguments), "TRACEARY_DB_PATH=" + poison, "GORACE=atexit_sleep_ms=0"}
		return cmd.CombinedOutput()
	}
	read := func(value any, args ...string) {
		t.Helper()
		out, e := run(args...)
		if e != nil {
			t.Fatalf("metadata CLI failed: %v %.1000s", e, out)
		}
		document, ok := bytes.CutSuffix(bytes.TrimSpace(out), []byte("\nPASS"))
		if !ok || json.Unmarshal(document, value) != nil {
			t.Fatal("metadata CLI changed stdout contract")
		}
	}
	var exported app.MetadataExport
	read(&exported, "export", "--runtime-dir", metadata)
	if len(exported.Runs) != 1 || exported.TotalReceipts != 1 || exported.Authority != "persisted_metadata_not_liveness" {
		t.Fatal("actual CLI lost audit summary")
	}
	var preview app.MetadataForgetPreview
	read(&preview, "forget-preview", "--runtime-dir", metadata)
	if !preview.RecoveryRecordLoss || !preview.OfflineRequired || preview.Snapshot.Digest != exported.Digest {
		t.Fatal("actual CLI lost preview binding/loss warning")
	}
	if _, err = run("forget", "--runtime-dir", metadata, "--preview-digest", preview.Snapshot.Digest, "--acknowledge-forget-history"); err == nil {
		t.Fatal("actual CLI accepted missing recovery loss acknowledgement")
	}
	var result app.MetadataForgetResult
	read(&result, "forget", "--runtime-dir", metadata, "--preview-digest", preview.Snapshot.Digest, "--acknowledge-forget-history", "--acknowledge-recovery-record-loss")
	if result.Outcome != "forgotten" || result.Runs != 1 || result.Receipts != 1 {
		t.Fatal("actual CLI did not forget confirmed exact scope")
	}
	if _, err = run("forget", "--runtime-dir", metadata, "--preview-digest", preview.Snapshot.Digest, "--acknowledge-forget-history", "--acknowledge-recovery-record-loss"); err == nil {
		t.Fatal("actual CLI accepted stale replay")
	}
	read(&exported, "export", "--runtime-dir", metadata)
	if len(exported.Runs) != 0 || exported.TotalReceipts != 0 || len(exported.Profiles) != 1 {
		t.Fatal("actual CLI left invalid history or erased profiles")
	}
	registryAfter, _ := os.ReadFile(filepath.Join(metadata, "registry.json"))
	coreAfter, _ := os.ReadFile(core)
	if !bytes.Equal(registryBefore, registryAfter) || !bytes.Equal(coreBytes, coreAfter) {
		t.Fatal("actual CLI modified Registry or core")
	}
	missing := filepath.Join(root, "missing")
	if _, err = run("export", "--runtime-dir", missing); err == nil {
		t.Fatal("missing root accepted")
	}
	for _, path := range []string{missing, filepath.Dir(poison), filepath.Join(metadata, "daemon.sock"), filepath.Join(metadata, "observations.sock")} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("metadata CLI created default data/root/daemon")
		}
	}
}
