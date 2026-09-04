package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	for cycle := 0; cycle < 2; cycle++ {
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
				t.Fatalf("benchmark cycle %d (%s) has no query-plan evidence", cycle, benchmarkCase.Name)
			}
		}
	}
	before := snapshotStoreFiles(t, path)
	handoff, err := benchmarkHandoff(context.Background(), path, 2, &handoffCardinality{Commands: 10, Memories: 10})
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

func TestCopiedStoreHandoffAllowsZeroOptionalWorkload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copied.db")
	if _, err := createSynthetic(context.Background(), path, 1, 1); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM command_audits; DELETE FROM events WHERE kind='command_executed'; DELETE FROM memories`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := benchmarkHandoff(context.Background(), path, 1, nil)
	if err != nil {
		t.Fatalf("copied-store zero workload error = %v", err)
	}
	if result.Name != "handoff" {
		t.Fatalf("result = %+v", result)
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

func TestSyntheticFixtureWritesPlaintextPayloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	if _, err := createSynthetic(context.Background(), path, 2, 1); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	assertPayloadRows := func(query string) {
		t.Helper()
		rows, queryErr := db.Query(query)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer func() { _ = rows.Close() }()
		count := 0
		for rows.Next() {
			var payload, sqliteType string
			if scanErr := rows.Scan(&payload, &sqliteType); scanErr != nil {
				t.Fatal(scanErr)
			}
			if sqliteType != "text" {
				t.Fatalf("typeof = %q, want text (payload=%q)", sqliteType, payload)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatal("payload query returned no rows")
		}
	}
	assertPayloadRows(`SELECT CAST(body AS TEXT), typeof(body) FROM events`)
	for _, column := range []string{"command_text", "input_text", "output_text"} {
		assertPayloadRows(fmt.Sprintf(`SELECT CAST(%[1]s AS TEXT), typeof(%[1]s) FROM command_audits`, column))
	}
}

func TestOpenCompatibleReadOnlyRejectsUnsupportedStoreState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		column string
		value  int
		match  string
	}{
		{"future reader", "minimum_reader_version", 39, "requires reader version 39"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsupported.db")
			if _, err := createSynthetic(context.Background(), path, 1, 1); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(fmt.Sprintf(`UPDATE store_format_state SET %s=? WHERE singleton=1`, tc.column), tc.value); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			readDB, err := openCompatibleReadOnly(context.Background(), path)
			if readDB != nil {
				_ = readDB.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("openCompatibleReadOnly() error = %v, want substring %q", err, tc.match)
			}
		})
	}
}
