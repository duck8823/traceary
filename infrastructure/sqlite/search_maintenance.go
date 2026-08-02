//nolint:revive,wrapcheck // SQLite transition errors are already scoped by the operation boundary.
package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func (d *Database) SearchRetirementSnapshot(ctx context.Context) (apptypes.SearchRetirementSnapshot, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchRetirementSnapshot{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return apptypes.SearchRetirementSnapshot{}, xerrors.Errorf("begin retirement snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	out, err := readSearchRetirementSnapshot(ctx, tx)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, xerrors.Errorf("finish retirement snapshot: %w", err)
	}
	return out, nil
}

func readSearchRetirementSnapshot(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (apptypes.SearchRetirementSnapshot, error) {
	var out apptypes.SearchRetirementSnapshot
	var authority, phase string
	var progress int64
	var adopted int
	if err := q.QueryRowContext(ctx, `SELECT authority,phase,progress,target_adopted FROM search_maintenance_control WHERE singleton=1`).Scan(&authority, &phase, &progress, &adopted); err != nil {
		return out, xerrors.Errorf("read search maintenance state: %w", err)
	}
	state, err := model.SearchMaintenanceOf(model.SearchAuthority(authority), model.SearchMaintenancePhase(phase), progress)
	if err != nil {
		return out, err
	}
	out.State = state
	out.TargetAdopted = adopted != 0
	if err := q.QueryRowContext(ctx, `SELECT generation_id,query_revision,high_water,state,cursor_key FROM literal_search_projection_state WHERE singleton=1`).Scan(&out.ProjectionGeneration, &out.ProjectionRevision, &out.ProjectionHighWater, &out.ProjectionState, &out.CursorKey); err != nil {
		return out, xerrors.Errorf("read literal projection snapshot: %w", err)
	}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&out.SourceHighWater); err != nil {
		return out, xerrors.Errorf("read source high-water: %w", err)
	}
	if err := q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM events),(SELECT COUNT(*) FROM command_audits),COALESCE((SELECT SUM(COALESCE(body_plaintext_bytes,length(body),0)) FROM events),0)+COALESCE((SELECT SUM(COALESCE(command_plaintext_bytes,length(command_text),0)+COALESCE(input_plaintext_bytes,length(input_text),0)+COALESCE(output_plaintext_bytes,length(output_text),0)) FROM command_audits),0)`).Scan(&out.EventCount, &out.AuditCount, &out.CanonicalLogicalBytes); err != nil {
		return out, xerrors.Errorf("read canonical search aggregate: %w", err)
	}
	var pages, size int64
	if err := q.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return out, err
	}
	if err := q.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return out, err
	}
	out.PhysicalBytes = pages * size
	return out, nil
}

func (d *Database) AdoptSearchRetirementTarget(ctx context.Context) (apptypes.SearchMaintenanceReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE search_maintenance_control SET target_adopted=1,evidence_binding='',updated_at=? WHERE singleton=1 AND authority='legacy' AND phase='active'`, formatTimestamp(time.Now().UTC()))
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("target adoption requires legacy/active state")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE literal_search_projection_state SET cursor_key=randomblob(32),state='stale',query_revision=query_revision+1,updated_at=? WHERE singleton=1`, formatTimestamp(time.Now().UTC())); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	return d.SearchMaintenanceStatus(ctx)
}

func (d *Database) BeginSearchRetirement(ctx context.Context, binding string, expected apptypes.SearchRetirementSnapshot) (apptypes.SearchMaintenanceReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	fresh, err := readSearchRetirementSnapshot(ctx, tx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if fresh.State.Authority() != model.SearchAuthorityLegacy || fresh.State.Phase() != model.SearchMaintenanceActive || fresh.ProjectionState != "complete" || fresh.ProjectionRevision != expected.ProjectionRevision || fresh.ProjectionHighWater != fresh.SourceHighWater || fresh.SourceHighWater != expected.SourceHighWater || string(fresh.CursorKey) != string(expected.CursorKey) {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("retirement snapshot changed before authority switch")
	}
	if !fresh.TargetAdopted || !expected.TargetAdopted {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("retirement target has not been explicitly adopted")
	}
	logical, err := legacySearchLogicalBytes(ctx, tx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	// Stop new legacy plaintext duplication atomically with the authority switch.
	for _, name := range []string{"events_search_after_insert", "events_search_after_body_update", "command_audits_search_after_insert", "command_audits_search_after_update", "command_audits_search_after_delete"} {
		if _, err = tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name); err != nil {
			return apptypes.SearchMaintenanceReport{}, xerrors.Errorf("disable legacy search writer: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE search_maintenance_control SET authority='tiered',phase='retiring',progress=0,evidence_binding=?,logical_bytes_before=?,physical_bytes_before=?,updated_at=? WHERE singleton=1 AND authority='legacy' AND phase='active'`, binding, logical, fresh.PhysicalBytes, formatTimestamp(time.Now().UTC()))
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	return d.SearchMaintenanceStatus(ctx)
}

