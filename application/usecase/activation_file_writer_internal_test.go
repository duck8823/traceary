package usecase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOSActivationFileWriter_ReadIfExistsRejectsSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	link := filepath.Join(dir, "traceary.md")
	if err := os.WriteFile(target, []byte("secret target\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	_, _, err := osActivationFileWriter{}.ReadIfExists(link)
	if err == nil || !strings.Contains(err.Error(), "symlinks are not supported") {
		t.Fatalf("ReadIfExists error = %v, want symlink rejection", err)
	}
}

func TestOSActivationFileWriter_ReadIfExistsRejectsDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, _, err := osActivationFileWriter{}.ReadIfExists(dir)
	if err == nil || !strings.Contains(err.Error(), "activation target is a directory") {
		t.Fatalf("ReadIfExists error = %v, want directory rejection", err)
	}
}

func TestOSActivationFileWriter_WriteAtomicCreatesNewFileWithRestrictedPermissions(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "nested", "traceary.md")
	if err := (osActivationFileWriter{}).WriteAtomic(target, "managed\n"); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "managed\n" {
		t.Fatalf("content = %q, want managed block", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
}

func TestOSActivationFileWriter_WriteAtomicPreservesExistingPermissions(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "traceary.md")
	if err := os.WriteFile(target, []byte("old\n"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	if err := (osActivationFileWriter{}).WriteAtomic(target, "new\n"); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "new\n" {
		t.Fatalf("content = %q, want replacement", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want preserved 0640", got)
	}
}
