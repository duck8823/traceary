package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRetiredRepairsIssueNoWorkOnSecondOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedRetiredOneOffStore(t, path, false)

	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("InitializeAuthorized(078) error = %v", err)
	}

	log := &repairStatementLog{}
	installRepairStatementObserver(t, path, log)

	t.Run("second_open_issues_no_repair_statements", func(t *testing.T) {
		openRetiredStoreSkippingProjection(t, path)
		log.reset()
		openRetiredStoreSkippingProjection(t, path)
		if kinds := log.forbidden(); len(kinds) != 0 {
			t.Fatalf("second open issued repair-object statements %v\nqueries:\n%s", kinds, strings.Join(log.queries, "\n"))
		}
	})
}

func TestDirtyStoreRepairsOnceThenRetires(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedRetiredOneOffStore(t, path, true)

	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("InitializeAuthorized(078) error = %v", err)
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var observedAt string
	if err := conn.QueryRow(`SELECT observed_at FROM usage_observations WHERE observation_id = 'grok:stop_hook:dirty'`).Scan(&observedAt); err != nil {
		t.Fatal(err)
	}
	if observedAt == epochZeroHookUsageObservedAt || strings.HasPrefix(observedAt, "1970-01-01T00:00:00") {
		t.Fatalf("observed_at = %q, want repaired away from epoch", observedAt)
	}

	log := &repairStatementLog{}
	installRepairStatementObserver(t, path, log)
	openRetiredStoreSkippingProjection(t, path)
	openRetiredStoreSkippingProjection(t, path)
	if kinds := log.forbidden(); len(kinds) != 0 {
		t.Fatalf("retired dirty store re-issued repair-object statements %v\nqueries:\n%s", kinds, strings.Join(log.queries, "\n"))
	}
}

func TestDoubleOpenIssuesNoRepairStatements(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedRetiredOneOffStore(t, path, false)
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("InitializeAuthorized(078) error = %v", err)
	}
	log := &repairStatementLog{}
	installRepairStatementObserver(t, path, log)
	t.Run("second_open_issues_no_repair_statements", func(t *testing.T) {
		openRetiredStoreSkippingProjection(t, path)
		log.reset()
		openRetiredStoreSkippingProjection(t, path)
		if kinds := log.forbidden(); len(kinds) != 0 {
			t.Fatalf("second open issued repair-object statements %v\nqueries:\n%s", kinds, strings.Join(log.queries, "\n"))
		}
	})
}

func TestWorkspaceExhaustedGateUsesStateRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name     string
		schema   string
		want     bool
		wantSkip bool
	}{
		{
			name: "exhausted=1 skips the sweep",
			schema: workspaceObservationCatchUpStateSchema + aggregatedWorkspaceObservationSchema + `
				UPDATE workspace_observation_catchup_state SET exhausted = 1 WHERE singleton = 1;`,
			want:     true,
			wantSkip: true,
		},
		{
			name: "exhausted=0 does not skip",
			schema: workspaceObservationCatchUpStateSchema + aggregatedWorkspaceObservationSchema + `
				INSERT INTO sessions (session_id, workspace) VALUES ('s', '/repo');
				INSERT INTO events (id, session_id, workspace, created_at, created_at_norm, agent)
				VALUES ('e', 's', '/repo', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00.000000000Z', 'codex');`,
			want:     false,
			wantSkip: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(tt.schema); err != nil {
				t.Fatalf("schema: %v", err)
			}
			got, err := workspaceObservationBackfillIsExhausted(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("exhausted = %v, want %v", got, tt.want)
			}
			result, err := backfillWorkspaceObservationsBatch(ctx, db, 10)
			if err != nil {
				t.Fatal(err)
			}
			if result.Skipped != tt.wantSkip {
				t.Fatalf("skipped = %v, want %v (result=%+v)", result.Skipped, tt.wantSkip, result)
			}
		})
	}
}

