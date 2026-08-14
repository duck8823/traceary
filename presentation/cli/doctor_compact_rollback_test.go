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
		if !strings.Contains(check.Hint, "compact rollback") {
			t.Fatalf("Hint = %q", check.Hint)
		}
	})
}
