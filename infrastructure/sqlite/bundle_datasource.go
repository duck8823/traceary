package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	sqlitelib "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SQLite constraint codes emitted by modernc.org/sqlite. See
// sqlite3.h — SQLITE_CONSTRAINT_PRIMARYKEY (1555) and
// SQLITE_CONSTRAINT_UNIQUE (2067). We match both so the idempotency
// check survives a future schema that promotes a UNIQUE index to
// PRIMARY KEY (or vice versa).
const (
	sqliteCodePrimaryKeyConflict = 1555
	sqliteCodeUniqueConflict     = 2067
	sqliteCodeForeignKeyConflict = 787
)

const bundleBackfilledParentSessionLabel = "traceary:bundle-backfilled-parent"

// BundleDatasource implements usecase.BundleEventRepository with the
// SQLite-backed Traceary store. Kept as a thin adapter on top of
// EventDatasource + the schema_migrations table so the bundle
// usecase stays infrastructure-agnostic.
type BundleDatasource struct {
	db         *Database
	eventStore *EventDatasource
}

// NewBundleDatasource constructs a BundleDatasource.
func NewBundleDatasource(db *Database, eventStore *EventDatasource) *BundleDatasource {
	return &BundleDatasource{db: db, eventStore: eventStore}
}

var _ usecase.BundleEventRepository = (*BundleDatasource)(nil)

// SchemaVersion returns the max version recorded in
// schema_migrations. 0 means "no migrations have been applied".
func (d *BundleDatasource) SchemaVersion(ctx context.Context) (int, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for schema version lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	var version int
	err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, xerrors.Errorf("failed to read schema_migrations: %w", err)
	}
	return version, nil
}

