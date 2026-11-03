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
	"syscall"
	"testing"
	"time"

	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestDaemonCommandProcessHelper(t *testing.T) {
	if os.Getenv("TRACEARY_TEST_DAEMON_HELPER") != "1" {
		t.Skip("subprocess-only fixture")
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("TRACEARY_TEST_DAEMON_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"traceary"}, args...)
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
func TestDaemonCLIRealProcessWithoutHomeOrCoreStore(t *testing.T) {
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tdm-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		data, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(bounded, binary, "-test.run=^TestDaemonCommandProcessHelper$")
		// HOME/DB env deliberately absent: only the explicit runtime root is usable.
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACEARY_LANG=en", "LOG_LEVEL=error", "TRACEARY_TEST_DAEMON_HELPER=1", "TRACEARY_TEST_DAEMON_ARGS=" + string(data)}
		return cmd
	}
	server := command("daemon", "serve", "--runtime-dir", root)
	var serverErrors bytes.Buffer
	server.Stderr = &serverErrors
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
			return
		default:
		}
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
	ready := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = client.Status(bounded, root); err == nil {
			ready = true
			break
		}
		select {
		case err := <-done:
			t.Fatalf("server exited: %v %s", err, serverErrors.String())
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ready {
		t.Fatal("CLI foreground server never became ready")
	}
	core := filepath.Join(root, "never-created", "core.db")
	statusCommand := command("daemon", "status", "--runtime-dir", root, "--json")
	statusCommand.Env = append(statusCommand.Env, "TRACEARY_DB_PATH="+core)
	output, err := statusCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("status process: %v %s", err, output)
	}
	var body map[string]any
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "{") {
			if err = json.Unmarshal([]byte(line), &body); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if body["state"] != "listening" || body["protocol_version"] != float64(1) {
		t.Fatal("CLI did not emit bounded readiness")
	}
	if _, err = os.Stat(filepath.Dir(core)); !os.IsNotExist(err) {
		t.Fatal("daemon command initialized core data")
	}
	if err = server.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("daemon did not stop gracefully: %v %s", err, serverErrors.String())
		}
	case <-bounded.Done():
		t.Fatal("daemon did not stop")
	}
	for _, name := range []string{"daemon.sock", "observations.sock"} {
		if _, err = os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("owned socket not cleaned")
		}
	}
}
