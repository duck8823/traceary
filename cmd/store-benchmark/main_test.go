package main

import (
	"context"
	"path/filepath"
	"testing"
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
	for _, benchmarkCase := range cases {
		result, err := benchmark(context.Background(), path, 2, benchmarkCase.name, benchmarkCase.query, benchmarkCase.args)
		if err != nil {
			t.Fatalf("benchmark(%s) error = %v", benchmarkCase.name, err)
		}
		if len(result.QueryPlan) == 0 {
			t.Fatalf("benchmark(%s) has no query-plan evidence", benchmarkCase.name)
		}
	}
}