// ListBundleSessions returns every session in parents-first order for bundle export.
func (d *BundleDatasource) ListBundleSessions(ctx context.Context) ([]*model.Session, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle session export: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	rows, err := db.QueryContext(ctx, `
SELECT session_id, started_at, ended_at, client, agent, workspace, label, summary,
       COALESCE(parent_session_id, ''), COALESCE(spawn_event_id, ''), subagent_kind, spawn_order
FROM sessions
ORDER BY
  CASE WHEN parent_session_id IS NULL THEN 0 ELSE 1 END,
  parent_session_id,
  session_id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query sessions for bundle export: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	out := []*model.Session{}
	for rows.Next() {
		session, err := scanBundleSession(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan bundle session row: %w", err)
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bundle session rows: %w", err)
	}
	return out, nil
}

// ListBundleCommandAudits returns every command audit in deterministic order for bundle export.
func (d *BundleDatasource) ListBundleCommandAudits(ctx context.Context) ([]*model.CommandAudit, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle command audit export: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	rows, err := db.QueryContext(ctx, `
SELECT event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed
FROM command_audits
ORDER BY event_id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query command audits for bundle export: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	out := []*model.CommandAudit{}
	for rows.Next() {
		audit, err := scanBundleCommandAudit(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan bundle command audit row: %w", err)
		}
		out = append(out, audit)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bundle command audit rows: %w", err)
	}
	return out, nil
}

// ListBundleMemories returns every durable memory with refs for bundle export.
func (d *BundleDatasource) ListBundleMemories(ctx context.Context) ([]apptypes.MemoryDetails, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle memory export: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	rows, err := db.QueryContext(ctx, selectMemorySummaryColumnsQuery+`
ORDER BY
  CASE WHEN m.supersedes_memory_id IS NULL THEN 0 ELSE 1 END,
  m.supersedes_memory_id,
  m.id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memories for bundle export: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	out := []apptypes.MemoryDetails{}
	for rows.Next() {
		summary, err := scanMemorySummary(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan bundle memory row: %w", err)
		}
		memory, err := findMemoryByID(ctx, db, summary.MemoryID())
		if err != nil {
			return nil, xerrors.Errorf("failed to load memory refs for %s: %w", summary.MemoryID(), err)
		}
		details, err := apptypes.MemoryDetailsFrom(memory)
		if err != nil {
			return nil, xerrors.Errorf("failed to build memory details for %s: %w", summary.MemoryID(), err)
		}
		out = append(out, details)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bundle memory rows: %w", err)
	}
	return out, nil
}

// ListBundleMemoryEdges returns every memory graph edge for bundle export.
func (d *BundleDatasource) ListBundleMemoryEdges(ctx context.Context) ([]*model.MemoryEdge, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle memory edge export: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	rows, err := db.QueryContext(ctx, `
SELECT id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at
  FROM memory_edges
 ORDER BY valid_from, id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory edges for bundle export: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	edges := []*model.MemoryEdge{}
	for rows.Next() {
		edge, err := scanMemoryEdge(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan bundle memory edge row: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bundle memory edge rows: %w", err)
	}
	return edges, nil
}

// BeginBundleImport starts the transaction shared by every table
// importer in a bundle (sessions, events, command_audits, memories,
// and memory_edges).
func (d *BundleDatasource) BeginBundleImport(ctx context.Context) (usecase.BundleImportTransaction, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle import: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("failed to close resource", "error", closeErr)
		}
		return nil, xerrors.Errorf("failed to begin bundle import transaction: %w", err)
	}
	return &bundleImportTx{db: db, tx: tx}, nil
}

type bundleImportTx struct {
	db *sql.DB
	tx *sql.Tx
}

// ImportSession inserts or replaces the session according to policy. Missing
// parent sessions are handled by the caller-selected bundle parent policy.
func (t *bundleImportTx) ImportSession(
	ctx context.Context,
	session *model.Session,
	policy usecase.BundleConflictPolicy,
	missingParent usecase.BundleMissingParentPolicy,
) (bool, error) {
	if session == nil {
		return false, xerrors.Errorf("session must not be nil")
	}
	if session.ParentSessionID().String() != "" {
		if session.ParentSessionID() == session.SessionID() {
			return false, xerrors.Errorf("session cannot be its own parent")
		}
		parentExists, err := t.sessionExists(ctx, session.ParentSessionID())
		if err != nil {
			return false, err
		}
		if !parentExists {
			switch missingParent {
			case usecase.BundleMissingParentSkip:
				return false, nil
			case usecase.BundleMissingParentBackfill:
				if err := t.backfillParentSession(ctx, session); err != nil {
					return false, err
				}
			default:
				return false, xerrors.Errorf("parent session not found: %s", session.ParentSessionID())
			}
		}
	}
	exists, err := t.sessionExists(ctx, session.SessionID())
	if err != nil {
		return false, err
	}
	if exists {
		switch policy {
		case usecase.BundleConflictSkip:
			return false, nil
		case usecase.BundleConflictError:
			return false, xerrors.Errorf("session conflict")
		}
	}
	if err := t.upsertSession(ctx, session); err != nil {
		return false, err
	}
	return true, nil
}

// ImportEvent inserts or replaces the event according to policy. For
// skip, a unique-constraint violation on the event id returns
// (false, nil) so re-importing the same bundle remains idempotent and
// surfaces events_skipped. For error, the same collision is returned as
// a failure so the whole bundle transaction rolls back.
func (t *bundleImportTx) ImportEvent(ctx context.Context, event *model.Event, policy usecase.BundleConflictPolicy) (bool, error) {
	if event == nil {
		return false, xerrors.Errorf("event must not be nil")
	}
	query := insertEventQuery
	if policy == usecase.BundleConflictReplace {
		query = `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  kind = excluded.kind,
  client = excluded.client,
  agent = excluded.agent,
  session_id = excluded.session_id,
  workspace = excluded.workspace,
  body = excluded.body,
  created_at = excluded.created_at,
  source_hook = excluded.source_hook`
	}
	_, err := t.tx.ExecContext(
		ctx,
		query,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook()),
	)
	if err == nil {
		return true, nil
	}
	if policy == usecase.BundleConflictSkip && isSQLiteUniqueOrPKConflict(err) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to import event %s: %w", event.EventID(), err)
}

