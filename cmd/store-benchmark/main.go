// Command store-benchmark produces reproducible, metadata-only SQLite query evidence.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

type report struct {
	SchemaVersion string       `json:"schema_version"`
	Status        string       `json:"status"`
	Fixture       fixtureInfo  `json:"fixture"`
	Iterations    int          `json:"iterations"`
	Cases         []caseResult `json:"cases"`
}
type fixtureInfo struct {
	Kind             string `json:"kind"`
	SmallRows        int    `json:"small_rows,omitempty"`
	LargeRows        int    `json:"large_rows,omitempty"`
	WALBytes         int64  `json:"wal_bytes,omitempty"`
	FreePages        int64  `json:"free_pages,omitempty"`
	ActiveSessions   int    `json:"active_sessions,omitempty"`
	CommandRows      int    `json:"command_rows,omitempty"`
	AcceptedMemories int    `json:"accepted_memories,omitempty"`
}
type caseResult struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	TimeoutMS int64    `json:"timeout_ms,omitempty"`
	ColdP50US int64    `json:"cold_p50_us"`
	ColdP95US int64    `json:"cold_p95_us"`
	WarmP50US int64    `json:"warm_p50_us"`
	WarmP95US int64    `json:"warm_p95_us"`
	QueryPlan []string `json:"query_plan"`
}

func main() {
	var dbPath, synthetic string
	var validateBaseline string
	var iterations, smallRows, largeRows int
	var caseTimeout time.Duration
	flag.StringVar(&dbPath, "db", "", "path to an operator-created store copy (opened immutable/read-only)")
	flag.StringVar(&synthetic, "synthetic", "", "create and benchmark a synthetic store at this new path")
	flag.StringVar(&validateBaseline, "validate-baseline", "", "validate a sanitized capacity baseline artifact and exit")
	flag.IntVar(&iterations, "iterations", 15, "samples per cold and warm series")
	flag.IntVar(&smallRows, "small-rows", 10000, "synthetic small event rows")
	flag.IntVar(&largeRows, "large-rows", 8, "synthetic 1 MiB event rows")
	flag.DurationVar(&caseTimeout, "case-timeout", 2*time.Minute, "maximum duration for each benchmark case")
	flag.Parse()
	if validateBaseline != "" {
		if err := validateBaselineFile(validateBaseline); err != nil {
			fatal(err.Error())
		}
		return
	}
	if (dbPath == "") == (synthetic == "") || iterations < 1 || smallRows < 1 || largeRows < 1 || caseTimeout <= 0 {
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
	queryDB, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		fatal(err.Error())
	}
	benchmarkCases, err := infra.CapacityBenchmarkQueries(ctx, queryDB)
	if closeErr := queryDB.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fatal(err.Error())
	}
	results := make([]caseResult, 0, len(benchmarkCases))
	overallStatus := "passed"
	for _, benchmarkCase := range benchmarkCases {
		caseCtx, cancel := context.WithTimeout(ctx, caseTimeout)
		if benchmarkCase.Name == "active" || benchmarkCase.Name == "latest" {
			matched, err := queryHasRows(caseCtx, dbPath, benchmarkCase.SQL, benchmarkCase.Args)
			if errors.Is(err, context.DeadlineExceeded) {
				results = append(results, timeoutResult(benchmarkCase.Name, caseTimeout))
				overallStatus = "timeout"
				cancel()
				continue
			}
			if err != nil || !matched {
				fatal(fmt.Sprintf("%s preflight returned no matching production row: %v", benchmarkCase.Name, err))
			}
		}
		result, err := benchmark(caseCtx, dbPath, iterations, benchmarkCase.Name, benchmarkCase.SQL, benchmarkCase.Args)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			results = append(results, timeoutResult(benchmarkCase.Name, caseTimeout))
			overallStatus = "timeout"
			continue
		}
		if err != nil {
			fatal(fmt.Sprintf("%s: %v", benchmarkCase.Name, err))
		}
		results = append(results, result)
	}
	var expected *handoffCardinality
	if info.Kind == "synthetic" {
		expected = &handoffCardinality{Commands: info.CommandRows, Memories: info.AcceptedMemories}
	}
	handoffCtx, cancelHandoff := context.WithTimeout(ctx, caseTimeout)
	handoff, err := benchmarkHandoff(handoffCtx, dbPath, iterations, expected)
	cancelHandoff()
	if errors.Is(err, context.DeadlineExceeded) {
		handoff = timeoutResult("handoff", caseTimeout)
		overallStatus = "timeout"
		err = nil
	}
	if err != nil {
		fatal(fmt.Sprintf("handoff: %v", err))
	}
	results = append(results, handoff)
	if err := json.NewEncoder(os.Stdout).Encode(report{SchemaVersion: "traceary.store-benchmark/v1", Status: overallStatus, Fixture: info, Iterations: iterations, Cases: results}); err != nil {
		fatal(err.Error())
	}
}

