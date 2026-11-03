//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	schema "github.com/duck8823/traceary/schema/sqlite"
)

func TestRuntimeHistoryCLIActualProcessUsesExplicitStoreWithoutBootstrap(t *testing.T) {
	baseDir := os.TempDir()
	if runtime.GOOS == "darwin" {
		baseDir = "/private/tmp"
	}
	root, err := os.MkdirTemp(baseDir, "thp-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "-C", project, "init", "--quiet")
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	store := filepath.Join(root, "core.db")
	migrations, err := schema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	db := sqlite.NewDatabase(store, migrations)
	if err = sqlite.NewStoreManagementDatasource(db).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	e := model.EventOf("history-fixture", types.EventKindNote, "cli", "fixture", "session", types.Workspace(project), "private synthetic body must not appear", base)
	if err = sqlite.NewEventDatasource(db).Save(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	poison := filepath.Join(root, "must-not-create", "default.db")
	metadata := filepath.Join(root, "rt")
	command := func(args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestDaemonCommandProcessHelper$")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACEARY_LANG=en", "LOG_LEVEL=error", "TRACEARY_TEST_DAEMON_HELPER=1", "TRACEARY_TEST_DAEMON_ARGS=" + string(data), "TRACEARY_DB_PATH=" + poison, "GORACE=atexit_sleep_ms=0"}
		return cmd
	}
	read := func(args ...string) map[string]any {
		t.Helper()
		out, err := command(args...).CombinedOutput()
		if err != nil {
			t.Fatalf("history CLI failed %v %.1000s", err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "{") {
				var result map[string]any
				if err = json.Unmarshal([]byte(line), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
		}
		t.Fatal("no history JSON")
		return nil
	}
	p := read("runtime", "project", "register", "--runtime-dir", metadata, "--path", project)
	w := read("runtime", "worktree", "register", "--runtime-dir", metadata, "--project", p["id"].(string), "--path", project, "--db-path", store)
	server := command("daemon", "serve", "--runtime-dir", metadata)
	var stderr bytes.Buffer
	server.Stderr = &stderr
	server.Stdout = &stderr
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait(); close(done) }()
	defer func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = server.Process.Kill()
			<-done
		}
	}()
	client, err := infra.NewLocalDaemon(nil, infra.DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	epoch := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := client.Status(ctx, metadata)
		if err == nil {
			epoch = status.Epoch
			break
		}
		select {
		case err := <-done:
			t.Fatalf("history daemon exited %v %.1000s", err, stderr.String())
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	if epoch == "" {
		t.Fatal("history daemon not ready")
	}
	if err = client.HintHistory(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	view := read("runtime", "history", "page", "--runtime-dir", metadata, "--epoch", epoch, "--worktree", w["id"].(string), "--from", base.Format(time.RFC3339Nano), "--to", base.Add(time.Hour).Format(time.RFC3339Nano), "--limit", "1")
	events := view["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["id"] != "history-fixture" || view["hint_revision"] != "1" || view["more"] != false || view["native_target_revalidated"] != false {
		t.Fatal("actual CLI lost history boundaries", view)
	}
	data, _ := json.Marshal(view)
	if bytes.Contains(data, []byte("private synthetic body")) {
		t.Fatal("history hydrated body")
	}
	after, err := os.ReadFile(store)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("history CLI changed committed store", err)
	}
	if _, err = os.Stat(filepath.Dir(poison)); !os.IsNotExist(err) {
		t.Fatal("history CLI bootstrapped default store")
	}
	executions, err := infra.OpenExecutionStore(metadata, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := executions.ListLaunches(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatal("history implicitly launched an agent", err)
	}
	// Exercise ordinary capture through the actual main composition, not a
	// manually invoked SDK hint. Core write/receipt survive optional routing.
	capture := func(route, kind string) ([]byte, error) {
		cmd := command("log", "--json", "--session-id", "explicit-fixture", "--workspace", project, "--kind", kind, "synthetic committed note")
		for i, entry := range cmd.Env {
			if strings.HasPrefix(entry, "TRACEARY_DB_PATH=") {
				cmd.Env[i] = "TRACEARY_DB_PATH=" + store
			}
		}
		cmd.Env = append(cmd.Env, "HOME="+root, "TRACEARY_RUNTIME_DIR="+route)
		return cmd.CombinedOutput()
	}
	out, err := capture(metadata, "note")
	if err != nil {
		t.Fatalf("routed capture failed: %v %.1000s", err, out)
	}
	var receipt struct {
		EventID string `json:"event_id"`
	}
	decodeReceipt := func(data []byte) error {
		// The selected Go subprocess fixture appends its own PASS line after
		// run returns; the actual CLI must emit exactly one JSON document.
		document, found := bytes.CutSuffix(bytes.TrimSpace(data), []byte("\nPASS"))
		if !found {
			return errors.New("unexpected capture stdout")
		}
		receipt.EventID = ""
		return json.Unmarshal(document, &receipt)
	}
	if err = decodeReceipt(out); err != nil || receipt.EventID == "" {
		t.Fatal("capture receipt changed", err)
	}
	view = read("runtime", "history", "page", "--runtime-dir", metadata, "--epoch", epoch, "--worktree", w["id"].(string), "--from", base.Format(time.RFC3339Nano), "--to", base.Add(time.Hour).Format(time.RFC3339Nano), "--limit", "1")
	if view["hint_revision"] != "2" {
		t.Fatal("committed capture did not notify daemon")
	}
	rows, err := sqlite.NewEventDatasource(sqlite.NewDatabase(store, nil)).ListRecentMetadata(ctx, apptypes.NewEventListCriteriaBuilder(10).Build())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		found = found || row.EventID().String() == receipt.EventID
	}
	if !found {
		t.Fatal("hint acknowledged without committed capture")
	}
	if _, err = capture(metadata, "invalid-kind"); err == nil {
		t.Fatal("invalid capture unexpectedly succeeded")
	}
	view = read("runtime", "history", "page", "--runtime-dir", metadata, "--epoch", epoch, "--worktree", w["id"].(string), "--from", base.Format(time.RFC3339Nano), "--to", base.Add(time.Hour).Format(time.RFC3339Nano), "--limit", "1")
	if view["hint_revision"] != "2" {
		t.Fatal("failed capture emitted a commit hint")
	}
	// An existing private root with no daemon must not launch a background
	// service or turn a successful primary write into an error.
	absent := filepath.Join(root, "absent")
	if err = os.Mkdir(absent, 0700); err != nil {
		t.Fatal(err)
	}
	out, err = capture(absent, "note")
	if err != nil || decodeReceipt(out) != nil || receipt.EventID == "" {
		t.Fatal("absent optional daemon changed capture", err)
	}
	entries, err := os.ReadDir(absent)
	if err != nil || len(entries) != 0 {
		t.Fatal("capture started a daemon or created metadata", err)
	}
}
