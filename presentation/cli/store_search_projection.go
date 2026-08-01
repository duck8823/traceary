package cli

import (
	"encoding/json"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

//nolint:wrapcheck // Cobra boundary intentionally preserves typed usecase errors.
func (c *RootCLI) newStoreSearchProjectionCommand() *cobra.Command {
	group := &cobra.Command{Use: "search-projection", Short: "Manage the non-authoritative derived search projection"}
	group.AddCommand(c.newStoreSearchProjectionRunCommand("start", true), c.newStoreSearchProjectionRunCommand("resume", false))
	group.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.searchProjection == nil {
			return xerrors.New("search projection usecase is not configured")
		}
		status, err := c.searchProjection.Inspect(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
	}})
	group.AddCommand(&cobra.Command{Use: "probe", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.searchProjection == nil {
			return xerrors.New("search projection usecase is not configured")
		}
		started := time.Now()
		status, err := c.searchProjection.Inspect(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			SchemaVersion          string `json:"schema_version"`
			FTSDesign              string `json:"fts_design"`
			InspectionMilliseconds int64  `json:"inspection_milliseconds"`
			Authoritative          bool   `json:"authoritative"`
		}{"traceary.search-projection-probe/v1", status.FTSDesign, time.Since(started).Milliseconds(), false})
	}})
	return group
}

//nolint:wrapcheck // Cobra boundary intentionally preserves typed usecase errors.
func (c *RootCLI) newStoreSearchProjectionRunCommand(name string, start bool) *cobra.Command {
	var rows int
	var wall, lock, timeAge time.Duration
	var stored, decoded, written, recent int64
	cmd := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.searchProjection == nil {
			return xerrors.New("search projection usecase is not configured")
		}
		b := apptypes.SearchProjectionBudget{Rows: rows, WallTime: wall, LockTime: lock, StoredBytes: stored, DecodedBytes: decoded, WriteBytes: written, RecentAge: timeAge, RecentBytes: recent}
		enc := json.NewEncoder(cmd.OutOrStdout())
		if start {
			got, err := c.searchProjection.StartGeneration(cmd.Context(), b, time.Now())
			if err != nil {
				return err
			}
			return enc.Encode(got)
		}
		got, err := c.searchProjection.Resume(cmd.Context(), b, time.Now())
		if err != nil {
			return err
		}
		return enc.Encode(got)
	}}
	cmd.Flags().IntVar(&rows, "rows", 128, "maximum source rows")
	cmd.Flags().DurationVar(&wall, "wall-time", time.Second, "maximum total batch duration")
	cmd.Flags().DurationVar(&lock, "lock-time", 250*time.Millisecond, "maximum write-lock duration")
	cmd.Flags().Int64Var(&stored, "stored-bytes", 8<<20, "maximum stored source bytes")
	cmd.Flags().Int64Var(&decoded, "decoded-bytes", 8<<20, "maximum decoded source bytes")
	cmd.Flags().Int64Var(&written, "write-bytes", 8<<20, "maximum logical write bytes")
	cmd.Flags().DurationVar(&timeAge, "recent-age", 30*24*time.Hour, "recent projection age")
	cmd.Flags().Int64Var(&recent, "recent-bytes", 64<<20, "recent projection byte ceiling")
	return cmd
}
