package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation"
)

type fakePageMetadataInspector struct {
	meta  apptypes.StorePageMetadata
	err   error
	calls int
}

func (f *fakePageMetadataInspector) InspectPageMetadata(context.Context, string) (apptypes.StorePageMetadata, error) {
	f.calls++
	return f.meta, f.err
}

func writeSparseStore(t *testing.T, size int64) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "traceary.db")
	f, err := os.Create(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return store
}

func writeReclaimWarnConfig(t *testing.T, jsonBody string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(jsonBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runReclaimWarningCommand(t *testing.T, cli *RootCLI, store string) string {
	t.Helper()
	root := &cobra.Command{Use: "traceary"}
	leaf := &cobra.Command{Use: "list", Run: func(_ *cobra.Command, _ []string) {}}
	root.PersistentFlags().String("db-path", "", "")
	root.AddCommand(leaf)
	cli.attachCompactReclaimWarning(root)
	errBuf := &bytes.Buffer{}
	root.SetOut(&bytes.Buffer{})
	root.SetErr(errBuf)
	root.SetArgs([]string{"list", "--db-path", store})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return errBuf.String()
}

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

func TestEmitCompactReclaimWarning_LargeFileWithEmptyFreelistStaysSilent(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 2<<30)
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 2 << 30, ReclaimableBytes: 0}}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence for empty freelist", stderr)
	}
	if _, err := os.Stat(store + ".reclaim-warn"); !os.IsNotExist(err) {
		t.Fatalf("marker exists; want none: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("inspector calls = %d, want 1", fake.calls)
	}
}

func TestEmitCompactReclaimWarning_ReclaimableAtThresholdWarnsOncePerDay(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	if got := presentation.LoadConfig().Compact.ReclaimWarnBytes; got != 1<<30 {
		t.Fatalf("ReclaimWarnBytes = %d, want 1GiB", got)
	}
	store := writeSparseStore(t, 4<<30)
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 4 << 30, ReclaimableBytes: 1 << 30}}
	cli := &RootCLI{pageMetadataInspector: fake}
	now := time.Date(2026, 8, 16, 15, 0, 0, 0, time.UTC)
	compactReclaimNow = func() time.Time { return now }
	t.Cleanup(func() { compactReclaimNow = time.Now })

	first := runReclaimWarningCommand(t, cli, store)
	if !strings.Contains(first, "can reclaim") {
		t.Fatalf("first emit = %q, want reclaim trailer", first)
	}
	compactReclaimNow = func() time.Time { return now.Add(time.Hour) }
	second := runReclaimWarningCommand(t, cli, store)
	if strings.Contains(second, "can reclaim") {
		t.Fatalf("second emit = %q, want rate-limited silence", second)
	}
	compactReclaimNow = func() time.Time { return now.Add(25 * time.Hour) }
	third := runReclaimWarningCommand(t, cli, store)
	if !strings.Contains(third, "can reclaim") {
		t.Fatalf("third emit = %q, want trailer after 25h", third)
	}
}

func TestEmitCompactReclaimWarning_ReclaimableBelowRatioStaysSilent(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 40<<30)
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 40 << 30, ReclaimableBytes: 1 << 30}}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence below 10 percent", stderr)
	}
	if _, err := os.Stat(store + ".reclaim-warn"); !os.IsNotExist(err) {
		t.Fatalf("marker exists; want none: %v", err)
	}
}

func TestEmitCompactReclaimWarning_SmallFileSkipsInspection(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 100<<20)
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 100 << 20, ReclaimableBytes: 100 << 20}}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence for small file", stderr)
	}
	if fake.calls != 0 {
		t.Fatalf("inspector calls = %d, want 0", fake.calls)
	}
}

func TestEmitCompactReclaimWarning_SuppressedMarkerSkipsInspection(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 4<<30)
	now := time.Date(2026, 8, 16, 15, 0, 0, 0, time.UTC)
	if err := recordCompactReclaimWarning(store, now); err != nil {
		t.Fatal(err)
	}
	compactReclaimNow = func() time.Time { return now.Add(time.Hour) }
	t.Cleanup(func() { compactReclaimNow = time.Now })
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 4 << 30, ReclaimableBytes: 1 << 30}}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence while marker suppresses", stderr)
	}
	if fake.calls != 0 {
		t.Fatalf("inspector calls = %d, want 0", fake.calls)
	}
}

func TestEmitCompactReclaimWarning_InspectorErrorStaysSilent(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 4<<30)
	fake := &fakePageMetadataInspector{err: errors.New("pragma failed")}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence on inspector error", stderr)
	}
	if _, err := os.Stat(store + ".reclaim-warn"); !os.IsNotExist(err) {
		t.Fatalf("marker exists; want none: %v", err)
	}
}

func TestEmitCompactReclaimWarning_NoInspectorStaysSilent(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 4<<30)
	stderr := runReclaimWarningCommand(t, &RootCLI{}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence with nil inspector", stderr)
	}
	if _, err := os.Stat(store + ".reclaim-warn"); !os.IsNotExist(err) {
		t.Fatalf("marker exists; want none: %v", err)
	}
}

func TestEmitCompactReclaimWarning_ConfigZeroDisables(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":0}}`)
	if got := presentation.LoadConfig().Compact.ReclaimWarnBytes; got != 0 {
		t.Fatalf("ReclaimWarnBytes = %d, want 0", got)
	}
	store := writeSparseStore(t, 4<<30)
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 4 << 30, ReclaimableBytes: 4 << 30}}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if strings.Contains(stderr, "can reclaim") {
		t.Fatalf("stderr = %q, want silence when disabled", stderr)
	}
	if fake.calls != 0 {
		t.Fatalf("inspector calls = %d, want 0", fake.calls)
	}
}

func TestEmitCompactReclaimWarning_MessageReportsReclaimableSize(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	writeReclaimWarnConfig(t, `{"compact":{"reclaim_warn_bytes":1073741824}}`)
	store := writeSparseStore(t, 4<<30)
	fake := &fakePageMetadataInspector{meta: apptypes.StorePageMetadata{DatabaseBytes: 4 << 30, ReclaimableBytes: 1 << 30}}
	stderr := runReclaimWarningCommand(t, &RootCLI{pageMetadataInspector: fake}, store)
	if !strings.Contains(stderr, "can reclaim about 1.0 GiB") {
		t.Fatalf("stderr = %q, want reclaimable figure", stderr)
	}
}
