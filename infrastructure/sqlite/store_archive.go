package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// ListArchiveEligible implements application.StoreArchiver.
func (d *StoreManagementDatasource) ListArchiveEligible(
	ctx context.Context,
	before time.Time,
	target apptypes.GarbageCollectionTarget,
) ([]application.ArchiveTableData, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for archive list: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	beforeValue := formatTimestamp(before)
	memoryEdgeBefore := formatMemoryValidityTimestamp(before)
	var tables []application.ArchiveTableData

	if target == apptypes.GarbageCollectionTargetEvents || target == apptypes.GarbageCollectionTargetAll {
		events, err := queryArchiveMaps(ctx, db, `
SELECT id, kind, client, agent, session_id, workspace, body, created_at, source_hook
  FROM events
 WHERE created_at < ?
 ORDER BY id`, beforeValue)
		if err != nil {
			return nil, xerrors.Errorf("list archive events: %w", err)
		}
		tables = append(tables, application.ArchiveTableData{
			Name: "events", PrimaryKey: []string{"id"}, Rows: events,
		})
		if len(events) > 0 {
			ids := make([]string, 0, len(events))
			for _, row := range events {
				if id, ok := row["id"].(string); ok {
					ids = append(ids, id)
				}
			}
			audits, err := queryArchiveMapsIn(ctx, db, `
SELECT event_id, command_text, command_wrapper, command_name, input_text, output_text, input_truncated, output_truncated,
       input_original_bytes, output_original_bytes, exit_code, failed, failure_reason
  FROM command_audits
 WHERE event_id IN (%s)
 ORDER BY event_id`, ids)
			if err != nil {
				return nil, xerrors.Errorf("list archive command_audits: %w", err)
			}
			tables = append(tables, application.ArchiveTableData{
				Name: "command_audits", PrimaryKey: []string{"event_id"}, Rows: audits,
			})
		}
	}

	if target == apptypes.GarbageCollectionTargetSessions || target == apptypes.GarbageCollectionTargetAll {
		sessions, err := queryArchiveMaps(ctx, db, `
SELECT session_id, started_at, ended_at, client, agent, workspace, label, summary,
       parent_session_id, spawn_event_id, subagent_kind, spawn_order, model
  FROM sessions
 WHERE ended_at IS NOT NULL
   AND COALESCE(ended_at, started_at) < ?
   AND NOT EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.session_id)
   AND NOT EXISTS (SELECT 1 FROM sessions AS child_sessions WHERE child_sessions.parent_session_id = sessions.session_id)
 ORDER BY session_id`, beforeValue)
		if err != nil {
			return nil, xerrors.Errorf("list archive sessions: %w", err)
		}
		tables = append(tables, application.ArchiveTableData{
			Name: "sessions", PrimaryKey: []string{"session_id"}, Rows: sessions,
		})
	}

	if target == apptypes.GarbageCollectionTargetMemories || target == apptypes.GarbageCollectionTargetAll {
		memories, err := queryArchiveMaps(ctx, db, `
SELECT id, type, scope_kind, scope_value, fact, status, confidence, source,
       supersedes_memory_id, expires_at, valid_from, valid_to, created_at, updated_at
  FROM memories
 WHERE status IN ('expired', 'superseded', 'rejected')
   AND updated_at < ?
 ORDER BY id`, beforeValue)
		if err != nil {
			return nil, xerrors.Errorf("list archive memories: %w", err)
		}
		tables = append(tables, application.ArchiveTableData{
			Name: "memories", PrimaryKey: []string{"id"}, Rows: memories,
		})
		if len(memories) > 0 {
			ids := make([]string, 0, len(memories))
			for _, row := range memories {
				if id, ok := row["id"].(string); ok {
					ids = append(ids, id)
				}
			}
			ev, err := queryArchiveMapsIn(ctx, db, `
SELECT memory_id, ordinal, ref_kind, ref_value
  FROM memory_evidence_refs
 WHERE memory_id IN (%s)
 ORDER BY memory_id, ordinal`, ids)
			if err != nil {
				return nil, xerrors.Errorf("list archive memory_evidence_refs: %w", err)
			}
			tables = append(tables, application.ArchiveTableData{
				Name: "memory_evidence_refs", PrimaryKey: []string{"memory_id", "ordinal"}, Rows: ev,
			})
			ar, err := queryArchiveMapsIn(ctx, db, `
SELECT memory_id, ordinal, ref_kind, ref_value
  FROM memory_artifact_refs
 WHERE memory_id IN (%s)
 ORDER BY memory_id, ordinal`, ids)
			if err != nil {
				return nil, xerrors.Errorf("list archive memory_artifact_refs: %w", err)
			}
			tables = append(tables, application.ArchiveTableData{
				Name: "memory_artifact_refs", PrimaryKey: []string{"memory_id", "ordinal"}, Rows: ar,
			})
		}
	}

	if target == apptypes.GarbageCollectionTargetMemoryEdges || target == apptypes.GarbageCollectionTargetAll {
		edges, err := queryArchiveMaps(ctx, db, `
SELECT id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at
  FROM memory_edges
 WHERE valid_to IS NOT NULL AND valid_to < ?
 ORDER BY id`, memoryEdgeBefore)
		if err != nil {
			return nil, xerrors.Errorf("list archive memory_edges: %w", err)
		}
		tables = append(tables, application.ArchiveTableData{
			Name: "memory_edges", PrimaryKey: []string{"id"}, Rows: edges,
		})
	}

	return tables, nil
}

