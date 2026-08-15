package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func addReleaseGateCommands(release *cobra.Command) {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "evaluate-gates",
		Short: "Evaluate remaining #1620 release gates on a fixture store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEvaluateGates(cmd.Context(), cmd.OutOrStdout(), dbPath)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "fixture or operator-copy store (never the default live path)")
	if err := cmd.MarkFlagRequired("db"); err != nil {
		panic(err) // programming error: the flag was just registered
	}
	release.AddCommand(cmd)
}

func runEvaluateGates(ctx context.Context, out io.Writer, dbPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	resolved, err := filepath.Abs(dbPath)
	if err != nil {
		return xerrors.Errorf("failed to resolve --db: %w", err)
	}
	if err := application.RefuseLiveStore(resolved); err != nil {
		return xerrors.Errorf("failed to evaluate release gates: %w", err)
	}
	if _, err := os.Stat(resolved); err != nil {
		return xerrors.Errorf("failed to open --db: %w", err)
	}
	report, err := infra.NewReleaseGateEvaluator(infra.NewDatabase(resolved, nil)).
		Evaluate(ctx, time.Now().UTC())
	if err != nil {
		return xerrors.Errorf("failed to evaluate release gates: %w", err)
	}
	enc := json.NewEncoder(out)
	if err := enc.Encode(report); err != nil {
		return xerrors.Errorf("failed to encode release-gate report: %w", err)
	}
	if !report.Passed {
		return xerrors.Errorf("release gates missed")
	}
	return nil
}
