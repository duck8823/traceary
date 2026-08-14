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
	if findCommandOrNil(store, "search-projection") == nil {
		t.Fatal("store search-projection must remain")
	}
	retention := findCommandOrNil(store, "retention")
	if retention == nil {
		t.Fatal("store retention (files) must remain")
	}
	if findCommandOrNil(retention, "plan") != nil || findCommandOrNil(retention, "apply") != nil || findCommandOrNil(retention, "restore") != nil {
		t.Fatal("raw-body store retention plan/apply/restore must be removed")
	}
	if findCommandOrNil(retention, "files") == nil {
		t.Fatal("store retention files must remain")
	}
}

func TestStoreCompactHelpNamesForce(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	compact := findCommandOrNil(findCommandOrNil(root, "store"), "compact")
	if compact == nil {
		t.Fatal("store compact is not registered")
	}
	if compact.Flags().Lookup("force") == nil {
		t.Fatal("store compact must accept --force")
	}
	if compact.Flags().Lookup("keep-days") == nil {
		t.Fatal("store compact must accept --keep-days")
	}
	if !strings.Contains(compact.Short, "Rewrite") && !strings.Contains(compact.Short, "書き換え") {
		t.Fatalf("compact short = %q, want it to describe the rewrite", compact.Short)
	}
}