// ImportCommandAudit inserts or replaces the command audit according to policy.
// A missing event row is intentionally surfaced as an FK error so audits cannot
// land before or without their referenced event.
func (t *bundleImportTx) ImportCommandAudit(ctx context.Context, audit *model.CommandAudit, policy usecase.BundleConflictPolicy) (bool, error) {
	if audit == nil {
		return false, xerrors.Errorf("command audit must not be nil")
	}
	exists, err := t.commandAuditExists(ctx, audit.EventID())
	if err != nil {
		return false, err
	}
	if exists {
		switch policy {
		case usecase.BundleConflictSkip:
			return false, nil
		case usecase.BundleConflictError:
			return false, xerrors.Errorf("command audit conflict")
		}
	}
	query := insertCommandAuditQuery
	if policy == usecase.BundleConflictReplace {
		query = `INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(event_id) DO UPDATE SET
  command_text = excluded.command_text,
  input_text = excluded.input_text,
  output_text = excluded.output_text,
  input_truncated = excluded.input_truncated,
  output_truncated = excluded.output_truncated,
  input_original_bytes = excluded.input_original_bytes,
  output_original_bytes = excluded.output_original_bytes,
  exit_code = excluded.exit_code,
  failed = excluded.failed`
	}
	var exitCodeSQL *int
	if exitCode, ok := audit.ExitCode().Value(); ok {
		exitCodeSQL = &exitCode
	}
	_, err = t.tx.ExecContext(
		ctx,
		query,
		audit.EventID().String(),
		audit.Command(),
		audit.Input(),
		audit.Output(),
		audit.InputTruncated(),
		audit.OutputTruncated(),
		audit.InputOriginalBytes(),
		audit.OutputOriginalBytes(),
		exitCodeSQL,
		audit.Failed(),
	)
	if err == nil {
		return true, nil
	}
	if policy == usecase.BundleConflictSkip && isSQLiteUniqueOrPKConflict(err) {
		return false, nil
	}
	if isSQLiteForeignKeyConflict(err) {
		return false, xerrors.Errorf("event not found for command audit %s: %w", audit.EventID(), err)
	}
	return false, xerrors.Errorf("failed to import command audit %s: %w", audit.EventID(), err)
}

// ImportMemory inserts or replaces a durable memory according to policy.
func (t *bundleImportTx) ImportMemory(ctx context.Context, memory *model.Memory, policy usecase.BundleConflictPolicy) (bool, error) {
	if memory == nil {
		return false, xerrors.Errorf("memory must not be nil")
	}
	exists, err := t.MemoryExists(ctx, memory.MemoryID())
	if err != nil {
		return false, err
	}
	if exists {
		switch policy {
		case usecase.BundleConflictSkip:
			return false, nil
		case usecase.BundleConflictError:
			return false, xerrors.Errorf("memory conflict")
		}
	}
	if err := persistMemoryTx(ctx, t.tx, memory); err != nil {
		return false, xerrors.Errorf("failed to import memory %s: %w", memory.MemoryID(), err)
	}
	return true, nil
}

// ImportMemoryEdge inserts or replaces a memory graph edge according to policy.
func (t *bundleImportTx) ImportMemoryEdge(ctx context.Context, edge *model.MemoryEdge, policy usecase.BundleConflictPolicy) (bool, error) {
	if edge == nil {
		return false, xerrors.Errorf("memory edge must not be nil")
	}
	validToValue := nullableString("")
	if to, ok := edge.ValidTo().Value(); ok {
		validToValue = nullableString(formatMemoryValidityTimestamp(to))
	}
	query := insertMemoryEdgeQuery
	if policy == usecase.BundleConflictReplace {
		query = `INSERT INTO memory_edges (id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  from_memory_id = excluded.from_memory_id,
  to_memory_id = excluded.to_memory_id,
  relation_type = excluded.relation_type,
  valid_from = excluded.valid_from,
  valid_to = excluded.valid_to,
  created_at = excluded.created_at`
	}
	_, err := t.tx.ExecContext(
		ctx,
		query,
		edge.EdgeID().String(),
		edge.FromMemoryID().String(),
		edge.ToMemoryID().String(),
		edge.RelationType().String(),
		formatMemoryValidityTimestamp(edge.ValidFrom()),
		validToValue,
		edge.CreatedAt().UTC().Format(time.RFC3339Nano),
	)
	if err == nil {
		return true, nil
	}
	if policy == usecase.BundleConflictSkip && isSQLiteUniqueOrPKConflict(err) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to import memory edge %s: %w", edge.EdgeID(), err)
}

