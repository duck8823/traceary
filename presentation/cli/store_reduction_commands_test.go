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
	if compact.Flags().Lookup("projection-rebuild") == nil || compact.Flags().Lookup("projection-abort") == nil {
		t.Fatal("store compact must absorb --projection-rebuild and --projection-abort")
	}
}

func TestStoreCompactHelpNamesRefuseUnrefined(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	compact := findCommandOrNil(findCommandOrNil(root, "store"), "compact")
	if compact == nil {
		t.Fatal("store compact is not registered")
	}
	if compact.Flags().Lookup("force") != nil {
		t.Fatal("store compact must not accept --force")
	}
	if compact.Flags().Lookup("refuse-unrefined") == nil {
		t.Fatal("store compact must accept --refuse-unrefined")
	}
	if compact.Flags().Lookup("keep-days") == nil {
		t.Fatal("store compact must accept --keep-days")
	}
	if !strings.Contains(compact.Short, "Rewrite") && !strings.Contains(compact.Short, "書き換え") {
		t.Fatalf("compact short = %q, want it to describe the rewrite", compact.Short)
	}
}

func TestSearchProjectionIndexFamilyBytesHelpNamesRebuildPeak(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	compact := findCommandOrNil(findCommandOrNil(root, "store"), "compact")
	if compact == nil {
		t.Fatal("store compact is not registered")
	}
	flag := compact.Flags().Lookup("index-family-bytes")
	if flag == nil {
		t.Fatal("--index-family-bytes is missing")
	}
	if !strings.Contains(flag.Usage, "rebuild-peak") {
		t.Fatalf("usage=%q, want it to say the budget is not a rebuild-peak cap", flag.Usage)
	}
	if compact.Flags().Lookup("projection-rebuild") == nil {
		t.Fatal("store compact must accept --projection-rebuild")
	}
}
