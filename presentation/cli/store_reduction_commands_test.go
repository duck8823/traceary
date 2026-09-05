package cli

import (
	"strings"
	"testing"
)

func TestStoreReductionCommandsAreRemoved(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	store := findCommandOrNil(root, "store")
	if store == nil {
		t.Fatal("store command is not registered")
	}
	for _, name := range []string{"gc", "dedupe", "search-retire", "payload-rehearsal", "payload-backfill"} {
		if got := findCommandOrNil(store, name); got != nil {
			t.Fatalf("store %s must be removed by #1872", name)
		}
	}
	compact := findCommandOrNil(store, "compact")
	if compact == nil {
		t.Fatal("store compact must remain")
	}
	for _, name := range []string{"plan", "apply", "resume", "status"} {
		if got := findCommandOrNil(compact, name); got != nil {
			t.Fatalf("store compact %s must be removed by #1872", name)
		}
	}
	if findCommandOrNil(compact, "rollback") == nil {
		t.Fatal("store compact rollback must remain")
	}
	if findCommandOrNil(store, "search-projection") != nil {
		t.Fatal("store search-projection must be removed by #2077")
	}
	if findCommandOrNil(store, "retention") != nil {
		t.Fatal("store retention must be removed by #2074")
	}
	if findCommandOrNil(store, "archive") != nil {
		t.Fatal("store archive must be removed by #2074")
	}
	if compact.Flags().Lookup("archive") == nil || compact.Flags().Lookup("retention-plan") == nil {
		t.Fatal("store compact must absorb --archive and --retention-plan")
	}
	for _, name := range []string{
		"projection-rebuild",
		"projection-abort",
		"index-family-bytes",
		"decoded-bytes",
		"recent-age",
		"lock-time",
		"rows",
		"wall-time",
		"stored-bytes",
		"write-bytes",
	} {
		if compact.Flags().Lookup(name) != nil {
			t.Fatalf("store compact must not accept --%s", name)
		}
	}
}

func TestStoreCompactHelpRejectsRefuseUnrefined(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	compact := findCommandOrNil(findCommandOrNil(root, "store"), "compact")
	if compact == nil {
		t.Fatal("store compact is not registered")
	}
	if compact.Flags().Lookup("force") != nil {
		t.Fatal("store compact must not accept --force")
	}
	if compact.Flags().Lookup("refuse-unrefined") != nil {
		t.Fatal("store compact must not accept --refuse-unrefined")
	}
	if compact.Flags().Lookup("keep-days") == nil {
		t.Fatal("store compact must accept --keep-days")
	}
	if !strings.Contains(compact.Short, "Rewrite") && !strings.Contains(compact.Short, "書き換え") {
		t.Fatalf("compact short = %q, want it to describe the rewrite", compact.Short)
	}
}

func TestStoreCompactRemovedProjectionFlagsAreUnknown(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	compact := findCommandOrNil(findCommandOrNil(root, "store"), "compact")
	if compact == nil {
		t.Fatal("store compact is not registered")
	}
	for _, name := range []string{
		"index-family-bytes",
		"projection-rebuild",
		"projection-abort",
		"decoded-bytes",
		"recent-age",
		"lock-time",
	} {
		if compact.Flags().Lookup(name) != nil {
			t.Fatalf("store compact still accepts --%s", name)
		}
	}
}
