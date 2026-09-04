package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

type calibrateReport struct {
	SchemaVersion string            `json:"schema_version"`
	Seed          int64             `json:"seed"`
	Corpora       []calibrateCorpus `json:"corpora"`
	Range         calibrateRange    `json:"range"`
}

type calibrateCorpus struct {
	Kind                      string  `json:"kind"`
	Rows                      int     `json:"rows"`
	DatabaseBytes             int64   `json:"database_bytes"`
	ResidentBytes             int64   `json:"resident_bytes"`
	RetainedSourceBytes       int64   `json:"retained_source_bytes"`
	WholeStoreAmplification   float64 `json:"whole_store_amplification,omitempty"`
	SearchAmplificationStatus string  `json:"search_amplification_status"`
	SearchAmplificationReason string  `json:"search_amplification_reason,omitempty"`
	SearchAmplificationPPM    int64   `json:"search_amplification_ppm,omitempty"`
	CapacityStatus            string  `json:"capacity_status"`
	OperatorCostStatus        string  `json:"operator_cost_status"`
}

type calibrateRange struct {
	WholeStoreAmplificationMin float64 `json:"whole_store_amplification_min,omitempty"`
	WholeStoreAmplificationMax float64 `json:"whole_store_amplification_max,omitempty"`
	MeasuredCorpora            int     `json:"measured_corpora"`
}

type calibrateOpts struct {
	Dir           string
	Rows          int
	EnormousRows  int
	EnormousBytes int
	Seed          int64
}

func runCalibrateGates(ctx context.Context, opts calibrateOpts) (calibrateReport, error) {
	if opts.Dir == "" {
		return calibrateReport{}, fmt.Errorf("calibrate-gates directory is required")
	}
	if opts.Rows < 1 || opts.EnormousRows < 1 || opts.EnormousBytes < 1 {
		return calibrateReport{}, fmt.Errorf("calibrate row counts must be positive")
	}
	if opts.Seed == 0 {
		opts.Seed = calibrateSeed
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return calibrateReport{}, fmt.Errorf("create calibrate directory: %w", err)
	}
	report := calibrateReport{SchemaVersion: "traceary.store-gate-calibrate/v1", Seed: opts.Seed}
	var minAmp, maxAmp float64
	var haveAmp bool
	for _, kind := range allCorpusKinds() {
		rows := opts.Rows
		if kind == corpusEnormous {
			rows = opts.EnormousRows
		}
		path := filepath.Join(opts.Dir, string(kind)+".db")
		if err := createCalibrateCorpus(ctx, path, kind, opts.Seed, rows, opts.EnormousBytes); err != nil {
			return calibrateReport{}, fmt.Errorf("%s: %w", kind, err)
		}
		row, err := measureCalibrateCorpus(ctx, path, kind, rows)
		if err != nil {
			return calibrateReport{}, fmt.Errorf("%s measure: %w", kind, err)
		}
		report.Corpora = append(report.Corpora, row)
		if row.WholeStoreAmplification > 0 {
			if !haveAmp || row.WholeStoreAmplification < minAmp {
				minAmp = row.WholeStoreAmplification
			}
			if !haveAmp || row.WholeStoreAmplification > maxAmp {
				maxAmp = row.WholeStoreAmplification
			}
			haveAmp = true
			report.Range.MeasuredCorpora++
		}
	}
	if haveAmp {
		report.Range.WholeStoreAmplificationMin = minAmp
		report.Range.WholeStoreAmplificationMax = maxAmp
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, fmt.Errorf("encode calibrate report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(opts.Dir, "calibrate.json"), append(out, '\n'), 0o600); err != nil {
		return report, fmt.Errorf("write calibrate.json: %w", err)
	}
	return report, nil
}

func createCalibrateCorpus(ctx context.Context, path string, kind corpusKind, seed int64, rows, enormousBytes int) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("destination already exists")
	}
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	database := infra.NewDatabase(path, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		return fmt.Errorf("initialize calibrate store: %w", err)
	}
	events := infra.NewEventDatasource(database)
	for index := 0; index < rows; index++ {
		body := corpusBody(kind, seed, index, enormousBytes)
		createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Millisecond)
		event := model.EventOf(
			types.EventID(fmt.Sprintf("%s-%08d", kind, index)),
			types.EventKindPrompt,
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("calibrate"),
			types.Workspace("calibrate"),
			body,
			createdAt,
		)
		if err := events.Save(ctx, event); err != nil {
			return fmt.Errorf("save %s row %d: %w", kind, index, err)
		}
	}
	return nil
}

func measureCalibrateCorpus(ctx context.Context, path string, kind corpusKind, rows int) (calibrateCorpus, error) {
	row := calibrateCorpus{
		Kind:                      string(kind),
		Rows:                      rows,
		SearchAmplificationStatus: "unmeasured",
		SearchAmplificationReason: "recent index family is no longer stored",
	}
	if info, err := os.Stat(path); err == nil {
		row.ResidentBytes = info.Size()
	}
	database := infra.NewDatabase(path, nil)
	capacity, err := infra.NewCapacityInspector(database).InspectCapacity(ctx)
	if err != nil {
		return row, fmt.Errorf("inspect capacity: %w", err)
	}
	row.DatabaseBytes = capacity.DatabaseBytes
	row.CapacityStatus = capacity.Evidence.Status
	cost, err := infra.NewOperatorCostInspector(database).InspectOperatorCost(ctx, time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC), row.ResidentBytes)
	if err != nil {
		return row, fmt.Errorf("inspect operator cost: %w", err)
	}
	row.RetainedSourceBytes = cost.RetainedSourceBytes
	row.WholeStoreAmplification = cost.Amplification
	row.OperatorCostStatus = cost.Evidence.Status
	return row, nil
}