func (t *bundleImportTx) MemoryExists(ctx context.Context, memoryID types.MemoryID) (bool, error) {
	var value int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM memories WHERE id = ?`, memoryID.String()).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check memory conflict %s: %w", memoryID, err)
}

func (t *bundleImportTx) MemoryEdgeExists(ctx context.Context, edgeID types.MemoryEdgeID) (bool, error) {
	var value int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM memory_edges WHERE id = ?`, edgeID.String()).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check memory edge conflict %s: %w", edgeID, err)
}

func (t *bundleImportTx) sessionExists(ctx context.Context, sessionID types.SessionID) (bool, error) {
	var value int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE session_id = ?`, sessionID.String()).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check session conflict %s: %w", sessionID, err)
}

func (t *bundleImportTx) commandAuditExists(ctx context.Context, eventID types.EventID) (bool, error) {
	var value int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM command_audits WHERE event_id = ?`, eventID.String()).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check command audit conflict %s: %w", eventID, err)
}

func (t *bundleImportTx) backfillParentSession(ctx context.Context, child *model.Session) error {
	parent := model.SessionOf(
		child.ParentSessionID(),
		child.StartedAt(),
		types.None[time.Time](),
		child.Client(),
		child.Agent(),
		child.Workspace(),
		bundleBackfilledParentSessionLabel,
		"Backfilled by Traceary bundle import because the child session referenced a missing parent.",
		"",
	)
	return t.upsertSession(ctx, parent)
}

func (t *bundleImportTx) upsertSession(ctx context.Context, session *model.Session) error {
	var parentSessionID *string
	if session.ParentSessionID().String() != "" {
		v := session.ParentSessionID().String()
		parentSessionID = &v
	}
	var spawnEventID *string
	if session.SpawnEventID().String() != "" {
		v := session.SpawnEventID().String()
		spawnEventID = &v
	}
	var endedAt *string
	if value, ok := session.EndedAt().Value(); ok {
		v := formatTimestamp(value)
		endedAt = &v
	}
	var spawnOrder *int
	if value, ok := session.SpawnOrder().Value(); ok {
		spawnOrder = &value
	}
	_, err := t.tx.ExecContext(ctx, `
INSERT INTO sessions (
  session_id, started_at, ended_at, client, agent, workspace, label, summary,
  parent_session_id, spawn_event_id, subagent_kind, spawn_order
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  started_at = excluded.started_at,
  ended_at = excluded.ended_at,
  client = excluded.client,
  agent = excluded.agent,
  workspace = excluded.workspace,
  label = excluded.label,
  summary = excluded.summary,
  parent_session_id = excluded.parent_session_id,
  spawn_event_id = excluded.spawn_event_id,
  subagent_kind = excluded.subagent_kind,
  spawn_order = excluded.spawn_order`,
		session.SessionID().String(),
		formatTimestamp(session.StartedAt()),
		endedAt,
		session.Client().String(),
		session.Agent().String(),
		session.Workspace().String(),
		session.Label(),
		session.Summary(),
		parentSessionID,
		spawnEventID,
		session.SubagentKind(),
		spawnOrder,
	)
	if err != nil {
		if isSQLiteForeignKeyConflict(err) && session.ParentSessionID().String() != "" {
			return xerrors.Errorf("parent session not found: %s", session.ParentSessionID())
		}
		return xerrors.Errorf("failed to import session %s: %w", session.SessionID(), err)
	}
	return nil
}

