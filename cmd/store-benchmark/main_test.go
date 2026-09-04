package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

func TestSyntheticFixtureWritesCompleteIdentityPayloadMetadata(t *testing.T) {
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
			var payload, codec, digest string
			var version, plaintextBytes, encodedBytes int
			if scanErr := rows.Scan(&payload, &codec, &version, &plaintextBytes, &encodedBytes, &digest); scanErr != nil {
				t.Fatal(scanErr)
			}
			sum := sha256.Sum256([]byte(payload))
			if codec != "identity" || version != 1 || plaintextBytes != len([]byte(payload)) || encodedBytes != len([]byte(payload)) || digest != hex.EncodeToString(sum[:]) {
				t.Fatalf("invalid identity metadata: codec=%q version=%d plaintext=%d encoded=%d digest=%q payload=%q", codec, version, plaintextBytes, encodedBytes, digest, payload)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatal("metadata query returned no rows")
		}
	}
	assertPayloadRows(`SELECT body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256 FROM events`)
	for _, prefix := range []string{"command", "input", "output"} {
		assertPayloadRows(fmt.Sprintf(`SELECT %[1]s_text, %[1]s_codec, %[1]s_format_version, %[1]s_plaintext_bytes, %[1]s_encoded_bytes, %[1]s_sha256 FROM command_audits`, prefix))
	}
}

func TestOpenCompatibleReadOnlyRejectsUnsupportedStoreState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		column string
		value  int
		match  string
	}{
		{"future reader", "minimum_reader_version", 38, "requires reader version 38"},
		{"future payload format", "maximum_payload_format", 2, "payload format 2 is unsupported"},
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