func (d *Database) RetireLegacySearchBatch(ctx context.Context, rows int) (apptypes.SearchMaintenanceReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var authority, phase string
	var progress int64
	if err = tx.QueryRowContext(ctx, `SELECT authority,phase,progress FROM search_maintenance_control WHERE singleton=1`).Scan(&authority, &phase, &progress); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if authority != "tiered" || phase != "retiring" {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("legacy search retirement is not in progress")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM event_search_documents WHERE search_document_id IN (SELECT search_document_id FROM event_search_documents ORDER BY search_document_id LIMIT ?)`, rows)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, xerrors.Errorf("retire legacy search batch: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	progress += deleted
	var remaining int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents`).Scan(&remaining); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	logical, err := legacySearchLogicalBytes(ctx, tx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	physical, err := databasePhysicalBytes(ctx, tx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	next := "retiring"
	if remaining == 0 {
		next = "retired"
		if err = dropLegacySearchProjection(ctx, tx); err != nil {
			return apptypes.SearchMaintenanceReport{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE search_maintenance_control SET phase=?,progress=?,logical_bytes_after=?,physical_bytes_after=?,updated_at=? WHERE singleton=1`, next, progress, logical, physical, formatTimestamp(time.Now().UTC())); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	report, err := d.SearchMaintenanceStatus(ctx)
	report.RowsProcessed = deleted
	return report, err
}

func (d *Database) BeginSearchRestore(ctx context.Context) (apptypes.SearchMaintenanceReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = createLegacySearchProjection(ctx, tx); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE search_maintenance_control SET phase='restoring',progress=0,transition_revision=(SELECT query_revision FROM literal_search_projection_state WHERE singleton=1),updated_at=? WHERE singleton=1 AND authority='tiered' AND phase IN('retired','retiring','restoring')`, formatTimestamp(time.Now().UTC()))
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("search restore requires tiered retired or retiring state")
	}
	if err = tx.Commit(); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	return d.SearchMaintenanceStatus(ctx)
}

func dropLegacySearchProjection(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{"DROP VIEW IF EXISTS event_search_projection", "DROP TABLE IF EXISTS event_search_fts", "DROP TABLE IF EXISTS event_search_documents", "DROP TABLE IF EXISTS event_search_backfill_state"} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return xerrors.Errorf("drop retired legacy search projection: %w", err)
		}
	}
	return nil
}

func createLegacySearchProjection(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS event_search_documents(search_document_id INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,body_text TEXT NOT NULL DEFAULT '',command_text TEXT NOT NULL DEFAULT '',input_text TEXT NOT NULL DEFAULT '',output_text TEXT NOT NULL DEFAULT '')`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS event_search_fts USING fts5(body_text,command_text,input_text,output_text,content='event_search_documents',content_rowid='search_document_id',tokenize='trigram case_sensitive 1')`,
		`CREATE VIEW IF NOT EXISTS event_search_projection AS SELECT e.id event_id,lower(CASE WHEN e.body_availability='unavailable_retention' THEN '' ELSE e.body END) body_text,lower(COALESCE(a.command_text,'')) command_text,lower(COALESCE(a.input_text,'')) input_text,lower(COALESCE(a.output_text,'')) output_text FROM events e LEFT JOIN command_audits a ON a.event_id=e.id`,
		`CREATE TRIGGER IF NOT EXISTS event_search_documents_after_insert AFTER INSERT ON event_search_documents BEGIN INSERT INTO event_search_fts(rowid,body_text,command_text,input_text,output_text) VALUES(NEW.search_document_id,NEW.body_text,NEW.command_text,NEW.input_text,NEW.output_text); END`,
		`CREATE TRIGGER IF NOT EXISTS event_search_documents_after_delete AFTER DELETE ON event_search_documents BEGIN INSERT INTO event_search_fts(event_search_fts,rowid,body_text,command_text,input_text,output_text) VALUES('delete',OLD.search_document_id,OLD.body_text,OLD.command_text,OLD.input_text,OLD.output_text); END`,
		`CREATE TRIGGER IF NOT EXISTS event_search_documents_after_update AFTER UPDATE OF body_text,command_text,input_text,output_text ON event_search_documents BEGIN INSERT INTO event_search_fts(event_search_fts,rowid,body_text,command_text,input_text,output_text) VALUES('delete',OLD.search_document_id,OLD.body_text,OLD.command_text,OLD.input_text,OLD.output_text); INSERT INTO event_search_fts(rowid,body_text,command_text,input_text,output_text) VALUES(NEW.search_document_id,NEW.body_text,NEW.command_text,NEW.input_text,NEW.output_text); END`,
		`CREATE TABLE IF NOT EXISTS event_search_backfill_state(singleton INTEGER PRIMARY KEY CHECK(singleton=1),last_event_id TEXT NOT NULL DEFAULT '',target_event_id TEXT,completed INTEGER NOT NULL DEFAULT 0 CHECK(completed IN(0,1)),updated_at TEXT NOT NULL)`,
		`INSERT OR IGNORE INTO event_search_backfill_state(singleton,last_event_id,completed,updated_at) VALUES(1,'',0,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return xerrors.Errorf("create legacy search projection for restore: %w", err)
		}
	}
	return nil
}

func (d *Database) RestoreLegacySearchBatch(ctx context.Context, limit int) (apptypes.SearchMaintenanceReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var authority, phase string
	var progress, transitionRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT authority,phase,progress,transition_revision FROM search_maintenance_control WHERE singleton=1`).Scan(&authority, &phase, &progress, &transitionRevision); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if authority != "tiered" || phase != "restoring" {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("legacy search restore is not in progress")
	}
	var currentRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT query_revision FROM literal_search_projection_state WHERE singleton=1`).Scan(&currentRevision); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if currentRevision != transitionRevision {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("canonical history changed during legacy search restore; restart restore")
	}
	rows, err := tx.QueryContext(ctx, `SELECT rowid,id,body_availability FROM events WHERE rowid>? ORDER BY rowid LIMIT ?`, progress, limit)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	type item struct {
		rowid        int64
		id           string
		availability string
	}
	batch := []item{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.rowid, &x.id, &x.availability); err != nil {
			_ = rows.Close()
			return apptypes.SearchMaintenanceReport{}, err
		}
		batch = append(batch, x)
	}
	if err = rows.Close(); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	for _, x := range batch {
		plain, e := loadEventPlaintext(ctx, tx, x.id)
		if e != nil {
			return apptypes.SearchMaintenanceReport{}, e
		}
		body, _ := visibleEventBody(string(plain), domtypes.BodyAvailability(x.availability))
		command, e := hydrateAuditPayload(ctx, tx, x.id, "command")
		if e != nil {
			return apptypes.SearchMaintenanceReport{}, e
		}
		input, e := hydrateAuditPayload(ctx, tx, x.id, "input")
		if e != nil {
			return apptypes.SearchMaintenanceReport{}, e
		}
		output, e := hydrateAuditPayload(ctx, tx, x.id, "output")
		if e != nil {
			return apptypes.SearchMaintenanceReport{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO event_search_documents(event_id,body_text,command_text,input_text,output_text) VALUES(?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET body_text=excluded.body_text,command_text=excluded.command_text,input_text=excluded.input_text,output_text=excluded.output_text`, x.id, lowerSearchASCII(body), lowerSearchASCII(command.String), lowerSearchASCII(input.String), lowerSearchASCII(output.String)); e != nil {
			return apptypes.SearchMaintenanceReport{}, e
		}
		progress = x.rowid
	}
	complete := len(batch) < limit
	if complete {
		if err = createLegacySearchWriterTriggers(ctx, tx); err != nil {
			return apptypes.SearchMaintenanceReport{}, err
		}
	}
	logical, err := legacySearchLogicalBytes(ctx, tx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	physical, err := databasePhysicalBytes(ctx, tx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	nextAuthority, nextPhase := "tiered", "restoring"
	if complete {
		nextAuthority, nextPhase = "legacy", "active"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE search_maintenance_control SET authority=?,phase=?,progress=?,logical_bytes_after=?,physical_bytes_after=?,updated_at=? WHERE singleton=1`, nextAuthority, nextPhase, progress, logical, physical, formatTimestamp(time.Now().UTC())); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	report, err := d.SearchMaintenanceStatus(ctx)
	report.RowsProcessed = int64(len(batch))
	return report, err
}

func createLegacySearchWriterTriggers(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS events_search_after_insert AFTER INSERT ON events BEGIN INSERT INTO event_search_documents(event_id,body_text,command_text,input_text,output_text) SELECT event_id,body_text,command_text,input_text,output_text FROM event_search_projection WHERE event_id=NEW.id ON CONFLICT(event_id) DO NOTHING; END`,
		`CREATE TRIGGER IF NOT EXISTS events_search_after_body_update AFTER UPDATE OF body,body_availability ON events BEGIN INSERT INTO event_search_documents(event_id,body_text,command_text,input_text,output_text) SELECT event_id,body_text,command_text,input_text,output_text FROM event_search_projection WHERE event_id=NEW.id ON CONFLICT(event_id) DO UPDATE SET body_text=excluded.body_text,command_text=excluded.command_text,input_text=excluded.input_text,output_text=excluded.output_text; END`,
		`CREATE TRIGGER IF NOT EXISTS command_audits_search_after_insert AFTER INSERT ON command_audits BEGIN INSERT INTO event_search_documents(event_id,body_text,command_text,input_text,output_text) SELECT event_id,body_text,command_text,input_text,output_text FROM event_search_projection WHERE event_id=NEW.event_id ON CONFLICT(event_id) DO UPDATE SET body_text=excluded.body_text,command_text=excluded.command_text,input_text=excluded.input_text,output_text=excluded.output_text; END`,
		`CREATE TRIGGER IF NOT EXISTS command_audits_search_after_update AFTER UPDATE OF command_text,input_text,output_text ON command_audits BEGIN UPDATE event_search_documents SET (body_text,command_text,input_text,output_text)=(SELECT body_text,command_text,input_text,output_text FROM event_search_projection WHERE event_id=NEW.event_id) WHERE event_id=NEW.event_id; END`,
		`CREATE TRIGGER IF NOT EXISTS command_audits_search_after_delete AFTER DELETE ON command_audits BEGIN UPDATE event_search_documents SET (body_text,command_text,input_text,output_text)=(SELECT body_text,command_text,input_text,output_text FROM event_search_projection WHERE event_id=OLD.event_id) WHERE event_id=OLD.event_id; END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return xerrors.Errorf("restore legacy search writer: %w", err)
		}
	}
	return nil
}

func (d *Database) SearchMaintenanceStatus(ctx context.Context) (apptypes.SearchMaintenanceReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	defer d.release(db)
	var r apptypes.SearchMaintenanceReport
	var authority, phase string
	if err = db.QueryRowContext(ctx, `SELECT authority,phase,progress,logical_bytes_before,logical_bytes_after,physical_bytes_before,physical_bytes_after FROM search_maintenance_control WHERE singleton=1`).Scan(&authority, &phase, &r.Progress, &r.LogicalBytesBefore, &r.LogicalBytesAfter, &r.PhysicalBytesBefore, &r.PhysicalBytesAfter); err != nil {
		return r, err
	}
	r.Authority = model.SearchAuthority(authority)
	r.Phase = model.SearchMaintenancePhase(phase)
	r.Complete = phase == "active" || phase == "retired"
	return r, nil
}
func legacySearchLogicalBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(body_text AS BLOB))+length(CAST(command_text AS BLOB))+length(CAST(input_text AS BLOB))+length(CAST(output_text AS BLOB))),0) FROM event_search_documents`).Scan(&n)
	return n, err
}
func databasePhysicalBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var p, s int64
	if err := q.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&p); err != nil {
		return 0, err
	}
	if err := q.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&s); err != nil {
		return 0, err
	}
	return p * s, nil
}

var _ interface {
	SearchRetirementSnapshot(context.Context) (apptypes.SearchRetirementSnapshot, error)
} = (*Database)(nil)

func lowerSearchASCII(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}
