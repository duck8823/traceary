package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectCompactRollbackCopies(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")

	t.Run("pass when no sibling exists", func(t *testing.T) {
		dir := t.TempDir()
		check := inspectCompactRollbackCopies(filepath.Join(dir, "traceary.db"))
		if check.Status != doctorStatusPass {
			t.Fatalf("Status = %q, want pass; %+v", check.Status, check)
		}
	})

	t.Run("warn names the retained file and size", func(t *testing.T) {
		dir := t.TempDir()
		db := filepath.Join(dir, "traceary.db")
		rollback := db + ".rollback-abc123"
		if err := os.WriteFile(rollback, []byte("old-store"), 0o600); err != nil {
			t.Fatal(err)
		}
		check := inspectCompactRollbackCopies(db)
		if check.Name != "compact-rollback-copy" {
			t.Fatalf("Name = %q", check.Name)
		}
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
		if !strings.Contains(check.Message, rollback) {
			t.Fatalf("Message = %q, want path", check.Message)
		}
		if !strings.Contains(check.Message, "reclaimable") {
			t.Fatalf("Message = %q, want reclaimable bytes", check.Message)
		}
		if !strings.Contains(check.Hint, "doctor --fix") {
			t.Fatalf("Hint = %q, want doctor --fix", check.Hint)
		}
		if !check.AutoFixAvailable || check.StructuredFixFunc == nil {
			t.Fatalf("want auto-fix, got %+v", check)
		}
	})

	t.Run("dry-run keeps the accepted rollback copy", func(t *testing.T) {
		dir := t.TempDir()
		db := filepath.Join(dir, "traceary.db")
		rollback := db + ".rollback-abc123"
		if err := os.WriteFile(rollback, []byte("old-store"), 0o600); err != nil {
			t.Fatal(err)
		}
		check := inspectCompactRollbackCopies(db)
		result, err := check.StructuredFixFunc(t.Context(), true)
		if err != nil {
			t.Fatal(err)
		}
		if result.Metrics["removed"] != 1 {
			t.Fatalf("metrics=%v", result.Metrics)
		}
		if _, err := os.Lstat(rollback); err != nil {
			t.Fatalf("dry-run removed %s: %v", rollback, err)
		}
	})

	t.Run("fix removes the accepted rollback copy", func(t *testing.T) {
		dir := t.TempDir()
		db := filepath.Join(dir, "traceary.db")
		rollback := db + ".rollback-abc123"
		if err := os.WriteFile(rollback, []byte("old-store"), 0o600); err != nil {
			t.Fatal(err)
		}
		check := inspectCompactRollbackCopies(db)
		if _, err := check.StructuredFixFunc(t.Context(), false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(rollback); !os.IsNotExist(err) {
			t.Fatalf("rollback still present: %v", err)
		}
		after := inspectCompactRollbackCopies(db)
		if after.Status != doctorStatusPass {
			t.Fatalf("after status=%q", after.Status)
		}
	})

	t.Run("symlink rollback is not unlinked", func(t *testing.T) {
		dir := t.TempDir()
		db := filepath.Join(dir, "traceary.db")
		target := filepath.Join(dir, "real-old")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		rollback := db + ".rollback-abc123"
		if err := os.Symlink(target, rollback); err != nil {
			t.Fatal(err)
		}
		check := inspectCompactRollbackCopies(db)
		if check.Status != doctorStatusPass {
			t.Fatalf("symlink should not count as retained copy: %+v", check)
		}
		if check.StructuredFixFunc != nil {
			t.Fatal("unexpected fix on pass check")
		}
		if _, err := os.Lstat(target); err != nil {
			t.Fatalf("symlink target was removed: %v", err)
		}
	})
}
