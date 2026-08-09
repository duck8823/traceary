package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestStoreSurfaceReplacesSearchMaintenanceWithSearchRetire pins the surface
// reduction #1718 makes: six `store search-maintenance` subcommands driving a
// resumable transition become one idempotent `store search-retire`. The
// removed group must not linger as an alias — a store that still accepted
// `start-retire` would suggest the transition still exists.
func TestStoreSurfaceReplacesSearchMaintenanceWithSearchRetire(t *testing.T) {
	store := findCommand(t, NewRootCLI().Command(), "store")
	if findCommandOrNil(store, "search-maintenance") != nil {
		t.Fatal("`store search-maintenance` still exists; #1718 removes the group")
	}
	retire := findCommandOrNil(store, "search-retire")
	if retire == nil {
		t.Fatal("`store search-retire` is not registered")
	}
	if retire.Flags().Lookup("db-path") == nil {
		t.Fatal("`store search-retire` must accept --db-path like its sibling store commands")
	}
}

func findCommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	found := findCommandOrNil(parent, name)
	if found == nil {
		t.Fatalf("command %q not found under %q", name, parent.Name())
	}
	return found
}

func findCommandOrNil(parent *cobra.Command, name string) *cobra.Command {
	for _, candidate := range parent.Commands() {
		if candidate.Name() == name {
			return candidate
		}
	}
	return nil
}
