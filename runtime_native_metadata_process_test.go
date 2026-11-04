//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeSessionMetadataProcessSkipsHomeAndCoreBootstrap(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	poison := filepath.Join(dir, "must-not-create", "core.db")
	slot := filepath.Join(dir, "not-an-internal-slot")
	args, err := json.Marshal([]string{"runtime", "native-session-start", "--slot", slot})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestDaemonCommandProcessHelper$")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACEARY_LANG=en", "LOG_LEVEL=error", "TRACEARY_TEST_DAEMON_HELPER=1", "TRACEARY_TEST_DAEMON_ARGS=" + string(args), "TRACEARY_DB_PATH=" + poison, "GORACE=atexit_sleep_ms=0"}
	cmd.Stdin = strings.NewReader("synthetic malformed native stdin must not be printed")
	out, err := cmd.CombinedOutput()
	// The shared subprocess fixture returns to Go's test runner, which emits
	// exactly PASS. Any application stdout/stderr would add bytes to this.
	if err != nil || string(out) != "PASS\n" {
		t.Fatalf("optional helper failed or emitted output without HOME: %q %v", out, err)
	}
	for _, path := range []string{filepath.Dir(poison), slot} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("helper created non-private metadata or core store: %v", err)
		}
	}
}