func timeoutResult(name string, limit time.Duration) caseResult {
	return caseResult{Name: name, Status: "timeout", TimeoutMS: limit.Milliseconds()}
}

func queryHasRows(ctx context.Context, path, query string, args []any) (bool, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return false, fmt.Errorf("open preflight store: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("query preflight: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

type handoffCardinality struct{ Commands, Memories int }

func benchmarkHandoff(ctx context.Context, path string, iterations int, expected *handoffCardinality) (caseResult, error) {
	operation := func(database *infra.Database) error {
		pack, err := usecase.NewContextUsecase(infra.NewSessionDatasource(database), infra.NewEventDatasource(database), infra.NewMemoryDatasource(database)).Handoff(ctx, apptypes.NewContextPackCriteriaBuilder().AllowStale(true).RecentCommandsLimit(10).MemoryLimit(10).Build())
		if err != nil {
			return fmt.Errorf("execute production handoff orchestration: %w", err)
		}
		value, ok := pack.Value()
		if !ok {
			return fmt.Errorf("production handoff returned no context pack")
		}
		if expected != nil && (len(value.RecentCommands()) != expected.Commands || len(value.Memories()) != expected.Memories) {
			return fmt.Errorf("production handoff cardinality commands=%d memories=%d, want %d/%d", len(value.RecentCommands()), len(value.Memories()), expected.Commands, expected.Memories)
		}
		return nil
	}
	cold, warm := make([]int64, 0, iterations), make([]int64, 0, iterations)
	for i := 0; i < iterations; i++ {
		database, err := infra.NewImmutableReadDatabase(ctx, path)
		if err != nil {
			return caseResult{}, fmt.Errorf("open cold immutable handoff group: %w", err)
		}
		start := time.Now()
		if err := operation(database); err != nil {
			_ = database.CloseSharedReadOnly()
			return caseResult{}, err
		}
		cold = append(cold, time.Since(start).Microseconds())
		start = time.Now()
		if err := operation(database); err != nil {
			_ = database.CloseSharedReadOnly()
			return caseResult{}, err
		}
		warm = append(warm, time.Since(start).Microseconds())
		if err := database.CloseSharedReadOnly(); err != nil {
			return caseResult{}, fmt.Errorf("close immutable handoff group: %w", err)
		}
	}
	planDB, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return caseResult{}, fmt.Errorf("open handoff plan store: %w", err)
	}
	defer func() { _ = planDB.Close() }()
	var plans []string
	for _, step := range infra.CapacityHandoffPlanQueries() {
		details, err := queryPlan(ctx, planDB, step.SQL, step.Args)
		if err != nil {
			return caseResult{}, err
		}
		for _, detail := range details {
			plans = append(plans, step.Name+": "+detail)
		}
	}
	return caseResult{Name: "handoff", Status: "passed", ColdP50US: percentile(cold, .5), ColdP95US: percentile(cold, .95), WarmP50US: percentile(warm, .5), WarmP95US: percentile(warm, .95), QueryPlan: plans}, nil
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
	return caseResult{Name: name, Status: "passed", ColdP50US: percentile(cold, .50), ColdP95US: percentile(cold, .95), WarmP50US: percentile(warm, .50), WarmP95US: percentile(warm, .95), QueryPlan: plan}, nil
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
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	values := make([]any, len(columns))
	for index := range values {
		values[index] = new(sql.RawBytes)
	}
	for rows.Next() {
		if err := rows.Scan(values...); err != nil {
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
	if smallRows < 1 || largeRows < 1 {
		return fixtureInfo{}, fmt.Errorf("synthetic fixture requires at least one small and one large row")
	}
	if _, err := os.Stat(path); err == nil {
		return fixtureInfo{}, fmt.Errorf("synthetic destination already exists")
	}
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		return fixtureInfo{}, err
	}
	database := infra.NewDatabase(path, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		return fixtureInfo{}, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fixtureInfo{}, err
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`} {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			return fixtureInfo{}, err
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO sessions(session_id,started_at,ended_at,client,agent,workspace) VALUES('synthetic-active','2026-01-01T00:00:00Z',NULL,'cli','codex','synthetic'),('synthetic-ended','2025-01-01T00:00:00Z','2025-01-02T00:00:00Z','cli','codex','synthetic')`); err != nil {
		return fixtureInfo{}, err
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('synthetic-active-start','session_started','codex','synthetic-active','synthetic lifecycle','2026-01-01T00:00:00Z','cli','synthetic'),('synthetic-ended-start','session_started','codex','synthetic-ended','synthetic lifecycle','2025-01-01T00:00:00Z','cli','synthetic'),('synthetic-ended-end','session_ended','codex','synthetic-ended','synthetic lifecycle','2025-01-02T00:00:00Z','cli','synthetic')`); err != nil {
		return fixtureInfo{}, err
	}
	for index := 0; index < 10; index++ {
		eventID := fmt.Sprintf("synthetic-command-%02d", index)
		created := time.Date(2026, 1, 2, 0, 0, index, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err = db.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'command_executed','codex','synthetic-active',?,?, 'cli','synthetic')`, eventID, "synthetic command", created); err != nil {
			return fixtureInfo{}, err
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO command_audits(event_id,command_text,input_text,output_text) VALUES(?,?,?,?)`, eventID, fmt.Sprintf("echo synthetic-%02d", index), "", "synthetic output"); err != nil {
			return fixtureInfo{}, err
		}
	}
	for index := 0; index < 10; index++ {
		now := time.Date(2026, 1, 1, 0, index, 0, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err = db.ExecContext(ctx, `INSERT INTO memories(id,type,scope_kind,scope_value,fact,status,confidence,source,created_at,updated_at,valid_from) VALUES(?,'decision','workspace','synthetic',?,'accepted','high','manual',?,?,?)`, fmt.Sprintf("synthetic-memory-%02d", index), fmt.Sprintf("synthetic durable fact %02d", index), now, now, now); err != nil {
			return fixtureInfo{}, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fixtureInfo{}, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'prompt','codex',?,?,?,'cli','synthetic')`)
	if err != nil {
		return fixtureInfo{}, err
	}
	const disposableRows = 1000
	for index := 0; index < smallRows+disposableRows+largeRows; index++ {
		body := "synthetic-small"
		if index == 0 {
			body = "synthetic-needle"
		}
		idPrefix := "synthetic-keep"
		if index >= smallRows && index < smallRows+disposableRows {
			idPrefix = "synthetic-disposable"
		}
		if index >= smallRows+disposableRows {
			body = strings.Repeat("L", 1<<20)
		}
		createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Millisecond).Format(time.RFC3339Nano)
		if _, err = stmt.ExecContext(ctx, fmt.Sprintf("%s-%09d", idPrefix, index), "synthetic-active", body, createdAt); err != nil {
			return fixtureInfo{}, err
		}
	}
	if err = stmt.Close(); err != nil {
		return fixtureInfo{}, err
	}
	if err = tx.Commit(); err != nil {
		return fixtureInfo{}, err
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM events WHERE id LIKE 'synthetic-disposable-%'`); err != nil {
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
	return fixtureInfo{Kind: "synthetic", SmallRows: smallRows, LargeRows: largeRows, WALBytes: wal, FreePages: free, ActiveSessions: 1, CommandRows: 10, AcceptedMemories: 10}, nil
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }
