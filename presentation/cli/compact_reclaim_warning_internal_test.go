package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/presentation"
)

func TestShouldEmitCompactReclaimWarningRateLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := filepath.Join(dir, "traceary.db")
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if !shouldEmitCompactReclaimWarning(store, now) {
		t.Fatal("missing state file should emit")
	}
	if err := recordCompactReclaimWarning(store, now); err != nil {
		t.Fatal(err)
	}
	if shouldEmitCompactReclaimWarning(store, now.Add(23*time.Hour)) {
		t.Fatal("should not emit again within 24h")
	}
	if !shouldEmitCompactReclaimWarning(store, now.Add(24*time.Hour)) {
		t.Fatal("should emit after 24h")
	}
}

func TestEmitCompactReclaimWarningOncePerDay(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"compact":{"reclaim_warn_bytes":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := presentation.LoadConfig().Compact.ReclaimWarnBytes; got != 1 {
		t.Fatalf("ReclaimWarnBytes = %d, want 1", got)
	}

	store := filepath.Join(t.TempDir(), "traceary.db")
	if err := os.WriteFile(store, []byte("xx"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 15, 0, 0, 0, time.UTC)
	compactReclaimNow = func() time.Time { return now }
	t.Cleanup(func() { compactReclaimNow = time.Now })

	root := &cobra.Command{Use: "traceary"}
	leaf := &cobra.Command{Use: "list", Run: func(_ *cobra.Command, _ []string) {}}
	root.PersistentFlags().String("db-path", "", "")
	root.AddCommand(leaf)
	attachCompactReclaimWarning(root)

	run := func() string {
		errBuf := &bytes.Buffer{}
		root.SetOut(&bytes.Buffer{})
		root.SetErr(errBuf)
		root.SetArgs([]string{"list", "--db-path", store})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return errBuf.String()
	}

	first := run()
	if !strings.Contains(first, "store can reclaim space") {
		t.Fatalf("first emit = %q, want reclaim trailer", first)
	}
	second := run()
	if strings.Contains(second, "store can reclaim space") {
		t.Fatalf("second emit = %q, want rate-limited silence", second)
	}
}
