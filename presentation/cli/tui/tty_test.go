package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestIsTerminal_NonFileWriter(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Fatal("bytes.Buffer must not report as terminal")
	}
}

func TestIsTerminal_RegularFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if IsTerminal(f) {
		t.Fatal("regular file must not report as terminal")
	}
	if IsTerminalFile(f) {
		t.Fatal("regular file must not report as terminal")
	}
}

func TestIsTerminalFile_Nil(t *testing.T) {
	if IsTerminalFile(nil) {
		t.Fatal("nil file must not report as terminal")
	}
}

func TestInteractive_RequiresBothEnds(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "io.txt"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if Interactive(f, f) {
		t.Fatal("regular file pair must not be interactive")
	}
	if Interactive(nil, f) {
		t.Fatal("nil stdin must not be interactive")
	}
}
