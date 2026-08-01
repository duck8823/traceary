// Command store-benchmark produces reproducible, metadata-only SQLite query evidence.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type report struct {
	SchemaVersion string       `json:"schema_version"`
	Fixture       fixtureInfo  `json:"fixture"`
	Iterations    int          `json:"iterations"`
	Cases         []caseResult `json:"cases"`
}
type fixtureInfo struct {
	Kind      string `json:"kind"`
	SmallRows int    `json:"small_rows,omitempty"`
	LargeRows int    `json:"large_rows,omitempty"`
	WALBytes  int64  `json:"wal_bytes,omitempty"`
	FreePages int64  `json:"free_pages,omitempty"`
}
type caseResult struct {
	Name      string   `json:"name"`
	ColdP50US int64    `json:"cold_p50_us"`
	ColdP95US int64    `json:"cold_p95_us"`
	WarmP50US int64    `json:"warm_p50_us"`
	WarmP95US int64    `json:"warm_p95_us"`
	QueryPlan []string `json:"query_plan"`
}

var cases = []struct {
	name, query string
	args        []any
}{
	{"active", `SELECT session_id FROM sessions WHERE ended_at IS NULL ORDER BY started_at DESC LIMIT 20`, nil},
	{"latest", `SELECT id FROM events ORDER BY created_at DESC, id DESC LIMIT 20`, nil},
	{"handoff", `SELECT e.id FROM events e JOIN sessions s ON s.session_id=e.session_id WHERE s.ended_at IS NULL ORDER BY e.created_at DESC, e.id DESC LIMIT 50`, nil},
	{"search", `SELECT id FROM events WHERE body LIKE ? ORDER BY created_at DESC, id DESC LIMIT 20`, []any{"%synthetic-needle%"}},
}

func main() {
	var dbPath, synthetic string
	var iterations, smallRows, largeRows int
	flag.StringVar(&dbPath, "db", "", "path to an operator-created store copy (opened immutable/read-only)")
	flag.StringVar(&synthetic, "synthetic", "", "create and benchmark a synthetic store at this new path")
	flag.IntVar(&iterations, "iterations", 15, "samples per cold and warm series")
	flag.IntVar(&smallRows, "small-rows", 10000, "synthetic small event rows")
	flag.IntVar(&largeRows, "large-rows", 8, "synthetic 1 MiB event rows")
	flag.Parse()
	if (dbPath == "") == (synthetic == "") || iterations < 1 || smallRows < 1 || largeRows < 1 {
		fatal("specify exactly one of --db or --synthetic and positive counts")
	}
	ctx := context.Background()
	info := fixtureInfo{Kind: "copied_store"}
	if synthetic != "" {
		var err error
		info, err = createSynthetic(ctx, synthetic, smallRows, largeRows)
		if err != nil {
			fatal(err.Error())
		}
		dbPath = synthetic
	}
	results := make([]caseResult, 0, len(cases))
	for _, benchmarkCase := range cases {
		result, err := benchmark(ctx, dbPath, iterations, benchmarkCase.name, benchmarkCase.query, benchmarkCase.args)
		if err != nil {
			fatal(fmt.Sprintf("%s: %v", benchmarkCase.name, err))
		}
		results = append(results, result)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report{SchemaVersion: "traceary.store-benchmark/v1", Fixture: info, Iterations: iterations, Cases: results}); err != nil {
		fatal(err.Error())
	}
}

func readOnlyDSN(path string) string { return "file:" + url.PathEscape(path) + "?immutable=1&mode=ro" }

