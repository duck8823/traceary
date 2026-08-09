package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestPersistedTieredAuthorityBroadQueryKeepsResultLimitSeparateFromCoverage(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("broad-%03d", i)
		at := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES(?,'note','codex','codex','s','w','broad needle',?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_projection_state SET generation_id='g',active_generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete',phase='complete'; UPDATE search_maintenance_control SET authority='tiered',phase='retired'`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	datasource := NewEventDatasource(database)
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build()
	metadata, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil || len(metadata) != 1 || metadata[0].EventID().String() != "broad-100" {
		t.Fatalf("broad metadata=%v err=%v", metadata, err)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil || len(full) != 1 || full[0].EventID().String() != "broad-100" {
		t.Fatalf("broad full=%v err=%v", eventIDs(full), err)
	}
	bounded, err := datasource.SearchBounded(ctx, criteria, 32)
	if err != nil || len(bounded) != 1 || bounded[0].Metadata().EventID().String() != "broad-100" {
		t.Fatalf("broad bounded=%v err=%v", bounded, err)
	}
	anchor, err := apptypes.EventPageAnchorOf(full[0].CreatedAt(), full[0].EventID())
	if err != nil {
		t.Fatal(err)
	}
	next, err := datasource.SearchPage(ctx, apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").PageAnchor(anchor).Build())
	if err != nil || len(next) != 1 || next[0].EventID().String() != "broad-099" {
		t.Fatalf("broad continuation=%v err=%v", eventIDs(next), err)
	}
	for name, invalid := range map[string]apptypes.EventSearchCriteria{
		"max limit":  apptypes.NewEventSearchCriteriaBuilder(int(^uint(0) >> 1)).Query("needle").Build(),
		"max offset": apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Offset(int(^uint(0) >> 1)).Build(),
	} {
		t.Run(name, func(t *testing.T) {
			checks := []func() error{
				func() error { _, checkErr := datasource.SearchPage(ctx, invalid); return checkErr },
				func() error { _, checkErr := datasource.SearchMetadata(ctx, invalid); return checkErr },
				func() error { _, checkErr := datasource.SearchBounded(ctx, invalid, 32); return checkErr },
			}
			for i, check := range checks {
				if checkErr := check(); checkErr == nil {
					t.Fatalf("surface %d accepted an unbounded search window", i)
				}
			}
		})
	}
}

func TestRestoredLegacyWriterDecodesCanonicalEnvelopeAndAuditPayloads(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE search_maintenance_control SET authority='tiered',phase='retired'; DROP VIEW event_search_projection; DROP TABLE event_search_fts; DROP TABLE event_search_documents; DROP TABLE event_search_backfill_state`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err = database.BeginSearchRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RestoreLegacySearchBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}

	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.release(raw)
	body := mustEncodedSearchPayload(t, `{"blocks":[{"type":"text","text":"Envelope Needle"}]}`, payloadCodecZstd)
	if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256) VALUES('encoded','note','codex','codex','s','w',?,'2026-01-01T00:00:00Z',?,?,?,?,?)`, body.Bytes, body.Codec, body.FormatVersion, body.PlaintextBytes, body.StoredBytes, body.SHA256); err != nil {
		t.Fatal(err)
	}
	command := mustEncodedSearchPayload(t, "Echo Audit Needle", payloadCodecZstd)
	input := mustEncodedSearchPayload(t, "Input Needle", payloadCodecZstd)
	output := mustEncodedSearchPayload(t, "Output Needle", payloadCodecZstd)
	if _, err = raw.ExecContext(ctx, `INSERT INTO command_audits(event_id,command_text,input_text,output_text,command_codec,command_format_version,command_plaintext_bytes,command_encoded_bytes,command_sha256,input_codec,input_format_version,input_plaintext_bytes,input_encoded_bytes,input_sha256,output_codec,output_format_version,output_plaintext_bytes,output_encoded_bytes,output_sha256) VALUES('encoded',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, command.Bytes, input.Bytes, output.Bytes, command.Codec, command.FormatVersion, command.PlaintextBytes, command.StoredBytes, command.SHA256, input.Codec, input.FormatVersion, input.PlaintextBytes, input.StoredBytes, input.SHA256, output.Codec, output.FormatVersion, output.PlaintextBytes, output.StoredBytes, output.SHA256); err != nil {
		t.Fatal(err)
	}
	assertLegacySearchDocument(t, raw, "encoded", "envelope needle", "echo audit needle", "input needle", "output needle")

	updated := mustEncodedSearchPayload(t, "Updated Audit Marker", payloadCodecZstd)
	if _, err = raw.ExecContext(ctx, `UPDATE command_audits SET command_text=?,command_codec=?,command_format_version=?,command_plaintext_bytes=?,command_encoded_bytes=?,command_sha256=? WHERE event_id='encoded'`, updated.Bytes, updated.Codec, updated.FormatVersion, updated.PlaintextBytes, updated.StoredBytes, updated.SHA256); err != nil {
		t.Fatal(err)
	}
	assertLegacySearchDocument(t, raw, "encoded", "envelope needle", "updated audit marker", "input needle", "output needle")
	if _, err = raw.ExecContext(ctx, `DELETE FROM command_audits WHERE event_id='encoded'`); err != nil {
		t.Fatal(err)
	}
	assertLegacySearchDocument(t, raw, "encoded", "envelope needle", "", "", "")
	if _, err = raw.ExecContext(ctx, `DELETE FROM events WHERE id='encoded'`); err != nil {
		t.Fatal(err)
	}
	var documents int
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents WHERE event_id='encoded'`).Scan(&documents); err != nil || documents != 0 {
		t.Fatalf("documents=%d err=%v", documents, err)
	}
}

func mustEncodedSearchPayload(t *testing.T, plaintext, codec string) encodedPayload {
	t.Helper()
	payload, err := encodePayload([]byte(plaintext), codec)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertLegacySearchDocument(t *testing.T, db *sql.DB, id string, want ...string) {
	t.Helper()
	var body, command, input, output string
	if err := db.QueryRow(`SELECT body_text,command_text,input_text,output_text FROM event_search_documents WHERE event_id=?`, id).Scan(&body, &command, &input, &output); err != nil {
		t.Fatal(err)
	}
	got := []string{body, command, input, output}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("document[%d]=%q want %q (all=%q)", i, got[i], want[i], got)
		}
	}
}

func TestSearchMaintenanceRetireRestoreIsBoundedAndResumable(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES(?,'note','codex','codex','s','w',?,?)`, id, "body "+id, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_projection_state SET generation_id='g',active_generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete',phase='complete'; UPDATE search_maintenance_control SET target_adopted=1`); err != nil {
		t.Fatal(err)
	}
	// Authoritative event search reads the bounded projection when complete.
	// Seed recent documents so the concurrent authority snapshot still sees all
	// three events while retirement proceeds.
	for i, id := range []string{"e1", "e2", "e3"} {
		body := "body " + id
		if _, err = raw.ExecContext(ctx, `INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES('g',?,?,?,?,?)`, i+1, id, "2026-01-01T00:00:00.000000000Z", body, len(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	snapshot, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	authorityRead := make(chan struct{})
	releaseRead := make(chan struct{})
	database.searchMaintenanceHook = func(point string) error {
		if point == "authority-after-read" {
			close(authorityRead)
			<-releaseRead
		}
		return nil
	}
	searchDone := make(chan error, 1)
	go func() {
		criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("body").Build()
		found, searchErr := NewEventDatasource(database).SearchMetadata(ctx, criteria)
		if searchErr == nil && len(found) != 3 {
			searchErr = errors.New("authority snapshot returned mixed membership")
		}
		searchDone <- searchErr
	}()
	<-authorityRead
	retireDone := make(chan error, 1)
	go func() {
		_, beginErr := database.BeginSearchRetirement(ctx, retirementEvidence(t, snapshot), snapshot)
		retireDone <- beginErr
	}()
	close(releaseRead)
	if err = <-searchDone; err != nil {
		t.Fatal(err)
	}
	if err = <-retireDone; err != nil {
		t.Fatal(err)
	}
	database.searchMaintenanceHook = nil
	database.searchMaintenanceHook = func(point string) error {
		if point == "retire-before-commit" {
			return errors.New("injected retire commit fault")
		}
		return nil
	}
	if _, err = database.RetireLegacySearchBatch(ctx, 1); err == nil {
		t.Fatal("retire commit fault returned nil")
	}
	database.searchMaintenanceHook = nil
	assertSearchMaintenanceStorage(t, database, "tiered", "retiring", 0, 3)
	first, err := database.RetireLegacySearchBatch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.RowsProcessed != 1 || first.Phase != model.SearchMaintenanceRetiring {
		t.Fatalf("first=%+v", first)
	}
	// Reopening the adapter resumes the persisted cursor rather than guessing
	// from table shape.
	second, err := NewDatabase(database.Path(), database.migrations).RetireLegacySearchBatch(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second.Phase != model.SearchMaintenanceRetired {
		t.Fatalf("second=%+v", second)
	}
	if second.LogicalBytesBefore <= 0 || second.LogicalBytesAfter != 0 || second.PhysicalBytesBefore <= 0 || second.PhysicalBytesAfter <= 0 {
		t.Fatalf("missing before/after byte attribution: %+v", second)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES('e4','note','codex','codex','s','w','new body','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var docs int
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN('event_search_documents','event_search_fts')`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if docs != 0 {
		t.Fatalf("retired legacy structures=%d want=0", docs)
	}
	if _, err = database.BeginSearchRestore(ctx); err != nil {
		t.Fatal(err)
	}
	database.searchMaintenanceHook = func(point string) error {
		if point == "restore-before-commit" {
			return errors.New("injected restore commit fault")
		}
		return nil
	}
	if _, err = database.RestoreLegacySearchBatch(ctx, 2); err == nil {
		t.Fatal("restore commit fault returned nil")
	}
	database.searchMaintenanceHook = nil
	assertSearchMaintenanceStorage(t, database, "tiered", "restoring", 0, 0)
	partial, err := database.RestoreLegacySearchBatch(ctx, 2)
	if err != nil || partial.Complete {
		t.Fatalf("partial restore=%+v err=%v", partial, err)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES('restart-event','note','codex','codex','s','w','restart body','2026-01-02T01:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err = database.RestoreLegacySearchBatch(ctx, 2); err == nil {
		t.Fatal("restore continued after canonical revision changed")
	}
	if _, err = database.BeginSearchRestore(ctx); err != nil {
		t.Fatalf("explicit restore restart: %v", err)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var restartedProgress, restartedDocs int
	if err = raw.QueryRowContext(ctx, `SELECT progress,(SELECT COUNT(*) FROM event_search_documents) FROM search_maintenance_control WHERE singleton=1`).Scan(&restartedProgress, &restartedDocs); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if restartedProgress != 0 || restartedDocs != 0 {
		t.Fatalf("restart progress=%d docs=%d", restartedProgress, restartedDocs)
	}
	var restored apptypes.SearchMaintenanceReport
	for {
		report, restoreErr := database.RestoreLegacySearchBatch(ctx, 2)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		restored = report
		if report.Complete {
			break
		}
	}
	if restored.LogicalBytesAfter <= 0 || restored.PhysicalBytesAfter <= 0 {
		t.Fatalf("missing restore byte attribution: %+v", restored)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 5 {
		t.Fatalf("restored documents=%d want=5", docs)
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES('e5','note','codex','codex','s','w','later','2026-01-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 6 {
		t.Fatalf("restored writer documents=%d", docs)
	}
	var completed int
	if err = raw.QueryRowContext(ctx, `SELECT completed FROM event_search_backfill_state WHERE singleton=1`).Scan(&completed); err != nil || completed != 1 {
		t.Fatalf("backfill completed=%d err=%v", completed, err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE events SET body='updated marker' WHERE id='e5'`); err != nil {
		t.Fatal(err)
	}
	var indexed string
	if err = raw.QueryRowContext(ctx, `SELECT body_text FROM event_search_documents WHERE event_id='e5'`).Scan(&indexed); err != nil || indexed != "updated marker" {
		t.Fatalf("updated document=%q err=%v", indexed, err)
	}
	if _, err = raw.ExecContext(ctx, `DELETE FROM events WHERE id='e5'`); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents`).Scan(&docs); err != nil || docs != 5 {
		t.Fatalf("delete propagation docs=%d err=%v", docs, err)
	}
}

func assertSearchMaintenanceStorage(t *testing.T, database *Database, authority, phase string, progress, documents int64) {
	t.Helper()
	db, err := database.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer database.release(db)
	var gotAuthority, gotPhase string
	var gotProgress, gotDocuments int64
	if err = db.QueryRow(`SELECT authority,phase,progress,(SELECT COUNT(*) FROM event_search_documents) FROM search_maintenance_control WHERE singleton=1`).Scan(&gotAuthority, &gotPhase, &gotProgress, &gotDocuments); err != nil {
		t.Fatal(err)
	}
	if gotAuthority != authority || gotPhase != phase || gotProgress != progress || gotDocuments != documents {
		t.Fatalf("maintenance storage=(%s,%s,%d,%d), want (%s,%s,%d,%d)", gotAuthority, gotPhase, gotProgress, gotDocuments, authority, phase, progress, documents)
	}
}

func TestPersistedTieredAuthorityPreservesDescendingContinuationAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"older", "newer"} {
		at := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES(?,'note','codex','codex','s','w','find needle',?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	_, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_projection_state SET generation_id='g',active_generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete',phase='complete'; UPDATE search_maintenance_control SET authority='tiered',phase='retired'`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	datasource := NewEventDatasource(database)
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build()
	page, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].EventID().String() != "newer" {
		t.Fatalf("first page=%v", eventIDs(page))
	}
	anchor, err := apptypes.EventPageAnchorOf(page[0].CreatedAt(), page[0].EventID())
	if err != nil {
		t.Fatal(err)
	}
	page, err = datasource.SearchPage(ctx, apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").PageAnchor(anchor).Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].EventID().String() != "older" {
		t.Fatalf("continuation=%v", eventIDs(page))
	}
	metadata, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil || len(metadata) != 1 || metadata[0].EventID().String() != "newer" {
		t.Fatalf("tiered metadata=%v err=%v", metadata, err)
	}
	bounded, err := datasource.SearchBounded(ctx, criteria, 100)
	if err != nil || len(bounded) != 1 || bounded[0].Metadata().EventID().String() != "newer" {
		t.Fatalf("tiered bounded=%v err=%v", bounded, err)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP VIEW event_search_projection", "DROP TABLE event_search_fts", "DROP TABLE event_search_documents", "DROP TABLE event_search_backfill_state"} {
		if _, err = raw.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	if _, err = datasource.SearchPage(ctx, criteria); err != nil {
		t.Fatalf("full search referenced retired legacy schema: %v", err)
	}
	if _, err = datasource.SearchMetadata(ctx, criteria); err != nil {
		t.Fatalf("metadata search referenced retired legacy schema: %v", err)
	}
	if _, err = datasource.SearchBounded(ctx, criteria, 100); err != nil {
		t.Fatalf("bounded search referenced retired legacy schema: %v", err)
	}
	workspace, workspaceErr := domtypes.WorkspaceFrom("w")
	if workspaceErr != nil {
		t.Fatal(workspaceErr)
	}
	structural := apptypes.NewEventSearchCriteriaBuilder(2).Workspace(workspace).Build()
	if got, structuralErr := datasource.SearchPage(ctx, structural); structuralErr != nil || len(got) != 2 {
		t.Fatalf("tiered structural full search=%v err=%v", eventIDs(got), structuralErr)
	}
	if got, structuralErr := datasource.SearchMetadata(ctx, structural); structuralErr != nil || len(got) != 2 {
		t.Fatalf("tiered structural metadata search=%v err=%v", got, structuralErr)
	}
	if got, structuralErr := datasource.SearchBounded(ctx, structural, 100); structuralErr != nil || len(got) != 2 {
		t.Fatalf("tiered structural bounded search=%v err=%v", got, structuralErr)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 'stale' is what migration 039 writes on every ordinary append, so it is a
	// tail condition the walk answers, not a "cannot answer" condition. See
	// event_search_authority_internal_test.go for the append and mutation cases.
	_, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET state='stale'`)
	_ = raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = datasource.SearchPage(ctx, criteria); err != nil {
		t.Fatalf("stale literal projection refused a tail-only condition: %v", err)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET state='rebuilding'`)
	_ = raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = datasource.SearchPage(ctx, criteria); err == nil {
		t.Fatal("incomplete tiered projection did not fail closed")
	}
}

func eventIDs(events []*model.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID().String())
	}
	return ids
}

func TestCreateLegacySearchProjectionRepairsPartialSchema(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.release(raw)
	for _, statement := range []string{
		"DROP VIEW event_search_projection",
		"DROP TABLE event_search_fts",
		"DROP TRIGGER event_search_documents_after_insert",
		"DROP TRIGGER event_search_documents_after_update",
		"DROP TRIGGER event_search_documents_after_delete",
		"DROP TRIGGER events_search_after_insert",
		"DROP TRIGGER events_search_after_body_update",
		"DROP TRIGGER command_audits_search_after_insert",
		"DROP TRIGGER command_audits_search_after_update",
		"DROP TRIGGER command_audits_search_after_delete",
	} {
		if _, err = raw.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = createLegacySearchProjection(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{"event_search_projection", "event_search_fts", "event_search_documents_after_insert", "event_search_documents_after_update", "event_search_documents_after_delete", "events_search_after_insert", "events_search_after_body_update", "command_audits_search_after_insert", "command_audits_search_after_update", "command_audits_search_after_delete"} {
		var count int
		if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name=?`, object).Scan(&count); err != nil || count != 1 {
			t.Fatalf("restored object %s count=%d err=%v", object, count, err)
		}
	}
}

func TestSearchMaintenanceRestoreFailureDoesNotSwitchAuthority(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,body_codec,body_plaintext_bytes,body_encoded_bytes) VALUES('bad','note','codex','codex','s','w',X'00','2026-01-01T00:00:00Z','zstd',1,1); UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_projection_state SET generation_id='g',active_generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete',phase='complete'; UPDATE search_maintenance_control SET target_adopted=1`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	snapshot, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.BeginSearchRetirement(ctx, retirementEvidence(t, snapshot), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RetireLegacySearchBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = database.BeginSearchRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RestoreLegacySearchBatch(ctx, 10); err == nil {
		t.Fatal("corrupt canonical payload restored")
	}
	status, err := database.SearchMaintenanceStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Authority != model.SearchAuthorityTiered || status.Phase != model.SearchMaintenanceRestoring {
		t.Fatalf("failure switched authority: %+v", status)
	}
}

func TestMigration40ContainsOnlyAdditiveControlDDL(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "sqlite", "migrations", "000040_add_search_maintenance_control.sql"))
	if err != nil {
		t.Fatal(err)
	}
	normalized := string(data)
	for _, forbidden := range []string{"DROP ", "DELETE ", "VACUUM"} {
		if containsFold(normalized, forbidden) {
			t.Fatalf("migration contains %q", forbidden)
		}
	}
}
func containsFold(value, needle string) bool {
	return len(value) >= len(needle) && indexFold(value, needle) >= 0
}
func indexFold(value, needle string) int {
	for i := 0; i+len(needle) <= len(value); i++ {
		match := true
		for j := range needle {
			a, b := value[i+j], needle[j]
			if a >= 'a' && a <= 'z' {
				a -= 32
			}
			if b >= 'a' && b <= 'z' {
				b -= 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestAdoptSearchRetirementTargetRotatesKeyAndInvalidatesProjection(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.AdoptSearchRetirementTarget(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !after.TargetAdopted || string(before.CursorKey) == string(after.CursorKey) || after.ProjectionState != "stale" {
		t.Fatalf("target adoption did not rotate and invalidate")
	}
}

func retirementEvidence(t *testing.T, snapshot apptypes.SearchRetirementSnapshot) apptypes.SearchParityV2Evidence {
	t.Helper()
	revision := apptypes.SearchParityRevision{Commit: strings.Repeat("a", 40)}
	projection := apptypes.SearchParityProjection{Revision: snapshot.ProjectionRevision, HighWater: snapshot.ProjectionHighWater, LogicalBytes: snapshot.CanonicalLogicalBytes, PhysicalBytes: snapshot.PhysicalBytes}
	binding, err := apptypes.KeyedSearchParityBinding(snapshot.CursorKey, "target-store", apptypes.SearchParityTargetFields(revision, projection, snapshot.EventCount, snapshot.AuditCount, snapshot.CanonicalLogicalBytes, snapshot.ProjectionGeneration, snapshot.ProjectionGeneration)...)
	if err != nil {
		t.Fatal(err)
	}
	evidence := apptypes.SearchParityV2Evidence{SchemaVersion: apptypes.SearchParityV2Schema, AuthorizationScope: "actual_target", TargetStoreBinding: binding, Revision: revision, Projection: projection, LiteralGeneration: snapshot.ProjectionGeneration, BoundedGeneration: snapshot.ProjectionGeneration, RunID: "test-run", ComparisonContract: "membership_set/v1", Criteria: []apptypes.SearchParityCriterion{{QueryClass: "fingerprint_eligible", Status: "passed", ComparisonEqual: true, CoverageComplete: true, LegacyLatencyUS: 1, TieredLatencyUS: 1}, {QueryClass: "bounded_verification", Status: "passed", ComparisonEqual: true, CoverageComplete: true, LegacyLatencyUS: 1, TieredLatencyUS: 1}}}
	for i := range evidence.Criteria {
		evidence.Criteria[i].CriterionBinding, _ = apptypes.KeyedSearchParityBinding(snapshot.CursorKey, "criterion", apptypes.SearchParityCriterionFields(evidence, evidence.Criteria[i])...)
	}
	return evidence
}
