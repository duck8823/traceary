package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCorpusBodyIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, kind := range allCorpusKinds() {
		first := corpusBody(kind, calibrateSeed, 3, 64)
		second := corpusBody(kind, calibrateSeed, 3, 64)
		if first == "" || first != second {
			t.Fatalf("%s body not deterministic: %q vs %q", kind, first, second)
		}
		other := corpusBody(kind, calibrateSeed, 4, 64)
		if kind != corpusEnormous && kind != corpusRepetitive && kind != corpusCJK && first == other {
			t.Fatalf("%s bodies for distinct indexes collided", kind)
		}
	}
}

func TestCalibrateGatesEmitsRangeFromTinyCorpora(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "calibrate")
	report, err := runCalibrateGates(context.Background(), calibrateOpts{
		Dir:           dir,
		Rows:          8,
		EnormousRows:  1,
		EnormousBytes: 4096,
		Seed:          calibrateSeed,
	})
	if err != nil {
		t.Fatalf("runCalibrateGates() error = %v", err)
	}
	if report.SchemaVersion != "traceary.store-gate-calibrate/v1" {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if len(report.Corpora) != len(allCorpusKinds()) {
		t.Fatalf("corpora = %d, want %d", len(report.Corpora), len(allCorpusKinds()))
	}
	if report.Range.MeasuredCorpora != len(allCorpusKinds()) {
		t.Fatalf("measured = %d", report.Range.MeasuredCorpora)
	}
	if report.Range.WholeStoreAmplificationMin <= 0 || report.Range.WholeStoreAmplificationMax < report.Range.WholeStoreAmplificationMin {
		t.Fatalf("range = %+v", report.Range)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "calibrate.json"))
	if err != nil {
		t.Fatalf("read calibrate.json: %v", err)
	}
	var onDisk calibrateReport
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("calibrate.json: %v", err)
	}
	if onDisk.Range.MeasuredCorpora != report.Range.MeasuredCorpora {
		t.Fatalf("disk report mismatch")
	}
	for _, corpus := range report.Corpora {
		if corpus.OperatorCostStatus != "complete" || corpus.WholeStoreAmplification <= 0 {
			t.Fatalf("corpus %+v missing operator-cost measurement", corpus)
		}
		if corpus.SearchAmplificationStatus != "unmeasured" {
			t.Fatalf("search amplification status = %q, want unmeasured", corpus.SearchAmplificationStatus)
		}
	}
}
