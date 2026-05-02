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
