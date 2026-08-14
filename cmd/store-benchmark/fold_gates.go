package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/duck8823/traceary/application"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func runFoldGates(ctx context.Context, dbPath string, thresholdBytes, wakeBudgetBytes int64) error {
	resolved, err := filepath.Abs(dbPath)
	if err != nil {
		return fmt.Errorf("resolve --db: %w", err)
	}
	live, err := defaultLiveStorePath()
	if err != nil {
		return err
	}
	if resolved == live {
		return fmt.Errorf("refusing the default live store %s; pass an operator copy via --db", live)
	}
	if _, err := os.Stat(resolved); err != nil {
		return fmt.Errorf("open --db: %w", err)
	}
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	database := infra.NewDatabase(resolved, migrations)
	if thresholdBytes <= 0 {
		thresholdBytes = application.DefaultFoldThresholdBytes
	}
	if wakeBudgetBytes <= 0 {
		wakeBudgetBytes = application.DefaultFoldWakeBudgetBytes
	}
	report, err := infra.NewFoldGateInspector(database).InspectFoldGate(ctx, thresholdBytes, wakeBudgetBytes)
	if err != nil {
		return fmt.Errorf("inspect fold-gate: %w", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return fmt.Errorf("encode fold-gate report: %w", err)
	}
	return nil
}

func defaultLiveStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(home, ".config", "traceary", "traceary.db"))
	if err != nil {
		return "", fmt.Errorf("resolve live store path: %w", err)
	}
	return path, nil
}
