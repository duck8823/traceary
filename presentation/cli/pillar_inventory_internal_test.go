package cli

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

func TestPillarInventoryMatchesShippedTree(t *testing.T) {
	root := NewRootCLI().Command()
	got := collectVisibleOperatorActions(root, "", nil)
	sort.Strings(got)

	want := make([]string, 0, len(pillarInventory))
	seen := map[string]struct{}{}
	for _, entry := range pillarInventory {
		if entry.Path == "" {
			t.Fatal("inventory entry has empty Path")
		}
		if _, dup := seen[entry.Path]; dup {
			t.Fatalf("duplicate inventory path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		want = append(want, entry.Path)
		if entry.Reason == "" {
			t.Errorf("%s: empty Reason", entry.Path)
		}
		switch entry.Pillar {
		case pillarRecord, pillarMemory, pillarKeep:
		default:
			t.Errorf("%s: unknown pillar %q", entry.Path, entry.Pillar)
		}
		if entry.RemovalTarget == "" && entry.Replacement != "" {
			t.Errorf("%s: Replacement without RemovalTarget", entry.Path)
		}
		if lookupCommandPath(root, entry.Path) == nil {
			t.Errorf("%s: not found on shipped tree", entry.Path)
		}
	}
	sort.Strings(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("visible operator actions vs inventory (-want +got):\n%s", diff)
	}
}

func TestPillarInventoryOnlyRemembersHasNotice(t *testing.T) {
	var noticed []string
	for _, entry := range pillarInventory {
		if entry.RemovalTarget == "" {
			continue
		}
		noticed = append(noticed, entry.Path)
		if entry.Path != "memory store remember" {
			t.Errorf("unexpected notice on %s", entry.Path)
		}
		if entry.RemovalTarget != rememberRemovalTarget {
			t.Errorf("remember RemovalTarget = %q, want %q", entry.RemovalTarget, rememberRemovalTarget)
		}
		if entry.Replacement != "traceary memory store propose" {
			t.Errorf("remember Replacement = %q", entry.Replacement)
		}
	}
	if diff := cmp.Diff([]string{"memory store remember"}, noticed); diff != "" {
		t.Errorf("noticed paths mismatch (-want +got):\n%s", diff)
	}
}

func collectVisibleOperatorActions(cmd *cobra.Command, prefix string, out []string) []string {
	children := append([]*cobra.Command(nil), cmd.Commands()...)
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		if child.Hidden {
			continue
		}
		path := child.Name()
		if prefix != "" {
			path = prefix + " " + child.Name()
		}
		if isVisibleOperatorAction(child) {
			out = append(out, path)
		}
		if len(child.Commands()) > 0 {
			out = collectVisibleOperatorActions(child, path, out)
		}
	}
	return out
}

func isVisibleOperatorAction(cmd *cobra.Command) bool {
	if cmd.Hidden || !cmd.Runnable() {
		return false
	}
	if len(cmd.Commands()) == 0 {
		// A childless runnable is an operator leaf even when Args is unset.
		// Requiring Args would let a new RunE-only command skip the inventory.
		return true
	}
	// Parents that are themselves actions (report, store compact, completion)
	// set Args. applyStrictGroups gives other parents RunE with Args == nil.
	return cmd.Args != nil
}

func TestCollectVisibleOperatorActionsIncludesChildlessRunnableWithoutArgs(t *testing.T) {
	root := &cobra.Command{Use: "traceary"}
	foo := &cobra.Command{
		Use: "foo",
		RunE: func(*cobra.Command, []string) error {
			return nil
		},
	}
	bar := &cobra.Command{Use: "bar"}
	bar.AddCommand(&cobra.Command{
		Use: "baz",
		RunE: func(*cobra.Command, []string) error {
			return nil
		},
		Args: cobra.NoArgs,
	})
	root.AddCommand(foo, bar)
	applyStrictGroups(root)

	got := collectVisibleOperatorActions(root, "", nil)
	sort.Strings(got)
	if diff := cmp.Diff([]string{"bar baz", "foo"}, got); diff != "" {
		t.Errorf("visible actions mismatch (-want +got):\n%s", diff)
	}
}

func TestLookupCommandPathRejectsMissing(t *testing.T) {
	root := NewRootCLI().Command()
	if got := lookupCommandPath(root, ""); got != nil {
		t.Fatalf("empty path = %s, want nil", got.Name())
	}
	if got := lookupCommandPath(root, "memory store missing"); got != nil {
		t.Fatalf("missing path = %s, want nil", got.Name())
	}
	if got := lookupCommandPath(root, "memory store remember"); got == nil || got.Name() != "remember" {
		t.Fatalf("remember lookup = %v", got)
	}
}
