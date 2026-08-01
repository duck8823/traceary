package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestSyntheticFixtureAndBenchmarkEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.db")
	fixture, err := createSynthetic(context.Background(), path, 1200, 2)
	if err != nil {
		t.Fatalf("createSynthetic() error = %v", err)
	}
	if fixture.SmallRows != 1200 || fixture.LargeRows != 2 || fixture.WALBytes == 0 || fixture.FreePages == 0 {
		t.Fatalf("fixture does not cover required storage shapes: %+v", fixture)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	benchmarkCases, err := infra.CapacityBenchmarkQueries(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, benchmarkCase := range benchmarkCases {
		result, err := benchmark(context.Background(), path, 2, benchmarkCase.Name, benchmarkCase.SQL, benchmarkCase.Args)
		if err != nil {
			t.Fatalf("benchmark(%s) error = %v", benchmarkCase.Name, err)
		}
		if len(result.QueryPlan) == 0 {
			t.Fatalf("benchmark(%s) has no query-plan evidence", benchmarkCase.Name)
		}
	}
}

func TestSyntheticFixtureRejectsRowsThatCannotCreateFreePages(t *testing.T) {
	_, err := createSynthetic(context.Background(), filepath.Join(t.TempDir(), "small.db"), 1, 1)
	if err == nil {
		t.Fatal("createSynthetic() error = nil, want minimum validation")
	}
}