// DeleteArchiveRows implements application.StoreArchiver.
func (d *StoreManagementDatasource) DeleteArchiveRows(ctx context.Context, idsByTable map[string][]string) (int, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for archive delete: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, xerrors.Errorf("begin archive delete: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	total := 0
	// FK-safe order: children before parents.
	order := []struct {
		table string
		col   string
	}{
		{"memory_edges", "id"},
		{"memory_evidence_refs", "memory_id"}, // composite handled specially
		{"memory_artifact_refs", "memory_id"},
		{"command_audits", "event_id"},
		{"events", "id"},
		{"sessions", "session_id"},
		{"memories", "id"},
	}
	for _, step := range order {
		ids := idsByTable[step.table]
		if len(ids) == 0 {
			continue
		}
		switch step.table {
		case "memory_evidence_refs", "memory_artifact_refs":
			// Composite PK encoded as memory_id\x00ordinal
			for _, id := range ids {
				parts := strings.SplitN(id, "\x00", 2)
				if len(parts) != 2 {
					return 0, xerrors.Errorf("invalid composite id for %s: %q", step.table, id)
				}
				res, err := tx.ExecContext(ctx, `DELETE FROM `+step.table+` WHERE memory_id = ? AND ordinal = ?`, parts[0], parts[1])
				if err != nil {
					return 0, xerrors.Errorf("delete %s: %w", step.table, err)
				}
				n, _ := res.RowsAffected()
				total += int(n)
			}
		default:
			// Clear supersedes pointers for memories being deleted.
			if step.table == "memories" {
				if err := execDeleteIn(ctx, tx, `
UPDATE memories SET supersedes_memory_id = NULL
 WHERE supersedes_memory_id IN (%s)`, ids); err != nil {
					return 0, xerrors.Errorf("clear supersedes before archive delete: %w", err)
				}
			}
			n, err := execDeleteInCount(ctx, tx, `DELETE FROM `+step.table+` WHERE `+step.col+` IN (%s)`, ids)
			if err != nil {
				return 0, xerrors.Errorf("delete %s: %w", step.table, err)
			}
			total += n
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, xerrors.Errorf("commit archive delete: %w", err)
	}
	committed = true
	if total > 0 {
		if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
			return 0, xerrors.Errorf("failed to run VACUUM after archive delete: %w", err)
		}
	}
	return total, nil
}

// RestoreArchiveRows implements application.StoreArchiver.
func (d *StoreManagementDatasource) RestoreArchiveRows(
	ctx context.Context,
	tables []application.ArchiveTableData,
	dryRun bool,
) (inserted, skipped, conflicts int, err error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, 0, 0, xerrors.Errorf("failed to open DB for archive restore: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	// Restore parents before children.
	order := []string{"events", "command_audits", "sessions", "memories", "memory_evidence_refs", "memory_artifact_refs", "memory_edges"}
	byName := map[string]application.ArchiveTableData{}
	for _, t := range tables {
		byName[t.Name] = t
	}

	if dryRun {
		for _, name := range order {
			t, ok := byName[name]
			if !ok {
				continue
			}
			for _, row := range t.Rows {
				exists, same, err := archiveRowExists(ctx, db, t, row)
				if err != nil {
					return 0, 0, 0, err
				}
				switch {
				case !exists:
					inserted++
				case same:
					skipped++
				default:
					conflicts++
				}
			}
		}
		return inserted, skipped, conflicts, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, xerrors.Errorf("begin archive restore: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, name := range order {
		t, ok := byName[name]
		if !ok {
			continue
		}
		for _, row := range t.Rows {
			exists, same, err := archiveRowExistsTx(ctx, tx, t, row)
			if err != nil {
				return 0, 0, 0, err
			}
			if exists {
				if same {
					skipped++
					continue
				}
				conflicts++
				continue
			}
			if err := insertArchiveRow(ctx, tx, t.Name, row); err != nil {
				return 0, 0, 0, xerrors.Errorf("insert %s: %w", t.Name, err)
			}
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, xerrors.Errorf("commit archive restore: %w", err)
	}
	committed = true
	return inserted, skipped, conflicts, nil
}

func queryArchiveMaps(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("query archive rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanArchiveMaps(rows)
}

func queryArchiveMapsIn(ctx context.Context, db *sql.DB, queryFmt string, ids []string) ([]map[string]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Stay under SQLite's bind-variable limit (dogfood #1386: large event
	// sets for command_audits blew up unchunked IN lists).
	const chunk = 400
	var out []map[string]any
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		placeholders := make([]string, len(part))
		args := make([]any, len(part))
		for i, id := range part {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(queryFmt, strings.Join(placeholders, ","))
		rows, err := queryArchiveMaps(ctx, db, query, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func scanArchiveMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, xerrors.Errorf("archive columns: %w", err)
	}
	var out []map[string]any
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, xerrors.Errorf("scan archive row: %w", err)
		}
		m := make(map[string]any, len(cols))
		for i, col := range cols {
			m[col] = normalizeSQLValue(raw[i])
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate archive rows: %w", err)
	}
	return out, nil
}

func normalizeSQLValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case int64:
		return t
	case float64:
		return t
	case bool:
		return t
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func execDeleteIn(ctx context.Context, tx *sql.Tx, queryFmt string, ids []string) error {
	_, err := execDeleteInCount(ctx, tx, queryFmt, ids)
	return err
}

func execDeleteInCount(ctx context.Context, tx *sql.Tx, queryFmt string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// Chunk to stay under SQLite variable limits.
	const chunk = 400
	total := 0
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		placeholders := make([]string, len(part))
		args := make([]any, len(part))
		for i, id := range part {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(queryFmt, strings.Join(placeholders, ","))
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return total, xerrors.Errorf("exec archive delete chunk: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

func archiveRowExists(ctx context.Context, db *sql.DB, table application.ArchiveTableData, row map[string]any) (exists, same bool, err error) {
	return archiveRowExistsQuerier(ctx, db, table, row)
}

func archiveRowExistsTx(ctx context.Context, tx *sql.Tx, table application.ArchiveTableData, row map[string]any) (exists, same bool, err error) {
	return archiveRowExistsQuerier(ctx, tx, table, row)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func archiveRowExistsQuerier(ctx context.Context, q queryRower, table application.ArchiveTableData, row map[string]any) (exists, same bool, err error) {
	where := make([]string, 0, len(table.PrimaryKey))
	args := make([]any, 0, len(table.PrimaryKey))
	for _, k := range table.PrimaryKey {
		where = append(where, k+" = ?")
		args = append(args, row[k])
	}
	query := `SELECT 1 FROM ` + table.Name + ` WHERE ` + strings.Join(where, " AND ") + ` LIMIT 1`
	var one int
	err = q.QueryRowContext(ctx, query, args...).Scan(&one)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, xerrors.Errorf("lookup %s: %w", table.Name, err)
	}
	// v1: primary-key presence is treated as an idempotent skip (same).
	// Content-diff conflicts are reserved for a later pass.
	return true, true, nil
}

func insertArchiveRow(ctx context.Context, tx *sql.Tx, table string, row map[string]any) error {
	switch table {
	case "events":
		_, err := tx.ExecContext(ctx, insertEventQuery,
			row["id"], row["kind"], nullStr(row["client"]), row["agent"], row["session_id"],
			nullStr(row["workspace"]), row["body"], row["created_at"], nullStr(row["source_hook"]),
		)
		if err != nil {
			return xerrors.Errorf("insert events: %w", err)
		}
		return nil
	case "command_audits":
		_, err := tx.ExecContext(ctx, insertCommandAuditQuery,
			row["event_id"], row["command_text"], nullStr(row["command_wrapper"]), archiveStringOr(row, "command_name", "unknown"), row["input_text"], row["output_text"],
			asInt(row["input_truncated"]), asInt(row["output_truncated"]),
			row["input_original_bytes"], row["output_original_bytes"],
			row["exit_code"], asInt(row["failed"]), archiveStringOr(row, "failure_reason", "unknown"),
		)
		if err != nil {
			return xerrors.Errorf("insert command_audits: %w", err)
		}
		return nil
	case "sessions":
		_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO sessions (
  session_id, started_at, ended_at, client, agent, workspace, label, summary,
  parent_session_id, spawn_event_id, subagent_kind, spawn_order, model
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row["session_id"], row["started_at"], row["ended_at"], nullStr(row["client"]), nullStr(row["agent"]),
			nullStr(row["workspace"]), nullStr(row["label"]), nullStr(row["summary"]),
			row["parent_session_id"], row["spawn_event_id"], row["subagent_kind"], row["spawn_order"], row["model"],
		)
		if err != nil {
			return xerrors.Errorf("insert sessions: %w", err)
		}
		return nil
	case "memories":
		_, err := tx.ExecContext(ctx, upsertMemoryQuery,
			row["id"], row["type"], row["scope_kind"], row["scope_value"], row["fact"],
			row["status"], row["confidence"], row["source"], row["supersedes_memory_id"],
			row["expires_at"], row["valid_from"], row["valid_to"], row["created_at"], row["updated_at"],
		)
		if err != nil {
			return xerrors.Errorf("insert memories: %w", err)
		}
		return nil
	case "memory_evidence_refs":
		_, err := tx.ExecContext(ctx, `
INSERT INTO memory_evidence_refs (memory_id, ordinal, ref_kind, ref_value) VALUES (?, ?, ?, ?)`,
			row["memory_id"], row["ordinal"], row["ref_kind"], row["ref_value"],
		)
		if err != nil {
			return xerrors.Errorf("insert memory_evidence_refs: %w", err)
		}
		return nil
	case "memory_artifact_refs":
		_, err := tx.ExecContext(ctx, `
INSERT INTO memory_artifact_refs (memory_id, ordinal, ref_kind, ref_value) VALUES (?, ?, ?, ?)`,
			row["memory_id"], row["ordinal"], row["ref_kind"], row["ref_value"],
		)
		if err != nil {
			return xerrors.Errorf("insert memory_artifact_refs: %w", err)
		}
		return nil
	case "memory_edges":
		_, err := tx.ExecContext(ctx, insertMemoryEdgeQuery,
			row["id"], row["from_memory_id"], row["to_memory_id"], row["relation_type"],
			row["valid_from"], row["valid_to"], row["created_at"],
		)
		if err != nil {
			return xerrors.Errorf("insert memory_edges: %w", err)
		}
		return nil
	default:
		return xerrors.Errorf("unsupported archive table %s", table)
	}
}

func archiveStringOr(row map[string]any, key, fallback string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return fallback
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return fallback
	}
	return text
}

func nullStr(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func asInt(v any) any {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case int64:
		return t
	case float64:
		return int64(t)
	default:
		return v
	}
}
