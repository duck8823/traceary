package sqlite

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestSearchProjectionDefaultFlagsMatchCatchUpBudget(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"start", "resume"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := cli.NewRootCLI().Command()
			command, _, err := root.Find([]string{"store", "search-projection", name})
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}

			got := apptypes.SearchProjectionBudget{
				Rows:             mustGetInt(t, command, "rows"),
				WallTime:         mustGetDuration(t, command, "wall-time"),
				LockTime:         mustGetDuration(t, command, "lock-time"),
				StoredBytes:      mustGetInt64(t, command, "stored-bytes"),
				DecodedBytes:     mustGetInt64(t, command, "decoded-bytes"),
				WriteBytes:       mustGetInt64(t, command, "write-bytes"),
				RecentAge:        mustGetDuration(t, command, "recent-age"),
				IndexFamilyBytes: mustGetInt64(t, command, "index-family-bytes"),
			}
			want := defaultSearchProjectionCatchUpBudget()

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("default CLI budget mismatch (-want +got):\n%s", diff)
			}
			if got.ConfigHash() != want.ConfigHash() {
				t.Errorf("default CLI config hash = %q, catch-up config hash = %q", got.ConfigHash(), want.ConfigHash())
			}
		})
	}
}

func mustGetInt(t *testing.T, command *cobra.Command, name string) int {
	t.Helper()
	value, err := command.Flags().GetInt(name)
	if err != nil {
		t.Fatalf("GetInt(%q) error = %v", name, err)
	}
	return value
}

func mustGetInt64(t *testing.T, command *cobra.Command, name string) int64 {
	t.Helper()
	value, err := command.Flags().GetInt64(name)
	if err != nil {
		t.Fatalf("GetInt64(%q) error = %v", name, err)
	}
	return value
}

func mustGetDuration(t *testing.T, command *cobra.Command, name string) time.Duration {
	t.Helper()
	value, err := command.Flags().GetDuration(name)
	if err != nil {
		t.Fatalf("GetDuration(%q) error = %v", name, err)
	}
	return value
}