func TestEpochZeroHookUsageGoSQLParity(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fixtures := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "valid post-epoch RFC3339Nano", raw: "2026-08-15T18:30:00Z", want: true},
		{name: "valid post-epoch with nanos", raw: "2026-08-15T18:30:00.123456789Z", want: true},
		{name: "leading and trailing space", raw: " 2026-08-15T18:30:00Z ", want: true},
		{name: "unix epoch", raw: "1970-01-01T00:00:00.000000000Z", want: false},
		{name: "unix epoch trimmed RFC3339Nano", raw: "1970-01-01T00:00:00Z", want: false},
		{name: "first second is usable", raw: "1970-01-01T00:00:01Z", want: true},
		{name: "sub-second of epoch is not usable", raw: "1970-01-01T00:00:00.999999999Z", want: false},
		{name: "malformed", raw: "not-a-timestamp", want: false},
		{name: "empty", raw: "", want: false},
		{name: "whitespace only", raw: "   ", want: false},
		{name: "numeric zero", raw: "0", want: false},
		{name: "pre-epoch", raw: "1969-12-31T23:59:59Z", want: false},
	}
	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			gotGo := goUsableSessionRepairTime(tt.raw)
			var gotSQL int
			if err := db.QueryRow(`SELECT CASE WHEN ts_valid(trim(?)) = 1 AND ts_norm(trim(?)) >= '1970-01-01T00:00:01.000000000Z' THEN 1 ELSE 0 END`, tt.raw, tt.raw).Scan(&gotSQL); err != nil {
				t.Fatal(err)
			}
			if gotGo != tt.want {
				t.Fatalf("Go predicate = %v, want %v", gotGo, tt.want)
			}
			if (gotSQL == 1) != tt.want {
				t.Fatalf("SQL predicate = %d, want %v", gotSQL, tt.want)
			}
		})
	}
}

func goUsableSessionRepairTime(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil || parsed.Unix() <= 0 {
		return false
	}
	return true
}

func seedRetiredOneOffStore(t *testing.T, path string, dirty bool) {
	t.Helper()
	ctx := context.Background()
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 78))).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-078) error = %v", err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	sessionTime := time.Date(2026, 8, 15, 18, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
INSERT OR IGNORE INTO sessions (session_id, started_at, ended_at, client, agent, workspace)
VALUES ('session-retired', ?, ?, 'test', 'test', 'ws')`, sessionTime, sessionTime); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := conn.Exec(`
INSERT INTO events (id, kind, agent, session_id, body, created_at, client, workspace)
VALUES ('event-retired', 'session_ended', 'test', 'session-retired', 'ended', ?, 'test', 'ws')`, sessionTime); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := conn.Exec(`UPDATE workspace_observation_catchup_state SET exhausted = 1 WHERE singleton = 1`); err != nil {
		t.Fatalf("mark exhausted: %v", err)
	}
	if !dirty {
		return
	}
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
INSERT INTO usage_observations (
    observation_id, session_id, host, source_name, source_version,
    scope, accounting, status, observed_at, finalized_at, terminal_code,
    input_state, cached_input_state, cache_write_input_state,
    output_state, reasoning_output_state, total_state, cost_state
) VALUES ('grok:stop_hook:dirty', 'session-retired', 'grok', 'stop_hook', 'schema-v1',
    'call', 'excluded', 'finalized', ?, ?, 'unknown',
    'unavailable', 'unavailable', 'unavailable',
    'unavailable', 'unavailable', 'unavailable', 'unavailable')`, epoch, epoch); err != nil {
		t.Fatalf("insert dirty usage: %v", err)
	}
}

func openRetiredStoreSkippingProjection(t *testing.T, path string) {
	t.Helper()
	db := NewDatabase(filepath.Join(t.TempDir(), "other.db"), preparedMigrations(t))
	if err := db.initializeAt(context.Background(), path, false); err != nil {
		t.Fatalf("initializeAt error = %v", err)
	}
}

func installRepairStatementObserver(t *testing.T, path string, log *repairStatementLog) {
	t.Helper()
	coordinatedSQLiteDriverMu.Lock()
	orig := coordinatedSQLiteDriver
	coordinatedSQLiteDriver = observingDriver{inner: orig, rec: log, path: path}
	coordinatedSQLiteDriverMu.Unlock()
	t.Cleanup(func() {
		coordinatedSQLiteDriverMu.Lock()
		coordinatedSQLiteDriver = orig
		coordinatedSQLiteDriverMu.Unlock()
	})
}