//nolint:wrapcheck // command boundary adds the benchmark case name before display.
func benchmark(ctx context.Context, path string, iterations int, name, query string, args []any) (caseResult, error) {
	cold, warm := make([]int64, 0, iterations), make([]int64, 0, iterations)
	var plan []string
	for index := 0; index < iterations; index++ {
		db, err := sql.Open("sqlite", readOnlyDSN(path))
		if err != nil {
			return caseResult{}, err
		}
		started := time.Now()
		if err = consume(ctx, db, query, args); err != nil {
			_ = db.Close()
			return caseResult{}, err
		}
		cold = append(cold, time.Since(started).Microseconds())
		started = time.Now()
		if err = consume(ctx, db, query, args); err != nil {
			_ = db.Close()
			return caseResult{}, err
		}
		warm = append(warm, time.Since(started).Microseconds())
		if index == 0 {
			plan, err = queryPlan(ctx, db, query, args)
			if err != nil {
				_ = db.Close()
				return caseResult{}, err
			}
		}
		if err = db.Close(); err != nil {
			return caseResult{}, err
		}
	}
	return caseResult{Name: name, ColdP50US: percentile(cold, .50), ColdP95US: percentile(cold, .95), WarmP50US: percentile(warm, .50), WarmP95US: percentile(warm, .95), QueryPlan: plan}, nil
}

//nolint:wrapcheck // fixed internal query; caller adds the benchmark case name.
func consume(ctx context.Context, db *sql.DB, query string, args []any) (err error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		var ignored string
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
	}
	return rows.Err()
}

//nolint:wrapcheck // fixed internal query; caller adds the benchmark case name.
func queryPlan(ctx context.Context, db *sql.DB, query string, args []any) (_ []string, err error) {
	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	var result []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, rows.Err()
}
func percentile(values []int64, quantile float64) int64 {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[int(math.Ceil(float64(len(sorted))*quantile))-1]
}

//nolint:wrapcheck // errors terminate this standalone operator tool with a fixed destination.
func createSynthetic(ctx context.Context, path string, smallRows, largeRows int) (fixtureInfo, error) {
	if _, err := os.Stat(path); err == nil {
		return fixtureInfo{}, fmt.Errorf("synthetic destination already exists")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fixtureInfo{}, err
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`, `CREATE TABLE sessions(session_id TEXT PRIMARY KEY,started_at TEXT NOT NULL,ended_at TEXT)`, `CREATE TABLE events(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,body TEXT NOT NULL,created_at TEXT NOT NULL)`, `CREATE INDEX idx_sessions_active ON sessions(ended_at,started_at DESC)`, `CREATE INDEX idx_events_latest ON events(created_at DESC,id DESC)`, `CREATE INDEX idx_events_session_latest ON events(session_id,created_at DESC,id DESC)`} {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			return fixtureInfo{}, err
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO sessions VALUES('synthetic-active','2026-01-01T00:00:00Z',NULL),('synthetic-ended','2025-01-01T00:00:00Z','2025-01-02T00:00:00Z')`); err != nil {
		return fixtureInfo{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fixtureInfo{}, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events VALUES(?,?,?,?)`)
	if err != nil {
		return fixtureInfo{}, err
	}
	for index := 0; index < smallRows+largeRows; index++ {
		body := "synthetic-small"
		if index == 0 {
			body = "synthetic-needle"
		}
		if index >= smallRows {
			body = strings.Repeat("L", 1<<20)
		}
		if _, err = stmt.ExecContext(ctx, fmt.Sprintf("synthetic-%09d", index), "synthetic-active", body, fmt.Sprintf("2026-01-01T00:00:%09dZ", index)); err != nil {
			return fixtureInfo{}, err
		}
	}
	if err = stmt.Close(); err != nil {
		return fixtureInfo{}, err
	}
	if err = tx.Commit(); err != nil {
		return fixtureInfo{}, err
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM events WHERE id IN (SELECT id FROM events WHERE length(body)<1024 AND body != 'synthetic-needle' LIMIT 1000)`); err != nil {
		return fixtureInfo{}, err
	}
	var free int64
	if err = db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return fixtureInfo{}, err
	}
	var wal int64
	if stat, statErr := os.Stat(path + "-wal"); statErr == nil {
		wal = stat.Size()
	}
	return fixtureInfo{Kind: "synthetic", SmallRows: smallRows, LargeRows: largeRows, WALBytes: wal, FreePages: free}, nil
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }
