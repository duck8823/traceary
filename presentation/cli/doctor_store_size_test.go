package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectStoreSizeBudget_MissingFilePasses(t *testing.T) {
	check := inspectStoreSizeBudget(filepath.Join(t.TempDir(), "missing.db"))
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
}

func TestInspectStoreSizeBudget_SmallFilePasses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.db")
	if err := os.WriteFile(path, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := inspectStoreSizeBudget(path)
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "within") {
		t.Fatalf("message = %q", check.Message)
	}
}

func TestInspectStoreSizeBudget_LargeFileWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse-ish: Seek past the warn threshold then write one byte.
	if _, err := f.Seek(storeSizeWarnBytes, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	check := inspectStoreSizeBudget(path)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "large") {
		t.Fatalf("message = %q", check.Message)
	}
	if !strings.Contains(check.Hint, "store gc") {
		t.Fatalf("hint = %q", check.Hint)
	}
}

func TestFormatByteSize(t *testing.T) {
	if got := formatByteSize(512); got != "512 B" {
		t.Fatalf("got %q", got)
	}
	if got := formatByteSize(storeSizeWarnBytes); !strings.Contains(got, "GiB") {
		t.Fatalf("got %q", got)
	}
}
