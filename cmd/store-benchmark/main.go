// Command store-benchmark produces reproducible, metadata-only SQLite query evidence.
package main

import (
	"context"
	"crypto/sha256"
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
	Name                string   `json:"name"`
	Status              string   `json:"status"`
	TimeoutMS           int64    `json:"timeout_ms,omitempty"`
	ElapsedLowerBoundUS int64    `json:"elapsed_lower_bound_us,omitempty"`
	ColdP50US           int64    `json:"cold_p50_us"`
	ColdP95US           int64    `json:"cold_p95_us"`
	WarmP50US           int64    `json:"warm_p50_us"`
	WarmP95US           int64    `json:"warm_p95_us"`
	QueryPlan           []string `json:"query_plan"`
	MatchedRows         *int64   `json:"matched_rows,omitempty"`
}

func main() {
	var dbPath, synthetic string
	var validateBaseline, calibrateDir, localityDir string
	var foldGates bool
	var foldThreshold, foldWakeBudget int64
	var iterations, smallRows, largeRows, calibrateRows, calibrateEnormousRows, calibrateEnormousBytes int
	var localityRows, localityBodyBytes int
	var caseTimeout time.Duration
	flag.StringVar(&dbPath, "db", "", "path to an operator-created store copy (opened immutable/read-only)")
	flag.StringVar(&synthetic, "synthetic", "", "create and benchmark a synthetic store at this new path")
	flag.StringVar(&validateBaseline, "validate-baseline", "", "validate a sanitized capacity baseline artifact and exit")
	flag.StringVar(&calibrateDir, "calibrate-gates", "", "write per-corpus stores and a storage-gate range report under this new directory")
	flag.StringVar(&localityDir, "measure-body-locality", "", "write scratch inline/side-table stores and a body-locality report under this new directory")
	flag.BoolVar(&foldGates, "fold-gates", false, "measure refinement ratio and per-host wake eligibility on --db (never the live store)")
	flag.Int64Var(&foldThreshold, "fold-threshold-bytes", 0, "consolidation threshold used to decide sessions worth folding (default 65536)")
	flag.Int64Var(&foldWakeBudget, "fold-wake-budget-bytes", 0, "wake injection budget for per-host fit (default 8192)")
	flag.IntVar(&iterations, "iterations", 15, "samples per cold and warm series")
	flag.IntVar(&smallRows, "small-rows", 10000, "synthetic small event rows")
	flag.IntVar(&largeRows, "large-rows", 8, "synthetic 1 MiB event rows")
	flag.IntVar(&calibrateRows, "calibrate-rows", 256, "events per non-enormous calibrate corpus")
	flag.IntVar(&calibrateEnormousRows, "calibrate-enormous-rows", 2, "1 MiB-class rows in the enormous corpus")
	flag.IntVar(&calibrateEnormousBytes, "calibrate-enormous-bytes", 1<<20, "body size of each enormous row")
	flag.IntVar(&localityRows, "locality-rows", defaultLocalityRows, "events per body-locality corpus")
	flag.IntVar(&localityBodyBytes, "locality-body-bytes", defaultLocalityBodyBytes, "plaintext body size before the canonical codec")
	flag.DurationVar(&caseTimeout, "case-timeout", 2*time.Minute, "maximum duration for each benchmark case")
	flag.Parse()
	selectedModes := 0
	for _, selected := range []bool{validateBaseline != "", synthetic != "", calibrateDir != "", localityDir != "", foldGates, dbPath != "" && !foldGates} {
		if selected {
			selectedModes++
		}
	}
	if selectedModes != 1 {
		fatal("specify exactly one benchmark or validation mode")
	}
	if localityDir != "" {
		report, err := runBodyLocality(context.Background(), bodyLocalityOpts{
			Dir:       localityDir,
			Rows:      localityRows,
			BodyBytes: localityBodyBytes,
			Seed:      bodyLocalitySeed,
			Iters:     iterations,
		})
		if err != nil {
			fatal(err.Error())
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fatal(err.Error())
		}
		return
	}
	if foldGates {
		if dbPath == "" {
			fatal("--fold-gates requires --db pointing at an operator copy")
		}
		if err := runFoldGates(context.Background(), dbPath, foldThreshold, foldWakeBudget); err != nil {
			fatal(err.Error())
		}
		return
	}
	if calibrateDir != "" {
		report, err := runCalibrateGates(context.Background(), calibrateOpts{
			Dir:           calibrateDir,
			Rows:          calibrateRows,
			EnormousRows:  calibrateEnormousRows,
			EnormousBytes: calibrateEnormousBytes,
			Seed:          calibrateSeed,
		})
		if err != nil {
			fatal(err.Error())
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fatal(err.Error())
		}
		return
	}
	if validateBaseline != "" {
		if err := validateBaselineFile(validateBaseline); err != nil {
			fatal(err.Error())
		}
		return
	}
	if iterations < 1 || smallRows < 1 || largeRows < 1 || caseTimeout < time.Millisecond {
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
	queryDB, err := openCompatibleReadOnly(ctx, dbPath)
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
		plan, err := explainCase(ctx, dbPath, benchmarkCase.SQL, benchmarkCase.Args)
		if err != nil {
			fatal(fmt.Sprintf("%s plan: %v", benchmarkCase.Name, err))
		}
		started := time.Now()
		caseCtx, cancel := context.WithTimeout(ctx, caseTimeout)
		var matchedRows *int64
		if benchmarkCase.Name == "search" {
			count, countErr := queryRowCount(caseCtx, dbPath, benchmarkCase.SQL, benchmarkCase.Args)
			if errors.Is(countErr, context.DeadlineExceeded) {
				results = append(results, timeoutResult(benchmarkCase.Name, caseTimeout, time.Since(started), plan))
				overallStatus = "timeout"
				cancel()
				continue
			}
			if countErr != nil {
				fatal(fmt.Sprintf("search matched rows: %v", countErr))
			}
			if info.Kind == "synthetic" && count == 0 {
				fatal("synthetic search preflight returned zero matches")
			}
			matchedRows = &count
		}
		if benchmarkCase.Name == "active" || benchmarkCase.Name == "latest" {
			matched, err := queryHasRows(caseCtx, dbPath, benchmarkCase.SQL, benchmarkCase.Args)
			if errors.Is(err, context.DeadlineExceeded) {
				results = append(results, timeoutResult(benchmarkCase.Name, caseTimeout, time.Since(started), plan))
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
			results = append(results, timeoutResult(benchmarkCase.Name, caseTimeout, time.Since(started), plan))
			overallStatus = "timeout"
			continue
		}
		if err != nil {
			fatal(fmt.Sprintf("%s: %v", benchmarkCase.Name, err))
		}
		result.MatchedRows = matchedRows
		results = append(results, result)
	}
	var expected *handoffCardinality
	if info.Kind == "synthetic" {
		expected = &handoffCardinality{Commands: info.CommandRows, Memories: info.AcceptedMemories}
	}
	handoffPlan, err := explainHandoff(ctx, dbPath)
	if err != nil {
		fatal(fmt.Sprintf("handoff plan: %v", err))
	}
	handoffCtx, cancelHandoff := context.WithTimeout(ctx, caseTimeout)
	handoffStarted := time.Now()
	handoff, err := benchmarkHandoff(handoffCtx, dbPath, iterations, expected)
	cancelHandoff()
	if errors.Is(err, context.DeadlineExceeded) {
		handoff = timeoutResult("handoff", caseTimeout, time.Since(handoffStarted), handoffPlan)
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

func timeoutResult(name string, limit, elapsed time.Duration, plan []string) caseResult {
	if elapsed < limit {
		elapsed = limit
	}
	return caseResult{Name: name, Status: "timeout", TimeoutMS: limit.Milliseconds(), ElapsedLowerBoundUS: elapsed.Microseconds(), QueryPlan: plan}
}

func explainCase(ctx context.Context, path, query string, args []any) ([]string, error) {
	db, err := openCompatibleReadOnly(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open plan store: %w", err)
	}
	defer func() { _ = db.Close() }()
	return queryPlan(ctx, db, query, args)
}

func explainHandoff(ctx context.Context, path string) ([]string, error) {
	db, err := openCompatibleReadOnly(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open handoff plan store: %w", err)
	}
	defer func() { _ = db.Close() }()
	var plans []string
	for _, step := range infra.CapacityHandoffPlanQueries() {
		details, err := queryPlan(ctx, db, step.SQL, step.Args)
		if err != nil {
			return nil, err
		}
		for _, detail := range details {
			plans = append(plans, step.Name+": "+detail)
		}
	}
	return plans, nil
}

func queryHasRows(ctx context.Context, path, query string, args []any) (bool, error) {
	db, err := openCompatibleReadOnly(ctx, path)
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

func queryRowCount(ctx context.Context, path, query string, args []any) (int64, error) {
	db, err := openCompatibleReadOnly(ctx, path)
	if err != nil {
		return 0, fmt.Errorf("open match-count store: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("query match count: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var count int64
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate match count: %w", err)
	}
	return count, nil
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
	planDB, err := openCompatibleReadOnly(ctx, path)
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

func openCompatibleReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	db := infra.OpenCoordinatedSQLite(path, readOnlyDSN(path))
	if err := infra.VerifyStoreCompatibility(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify store compatibility: %w", err)
	}
	return db, nil
}

func identityPayloadMetadata(payload string) (string, int, int, int, string) {
	bytes := []byte(payload)
	digest := sha256.Sum256(bytes)
	return "identity", 1, len(bytes), len(bytes), fmt.Sprintf("%x", digest)
}

//nolint:wrapcheck // command boundary adds the benchmark case name before display.
func benchmark(ctx context.Context, path string, iterations int, name, query string, args []any) (caseResult, error) {
	cold, warm := make([]int64, 0, iterations), make([]int64, 0, iterations)
	var plan []string
	for index := 0; index < iterations; index++ {
		db, err := openCompatibleReadOnly(ctx, path)
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
	db := infra.OpenCoordinatedSQLite(path, path)
	defer func() { _ = db.Close() }()
	if err := infra.VerifyStoreCompatibility(ctx, db); err != nil {
		return fixtureInfo{}, fmt.Errorf("verify synthetic store compatibility: %w", err)
	}
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`} {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			return fixtureInfo{}, err
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO sessions(session_id,started_at,ended_at,client,agent,workspace) VALUES('synthetic-active','2026-01-01T00:00:00Z',NULL,'cli','codex','synthetic'),('synthetic-ended','2025-01-01T00:00:00Z','2025-01-02T00:00:00Z','cli','codex','synthetic')`); err != nil {
		return fixtureInfo{}, err
	}
	insertEvent := func(id, kind, sessionID, body, createdAt string) error {
		codec, version, plaintextBytes, encodedBytes, digest := identityPayloadMetadata(body)
		_, execErr := db.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace,body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256) VALUES(?,?,'codex',?,?,?,'cli','synthetic',?,?,?,?,?)`, id, kind, sessionID, body, createdAt, codec, version, plaintextBytes, encodedBytes, digest)
		return execErr
	}
	for _, event := range []struct{ id, kind, sessionID, body, createdAt string }{
		{"synthetic-active-start", "session_started", "synthetic-active", "synthetic lifecycle", "2026-01-01T00:00:00Z"},
		{"synthetic-ended-start", "session_started", "synthetic-ended", "synthetic lifecycle", "2025-01-01T00:00:00Z"},
		{"synthetic-ended-end", "session_ended", "synthetic-ended", "synthetic lifecycle", "2025-01-02T00:00:00Z"},
	} {
		if err = insertEvent(event.id, event.kind, event.sessionID, event.body, event.createdAt); err != nil {
			return fixtureInfo{}, err
		}
	}
	for index := 0; index < 10; index++ {
		eventID := fmt.Sprintf("synthetic-command-%02d", index)
		created := time.Date(2026, 1, 2, 0, 0, index, 0, time.UTC).Format(time.RFC3339Nano)
		if err = insertEvent(eventID, "command_executed", "synthetic-active", "synthetic command", created); err != nil {
			return fixtureInfo{}, err
		}
		command, input, output := fmt.Sprintf("echo synthetic-%02d", index), "", "synthetic output"
		commandCodec, commandVersion, commandPlaintextBytes, commandEncodedBytes, commandDigest := identityPayloadMetadata(command)
		inputCodec, inputVersion, inputPlaintextBytes, inputEncodedBytes, inputDigest := identityPayloadMetadata(input)
		outputCodec, outputVersion, outputPlaintextBytes, outputEncodedBytes, outputDigest := identityPayloadMetadata(output)
		if _, err = db.ExecContext(ctx, `INSERT INTO command_audits(event_id,command_text,input_text,output_text,command_codec,command_format_version,command_plaintext_bytes,command_encoded_bytes,command_sha256,input_codec,input_format_version,input_plaintext_bytes,input_encoded_bytes,input_sha256,output_codec,output_format_version,output_plaintext_bytes,output_encoded_bytes,output_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, eventID, command, input, output, commandCodec, commandVersion, commandPlaintextBytes, commandEncodedBytes, commandDigest, inputCodec, inputVersion, inputPlaintextBytes, inputEncodedBytes, inputDigest, outputCodec, outputVersion, outputPlaintextBytes, outputEncodedBytes, outputDigest); err != nil {
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
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace,body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256) VALUES(?,'prompt','codex',?,?,?,'cli','synthetic',?,?,?,?,?)`)
	if err != nil {
		return fixtureInfo{}, err
	}
	const disposableRows = 1000
	for index := 0; index < smallRows+largeRows; index++ {
		body := "synthetic-small"
		if index == 0 {
			body = "synthetic-needle"
		}
		if index >= smallRows {
			body = strings.Repeat("L", 1<<20)
		}
		createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Millisecond).Format(time.RFC3339Nano)
		codec, version, plaintextBytes, encodedBytes, digest := identityPayloadMetadata(body)
		if _, err = stmt.ExecContext(ctx, fmt.Sprintf("synthetic-keep-%09d", index), "synthetic-active", body, createdAt, codec, version, plaintextBytes, encodedBytes, digest); err != nil {
			return fixtureInfo{}, err
		}
	}
	if err = stmt.Close(); err != nil {
		return fixtureInfo{}, err
	}
	if err = tx.Commit(); err != nil {
		return fixtureInfo{}, err
	}
	// Freelist pages used to come from dropping synthetic event rows. That
	// path is reserved for the Kimi transcript supersede; a scratch table
	// creates the same freelist without deleting events rows.
	if _, err = db.ExecContext(ctx, `CREATE TABLE synthetic_disposable(id TEXT PRIMARY KEY, body TEXT)`); err != nil {
		return fixtureInfo{}, err
	}
	for index := 0; index < disposableRows; index++ {
		if _, err = db.ExecContext(ctx, `INSERT INTO synthetic_disposable(id, body) VALUES(?, 'synthetic-small')`, fmt.Sprintf("synthetic-disposable-%09d", index)); err != nil {
			return fixtureInfo{}, err
		}
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM synthetic_disposable; DROP TABLE synthetic_disposable`); err != nil {
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
