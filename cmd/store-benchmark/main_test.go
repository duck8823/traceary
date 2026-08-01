package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
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
	if fixture.SmallRows != 1200 || fixture.LargeRows != 2 || fixture.WALBytes == 0 || fixture.FreePages == 0 || fixture.ActiveSessions != 1 || fixture.CommandRows != 10 || fixture.AcceptedMemories != 10 {
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
		if benchmarkCase.Name == "active" || benchmarkCase.Name == "latest" {
			matched, err := queryHasRows(context.Background(), path, benchmarkCase.SQL, benchmarkCase.Args)
			if err != nil || !matched {
				t.Fatalf("%s preflight matched=%v error=%v", benchmarkCase.Name, matched, err)
			}
		}
		result, err := benchmark(context.Background(), path, 2, benchmarkCase.Name, benchmarkCase.SQL, benchmarkCase.Args)
		if err != nil {
			t.Fatalf("benchmark(%s) error = %v", benchmarkCase.Name, err)
		}
		if len(result.QueryPlan) == 0 {
			t.Fatalf("benchmark(%s) has no query-plan evidence", benchmarkCase.Name)
		}
	}
	before := snapshotStoreFiles(t, path)
	handoff, err := benchmarkHandoff(context.Background(), path, 2)
	if err != nil {
		t.Fatalf("benchmarkHandoff() error = %v", err)
	}
	if len(handoff.QueryPlan) < 2 {
		t.Fatalf("handoff query plans = %v", handoff.QueryPlan)
	}
	after := snapshotStoreFiles(t, path)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("handoff mutated source files: before=%v after=%v", before, after)
	}
}

func snapshotStoreFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		name := path + suffix
		data, err := os.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		result[suffix] = fmt.Sprintf("%x:%d:%d", sha256.Sum256(data), info.Size(), info.ModTime().UnixNano())
	}
	return result
}

func TestSyntheticFixtureKeepsRequestedRowsWhileCreatingFreePages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.db")
	fixture, err := createSynthetic(context.Background(), path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.SmallRows != 1 || fixture.FreePages == 0 {
		t.Fatalf("fixture = %+v", fixture)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM events WHERE id LIKE 'synthetic-keep-%' AND length(body)<1024`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("retained small rows = %d, want 1", count)
	}
}