type repairStatementLog struct {
	mu      sync.Mutex
	kinds   []string
	queries []string
}

func (l *repairStatementLog) observe(query string) {
	kind := classifyRepairObjectStatement(query)
	if kind == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = append(l.kinds, kind)
	l.queries = append(l.queries, query)
}

func (l *repairStatementLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = nil
	l.queries = nil
}

func (l *repairStatementLog) forbidden() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, kind := range l.kinds {
		switch kind {
		case "epoch_descriptor_ddl", "epoch_observed_at", "workspace_head_frontier":
			out = append(out, kind)
		}
	}
	return out
}

func classifyRepairObjectStatement(query string) string {
	compact := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	switch {
	case strings.Contains(compact, "usage_observations_reject_descriptor_update") &&
		(strings.Contains(compact, "drop trigger") || strings.Contains(compact, "create trigger")):
		return "epoch_descriptor_ddl"
	case strings.Contains(compact, "update sessions") && strings.Contains(compact, "started_at_norm"):
		return "report_norm_sessions"
	case strings.Contains(compact, "update usage_observations") && strings.Contains(compact, "observed_at_norm"):
		return "report_norm_usage"
	case strings.Contains(compact, "update usage_observations") && strings.Contains(compact, "set observed_at"):
		return "epoch_observed_at"
	case isWorkspaceHeadOrFrontierQuery(compact):
		return "workspace_head_frontier"
	default:
		return ""
	}
}

func isWorkspaceHeadOrFrontierQuery(compact string) bool {
	if !strings.Contains(compact, "from events") || !strings.Contains(compact, "left join sessions") {
		return false
	}
	return strings.Contains(compact, "order by e.created_at_norm desc, e.id desc")
}

type observingDriver struct {
	inner driver.Driver
	rec   *repairStatementLog
	path  string
}

func (d observingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err //nolint:wrapcheck // test driver forwards the inner Open error unchanged
	}
	if d.path != "" && !strings.Contains(name, d.path) {
		return conn, nil
	}
	return &observingConn{inner: conn, rec: d.rec}, nil
}

type observingConn struct {
	inner driver.Conn
	rec   *repairStatementLog
}

func (c *observingConn) Prepare(query string) (driver.Stmt, error) {
	c.rec.observe(query)
	return c.inner.Prepare(query) //nolint:wrapcheck // test observer forwards the inner statement
}

func (c *observingConn) Close() error {
	return c.inner.Close() //nolint:wrapcheck // test observer forwards Close
}

func (c *observingConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *observingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	begin, ok := c.inner.(driver.ConnBeginTx)
	if !ok {
		return nil, driver.ErrSkip
	}
	return begin.BeginTx(ctx, opts) //nolint:wrapcheck // test observer forwards BeginTx
}

func (c *observingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.rec.observe(query)
	prepare, ok := c.inner.(driver.ConnPrepareContext)
	if !ok {
		return c.inner.Prepare(query) //nolint:wrapcheck // test observer forwards Prepare
	}
	return prepare.PrepareContext(ctx, query) //nolint:wrapcheck // test observer forwards PrepareContext
}

func (c *observingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.rec.observe(query)
	exec, ok := c.inner.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return exec.ExecContext(ctx, query, args) //nolint:wrapcheck // test observer forwards ExecContext
}

func (c *observingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.rec.observe(query)
	queryer, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, query, args) //nolint:wrapcheck // test observer forwards QueryContext
}

func (c *observingConn) Ping(ctx context.Context) error {
	pinger, ok := c.inner.(driver.Pinger)
	if !ok {
		return nil
	}
	return pinger.Ping(ctx) //nolint:wrapcheck // test observer forwards Ping
}

func (c *observingConn) ResetSession(ctx context.Context) error {
	resetter, ok := c.inner.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return resetter.ResetSession(ctx) //nolint:wrapcheck // test observer forwards ResetSession
}

func (c *observingConn) IsValid() bool {
	if validator, ok := c.inner.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}
