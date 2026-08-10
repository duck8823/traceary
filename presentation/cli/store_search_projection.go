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
	group := &cobra.Command{Use: "search-projection", Short: "Manage the derived search projection that serves search reads"}
	group.AddCommand(c.newStoreSearchProjectionRunCommand("start", true), c.newStoreSearchProjectionRunCommand("resume", false))
	group.AddCommand(&cobra.Command{Use: "abort", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.searchProjection == nil {
			return xerrors.New("search projection usecase is not configured")
		}
		got, err := c.searchProjection.Abandon(cmd.Context(), time.Now())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	}})
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
			MatchProbeMilliseconds int64  `json:"match_probe_milliseconds"`
			Authoritative          bool   `json:"authoritative"`
		}{"traceary.search-projection-probe/v1", status.FTSDesign, time.Since(started).Milliseconds(), status.MatchProbeMilliseconds, false})
	}})
	return group
}

//nolint:wrapcheck // Cobra boundary intentionally preserves typed usecase errors.
func (c *RootCLI) newStoreSearchProjectionRunCommand(name string, start bool) *cobra.Command {
	var rows int
	var wall, lock, timeAge time.Duration
	var stored, decoded, written, recent int64
	var untilComplete bool
	var maxBatches int
	var totalWall time.Duration
	cmd := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.searchProjection == nil {
			return xerrors.New("search projection usecase is not configured")
		}
		b := apptypes.SearchProjectionBudget{Rows: rows, WallTime: wall, LockTime: lock, StoredBytes: stored, DecodedBytes: decoded, WriteBytes: written, RecentAge: timeAge, IndexFamilyBytes: recent}
		enc := json.NewEncoder(cmd.OutOrStdout())
		if start {
			got, err := c.searchProjection.StartGeneration(cmd.Context(), b, time.Now())
			if err != nil {
				return err
			}
			return enc.Encode(got)
		}
		if untilComplete {
			got, err := c.searchProjection.ResumeUntil(cmd.Context(), b, apptypes.SearchProjectionRunOptions{MaxBatches: maxBatches, TotalWallTime: totalWall}, time.Now())
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
	cmd.Flags().DurationVar(&lock, "lock-time", apptypes.DefaultSearchProjectionLockTime, "maximum write-lock duration")
	cmd.Flags().Int64Var(&stored, "stored-bytes", 8<<20, "maximum stored source bytes")
	cmd.Flags().Int64Var(&decoded, "decoded-bytes", 8<<20, "maximum decoded source bytes")
	cmd.Flags().Int64Var(&written, "write-bytes", 8<<20, "maximum logical write bytes")
	cmd.Flags().DurationVar(&timeAge, "recent-age", 30*24*time.Hour, "recent projection age")
	// Index-family budget: physical bytes of documents, trigram index, session
	// tier and literal fingerprints (active b-tree allocation), not source text.
	// 1464 MiB is what the 4 GiB store gate leaves; trigram ~2.16x yields a
	// variable recent window (~1.5–2 weeks at the median rate).
	cmd.Flags().Int64Var(&recent, "index-family-bytes", apptypes.DefaultSearchProjectionIndexFamilyBytes, "physical byte ceiling for the bounded search index family (not source text)")
	if !start {
		cmd.Flags().BoolVar(&untilComplete, "until-complete", false, "resume bounded batches until complete or a command bound is reached")
		cmd.Flags().IntVar(&maxBatches, "max-batches", 100, "maximum durable batches in one command")
		cmd.Flags().DurationVar(&totalWall, "total-wall-time", 10*time.Minute, "maximum total multi-batch command duration")
	}
	return cmd
}
