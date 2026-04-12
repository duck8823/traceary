//go:build unix

package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSafeMkdirAll_CreatesNestedDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "c")
	if err := safeMkdirAll(target, 0o755); err != nil {
		t.Fatalf("safeMkdirAll() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat().IsDir() = false, want true")
	}
}

func TestSafeMkdirAll_RefusesAncestorSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := t.TempDir()
	link := filepath.Join(dir, "ancestor")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	target := filepath.Join(link, "a", "b")
	if err := safeMkdirAll(target, 0o755); err == nil {
		t.Fatalf("safeMkdirAll() error = nil, want symlink refusal")
	}

	entries, err := os.ReadDir(victim)
	if err != nil {
		t.Fatalf("ReadDir(victim) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("victim entries = %d, want 0", len(entries))
	}
}

func TestSafeReadFile_ReadsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	data, err := safeReadFile(path)
	if err != nil {
		t.Fatalf("safeReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want %q", data, "hello")
	}
}

func TestSafeReadFile_ReturnsNotExistForMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := safeReadFile(filepath.Join(dir, "missing"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("safeReadFile() error = %v, want fs.ErrNotExist", err)
	}
}

func TestSafeReadFile_RefusesLeafSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := safeReadFile(link); err == nil {
		t.Fatalf("safeReadFile() error = nil, want symlink refusal")
	}
}

func TestSafeReadFile_RefusesAncestorSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := t.TempDir()
	if err := os.WriteFile(filepath.Join(victim, "settings.json"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	link := filepath.Join(dir, "ancestor")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := safeReadFile(filepath.Join(link, "settings.json")); err == nil {
		t.Fatalf("safeReadFile() error = nil, want symlink refusal")
	}
}

func TestSafeWriteFile_CreatesNewFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := safeWriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("safeWriteFile() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got = %q, want %q", got, "hello")
	}
}

func TestSafeWriteFile_OverwritesExistingRegularFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := safeWriteFile(path, []byte("fresh"), 0o644); err != nil {
		t.Fatalf("safeWriteFile() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "fresh" {
		t.Fatalf("got = %q, want %q", got, "fresh")
	}
}

func TestSafeWriteFile_RefusesLeafSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("untouchable"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if err := safeWriteFile(link, []byte("evil"), 0o644); err == nil {
		t.Fatalf("safeWriteFile() error = nil, want symlink refusal")
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile(victim) error = %v", err)
	}
	if string(got) != "untouchable" {
		t.Fatalf("victim = %q, want %q", got, "untouchable")
	}
}

func TestSafeWriteFile_RefusesAncestorSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := t.TempDir()
	link := filepath.Join(dir, "ancestor")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	target := filepath.Join(link, "file")
	if err := safeWriteFile(target, []byte("evil"), 0o644); err == nil {
		t.Fatalf("safeWriteFile() error = nil, want symlink refusal")
	}
	entries, err := os.ReadDir(victim)
	if err != nil {
		t.Fatalf("ReadDir(victim) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("victim entries = %d, want 0", len(entries))
	}
}

func TestSafeChmod_ChangesPermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := safeChmod(path, 0o755); err != nil {
		t.Fatalf("safeChmod() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("Mode().Perm() = %o, want %o", info.Mode().Perm(), 0o755)
	}
}

// TestSafeWriteFile_RacingAncestorSwap exercises the TOCTOU race Codex
// previously reproduced against the path-precheck implementation. A
// goroutine loops swapping an ancestor between a regular directory and
// a symlink targetting the victim tree while safeWriteFile tries to
// materialize a file under that ancestor. The fd-pinned traversal must
// never write into the victim directory regardless of how many times
// the swap happens during a single call.
func TestSafeWriteFile_RacingAncestorSwap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := t.TempDir()
	ancestor := filepath.Join(root, "ancestor")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.RemoveAll(ancestor)
			_ = os.Symlink(victim, ancestor)
			_ = os.Remove(ancestor)
			_ = os.Mkdir(ancestor, 0o755)
		}
	}()

	const attempts = 200
	for i := 0; i < attempts; i++ {
		_ = safeWriteFile(filepath.Join(ancestor, "leaked"), []byte("leaked"), 0o644)
	}
	close(stop)
	wg.Wait()

	entries, err := os.ReadDir(victim)
	if err != nil {
		t.Fatalf("ReadDir(victim) error = %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("victim has %d entries after race, want 0 (%v)", len(entries), names)
	}
}