func (t *bundleImportTx) Commit(context.Context) error {
	if err := t.tx.Commit(); err != nil {
		_ = t.db.Close()
		return xerrors.Errorf("failed to commit bundle import transaction: %w", err)
	}
	if err := t.db.Close(); err != nil {
		return xerrors.Errorf("failed to close DB after bundle import: %w", err)
	}
	return nil
}

func (t *bundleImportTx) Rollback(context.Context) error {
	rollbackErr := t.tx.Rollback()
	closeErr := t.db.Close()
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return xerrors.Errorf("failed to rollback bundle import transaction: %w", rollbackErr)
	}
	if closeErr != nil {
		return xerrors.Errorf("failed to close DB after bundle import rollback: %w", closeErr)
	}
	return nil
}

// isSQLiteUniqueOrPKConflict reports whether err is a
// modernc.org/sqlite typed error whose Code() identifies a
// constraint violation the bundle usecase should treat as
// "duplicate, skip". We match on the typed error's Code() rather
// than the Error() message so a future driver upgrade that changes
// the text cannot silently flip the behaviour from "skip" to
// "fail".
func isSQLiteUniqueOrPKConflict(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlitelib.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqliteCodePrimaryKeyConflict || code == sqliteCodeUniqueConflict
}

func isSQLiteForeignKeyConflict(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlitelib.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqliteCodeForeignKeyConflict
}

func scanBundleSession(row interface {
	Scan(dest ...any) error
}) (*model.Session, error) {
	var (
		sessionID       string
		startedAtValue  string
		endedAtValue    sql.NullString
		clientValue     string
		agentValue      string
		workspaceValue  string
		labelValue      string
		summaryValue    string
		parentSessionID string
		spawnEventID    string
		subagentKind    string
		spawnOrder      sql.NullInt64
	)
	if err := row.Scan(
		&sessionID,
		&startedAtValue,
		&endedAtValue,
		&clientValue,
		&agentValue,
		&workspaceValue,
		&labelValue,
		&summaryValue,
		&parentSessionID,
		&spawnEventID,
		&subagentKind,
		&spawnOrder,
	); err != nil {
		return nil, xerrors.Errorf("scan session: %w", err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, startedAtValue)
	if err != nil {
		return nil, xerrors.Errorf("started_at: %w", err)
	}
	endedAt := types.None[time.Time]()
	if endedAtValue.Valid {
		value, err := time.Parse(time.RFC3339Nano, endedAtValue.String)
		if err != nil {
			return nil, xerrors.Errorf("ended_at: %w", err)
		}
		endedAt = types.Some(value)
	}
	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		return nil, xerrors.Errorf("agent: %w", err)
	}
	return model.SessionOf(
		types.SessionID(sessionID),
		startedAt,
		endedAt,
		types.Client(clientValue),
		agent,
		types.Workspace(workspaceValue),
		labelValue,
		summaryValue,
		types.SessionID(parentSessionID),
		types.EventID(spawnEventID),
		subagentKind,
		optionalIntFromNullInt64(spawnOrder),
	), nil
}

func scanBundleCommandAudit(row interface {
	Scan(dest ...any) error
}) (*model.CommandAudit, error) {
	var (
		eventID         string
		commandText     string
		inputText       string
		outputText      string
		inputTruncated  bool
		outputTruncated bool
		inputOriginal   int
		outputOriginal  int
		exitCode        sql.NullInt64
		failed          sql.NullBool
	)
	if err := row.Scan(
		&eventID,
		&commandText,
		&inputText,
		&outputText,
		&inputTruncated,
		&outputTruncated,
		&inputOriginal,
		&outputOriginal,
		&exitCode,
		&failed,
	); err != nil {
		return nil, xerrors.Errorf("scan command audit: %w", err)
	}
	audit := model.CommandAuditOf(
		types.EventID(eventID),
		commandText,
		inputText,
		outputText,
		inputTruncated,
		outputTruncated,
		optionalIntFromNullInt64(exitCode),
		failed.Bool,
	)
	audit.SetOriginalPayloadBytes(inputOriginal, outputOriginal)
	return audit, nil
}

// ensure sql import stays referenced; datasource uses it indirectly
// through Database.open().
var _ = sql.ErrNoRows
