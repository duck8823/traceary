package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/presentation/cli"
)

var removedArchiveRetentionFlags = []string{
	"archive",
	"archive-verify",
	"archive-restore",
	"archive-max-age",
	"archive-max-count",
	"archive-max-allocated-bytes",
	"archive-root",
	"backup-max-age",
	"backup-max-count",
	"backup-max-allocated-bytes",
	"backup-root",
	"retention-plan",
	"retention-apply",
	"confirm-plan-id",
	"plan",
	"expires-after",
	"output",
	"passphrase-env",
	"delete-after-verify",
	"dry-run",
	"keep-days",
	"target",
}

func TestRootCLI_StoreCompactRejectsRemovedArchiveFlags(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	db := filepath.Join(t.TempDir(), "traceary.db")
	boolFlags := map[string]struct{}{
		"archive": {}, "retention-plan": {}, "retention-apply": {},
		"delete-after-verify": {}, "dry-run": {},
	}
	for _, name := range removedArchiveRetentionFlags {
		t.Run("--"+name, func(t *testing.T) {
			root := cli.NewRootCLI().Command()
			root.SetOut(&strings.Builder{})
			root.SetErr(&strings.Builder{})
			args := []string{"store", "compact", "--" + name, "--db-path", db}
			if _, ok := boolFlags[name]; !ok {
				args = []string{"store", "compact", "--" + name, "value", "--db-path", db}
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want unknown flag", args)
			}
			if !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("Execute(%v) error = %q, want Cobra unknown-flag", args, err.Error())
			}
		})
	}
}

func TestRootCLI_StoreCompactHelpOmitsArchiveRetentionFlags(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := cli.NewRootCLI().Command()
	compact := mustFindCommand(t, root, "store", "compact")
	help := compact.Flags().FlagUsages()
	for _, name := range removedArchiveRetentionFlags {
		if compact.Flags().Lookup(name) != nil {
			t.Fatalf("store compact still registers --%s", name)
		}
		if strings.Contains(help, "--"+name) {
			t.Fatalf("store compact --help still mentions --%s", name)
		}
	}
}

func TestRootCLI_ArchiveRestoreHasNoBespokeMessage(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := cli.NewRootCLI().Command()
	root.SetOut(&strings.Builder{})
	errBuf := &strings.Builder{}
	root.SetErr(errBuf)
	root.SetArgs([]string{"store", "compact", "--archive-restore", "pkg", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
	err := root.Execute()
	if err == nil {
		t.Fatal("want unknown flag")
	}
	text := err.Error() + errBuf.String()
	if !strings.Contains(text, "unknown flag") {
		t.Fatalf("error = %q, want Cobra unknown-flag", text)
	}
	if strings.Contains(text, "0.48.2") || strings.Contains(strings.ToLower(text), "archive package") {
		t.Fatalf("bespoke archive-restore message leaked: %q", text)
	}
}

func TestRootCLI_BundleAndBackupHelpStillWork(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := cli.NewRootCLI().Command()
	export := mustFindCommand(t, root, "bundle", "export")
	if export.Flags().Lookup("passphrase-env") == nil {
		t.Fatal("bundle export --passphrase-env must remain")
	}
	imp := mustFindCommand(t, root, "bundle", "import")
	if imp.Flags().Lookup("passphrase-env") == nil {
		t.Fatal("bundle import --passphrase-env must remain")
	}
	backup := mustFindCommand(t, root, "store", "backup")
	if backup == nil {
		t.Fatal("store backup missing")
	}
}

func mustFindCommand(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	cmd := root
	for _, name := range path {
		next := commandNamed(cmd, name)
		if next == nil {
			t.Fatalf("command %s missing under %s", name, cmd.Name())
		}
		cmd = next
	}
	return cmd
}

func commandNamed(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	return nil
}
