package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
)

// SemanticVerifierDropEncodedPayloads is the Layer-2 verifier for offline
// migration 82 (owner #2323).
const SemanticVerifierDropEncodedPayloads SemanticVerifierID = "drop_encoded_payloads"

const droppedEncodedPayloadsReaderVersion = 38

var survivingEventsTriggerNames = []string{
	"event_metadata_projection_events_after_insert",
	"event_metadata_projection_events_after_update",
	"event_metadata_projection_events_after_delete",
	"events_body_metadata_after_insert",
	"events_body_metadata_after_body_update",
	"events_created_at_norm_after_insert",
	"events_created_at_norm_after_update",
	"events_id_immutable",
}

var survivingAuditTriggerNames = []string{
	"event_metadata_projection_command_audits_after_insert",
	"event_metadata_projection_command_audits_after_update",
	"event_metadata_projection_command_audits_after_delete",
}

var survivingEventsIndexNames = []string{
	"idx_events_raw_body_retention_candidates",
	"idx_events_created_at_norm_id_desc",
	"idx_events_workspace_created_at_norm_id_desc",
	"idx_events_session_created_at_norm_id_desc",
	"idx_events_workspace_session_created_at_norm_id_desc",
	"idx_events_source_hook_created_at_norm_id_desc",
}

func verifyDropEncodedPayloads(ctx context.Context, sourceDB, candidateDB *sql.DB, candidatePath string) error {
	if err := verifyEncodedPayloadColumnsAbsent(ctx, candidateDB); err != nil {
		return err
	}
	if err := verifyPayloadFamilyTablesAbsent(ctx, candidateDB); err != nil {
		return err
	}
	if err := verifyEncodedPayloadsReaderState(ctx, candidateDB); err != nil {
		return err
	}
	if err := verifyEncodedPayloadRowCounts(ctx, sourceDB, candidateDB); err != nil {
		return err
	}
	if err := verifyPlaintextLaneConservation(ctx, sourceDB, candidateDB); err != nil {
		return err
	}
	if err := verifyNoPayloadFamilyReferences(ctx, candidateDB); err != nil {
		return err
	}
	if err := verifyEncodedPayloadSurvivors(ctx, candidateDB); err != nil {
		return err
	}
	return verifyEncodedPayloadWriteProbe(ctx, candidatePath)
}

func verifyEncodedPayloadColumnsAbsent(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"events", "command_audits"} {
		columns, err := tableColumns(ctx, db, table)
		if err != nil {
			return err
		}
		for _, name := range columns {
			if strings.Contains(name, "codec") {
				return fmt.Errorf("candidate %s still has column %s", table, name)
			}
		}
	}
	return nil
}

func verifyPayloadFamilyTablesAbsent(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'payload_%'`)
	if err != nil {
		return fmt.Errorf("list candidate payload family tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan payload family table: %w", err)
		}
		return fmt.Errorf("candidate still has table %s", name)
	}
	return rows.Err()
}

func verifyEncodedPayloadsReaderState(ctx context.Context, db *sql.DB) error {
	var minimumReader int
	if err := db.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if minimumReader != droppedEncodedPayloadsReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want %d", minimumReader, droppedEncodedPayloadsReaderVersion)
	}
	columns, err := tableColumns(ctx, db, "store_format_state")
	if err != nil {
		return err
	}
	for _, name := range columns {
		if name == "maximum_payload_format" {
			return fmt.Errorf("candidate store_format_state still has maximum_payload_format")
		}
	}
	return nil
}

func verifyEncodedPayloadRowCounts(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	sourceEvents, err := countTableRows(ctx, sourceDB, "events")
	if err != nil {
		return err
	}
	candidateEvents, err := countTableRows(ctx, candidateDB, "events")
	if err != nil {
		return err
	}
	archiveCount, _, err := sourceArchiveInventory(ctx, sourceDB)
	if err != nil {
		return err
	}
	if candidateEvents != sourceEvents+archiveCount {
		return fmt.Errorf("candidate events count=%d, want source %d + archive %d", candidateEvents, sourceEvents, archiveCount)
	}
	sourceAudits, err := countTableRows(ctx, sourceDB, "command_audits")
	if err != nil {
		return err
	}
	candidateAudits, err := countTableRows(ctx, candidateDB, "command_audits")
	if err != nil {
		return err
	}
	if sourceAudits != candidateAudits {
		return fmt.Errorf("candidate command_audits count=%d, want source %d", candidateAudits, sourceAudits)
	}
	return nil
}

func verifyPlaintextLaneConservation(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	hasMeta, err := tableHasColumn(ctx, sourceDB, "events", "body"+"_"+"codec")
	if err != nil {
		return err
	}
	_, archiveIDs, err := sourceArchiveInventory(ctx, sourceDB)
	if err != nil {
		return err
	}
	for _, lane := range decodeLanes() {
		srcCount, srcDigest, err := digestLanePlaintext(ctx, sourceDB, lane, hasMeta, nil)
		if err != nil {
			return fmt.Errorf("digest source %s.%s: %w", lane.Table, lane.Field, err)
		}
		var skip []string
		if lane.Table == "events" {
			skip = archiveIDs
		}
		candCount, candDigest, err := digestLanePlaintext(ctx, candidateDB, lane, false, skip)
		if err != nil {
			return fmt.Errorf("digest candidate %s.%s: %w", lane.Table, lane.Field, err)
		}
		if srcCount != candCount || srcDigest != candDigest {
			return fmt.Errorf("candidate %s.%s plaintext diverged from decoded source", lane.Table, lane.Field)
		}
	}
	return nil
}

func digestLanePlaintext(ctx context.Context, db *sql.DB, lane decodeLane, decode bool, skipIDs []string) (uint64, [32]byte, error) {
	exists, err := tableExists(ctx, db, lane.Table)
	if err != nil {
		return 0, [32]byte{}, err
	}
	if !exists {
		return 0, [32]byte{}, nil
	}
	var query string
	if decode {
		query = `SELECT ` + quoteIdentifier(lane.PKColumn) + `, ` + quoteIdentifier(lane.Column) + `, ` +
			quoteIdentifier(lane.CodecPrefix+"_"+"codec") + `, ` +
			quoteIdentifier(lane.CodecPrefix+"_"+"format_version") + `, ` +
			quoteIdentifier(lane.CodecPrefix+"_"+"plaintext_bytes") + `, ` +
			quoteIdentifier(lane.CodecPrefix+"_"+"encoded_bytes") + `, ` +
			quoteIdentifier(lane.CodecPrefix+"_"+"sha256") +
			` FROM ` + quoteIdentifier(lane.Table) + ` ORDER BY ` + quoteIdentifier(lane.PKColumn)
	} else {
		query = `SELECT ` + quoteIdentifier(lane.PKColumn) + `, ` + quoteIdentifier(lane.Column) +
			` FROM ` + quoteIdentifier(lane.Table) + ` ORDER BY ` + quoteIdentifier(lane.PKColumn)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, [32]byte{}, err
	}
	defer func() { _ = rows.Close() }()
	skip := make(map[string]struct{}, len(skipIDs))
	for _, id := range skipIDs {
		skip[id] = struct{}{}
	}
	var count uint64
	var xor [32]byte
	for rows.Next() {
		var pk string
		var plain []byte
		if decode {
			var row payloadRow
			if err := rows.Scan(&pk, &row.Stored, &row.Codec, &row.FormatVersion, &row.PlaintextBytes, &row.StoredBytes, &row.SHA256); err != nil {
				return 0, [32]byte{}, err
			}
			plain, err = row.decode(maxDecodedPayloadBytes)
			if err != nil {
				return 0, [32]byte{}, err
			}
		} else {
			var stored []byte
			if err := rows.Scan(&pk, &stored); err != nil {
				return 0, [32]byte{}, err
			}
			plain = stored
		}
		if _, skipped := skip[pk]; skipped {
			continue
		}
		sum := sha256.Sum256(plain)
		count++
		for i := range xor {
			xor[i] ^= sum[i]
		}
	}
	if err := rows.Err(); err != nil {
		return 0, [32]byte{}, err
	}
	return count, xor, nil
}

func verifyNoPayloadFamilyReferences(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master WHERE type IN ('trigger','view') AND sql IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("list candidate triggers and views: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var objectType, name, sqlText string
		if err := rows.Scan(&objectType, &name, &sqlText); err != nil {
			return fmt.Errorf("scan candidate trigger or view: %w", err)
		}
		if strings.Contains(sqlText, "payload_") {
			return fmt.Errorf("candidate %s %s still references dropped payload family", objectType, name)
		}
		if strings.Contains(sqlText, "_codec") {
			return fmt.Errorf("candidate %s %s still references dropped codec columns", objectType, name)
		}
	}
	return rows.Err()
}

func verifyEncodedPayloadSurvivors(ctx context.Context, db *sql.DB) error {
	wantTriggers := make(map[string]struct{}, len(survivingEventsTriggerNames)+len(survivingAuditTriggerNames))
	for _, name := range survivingEventsTriggerNames {
		wantTriggers[name] = struct{}{}
	}
	for _, name := range survivingAuditTriggerNames {
		wantTriggers[name] = struct{}{}
	}
	rows, err := db.QueryContext(ctx, `SELECT name, tbl_name, sql FROM sqlite_master WHERE type='trigger' AND tbl_name IN ('events','command_audits')`)
	if err != nil {
		return fmt.Errorf("list candidate events/audit triggers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	gotTriggers := map[string]struct{}{}
	for rows.Next() {
		var name, table, sqlText string
		if err := rows.Scan(&name, &table, &sqlText); err != nil {
			return fmt.Errorf("scan candidate trigger: %w", err)
		}
		gotTriggers[name] = struct{}{}
		if strings.Contains(sqlText, "_codec") {
			return fmt.Errorf("candidate trigger %s still references codec columns", name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate candidate triggers: %w", err)
	}
	if len(gotTriggers) != len(wantTriggers) {
		return fmt.Errorf("candidate events/audit trigger count = %d, want %d", len(gotTriggers), len(wantTriggers))
	}
	for name := range wantTriggers {
		if _, ok := gotTriggers[name]; !ok {
			return fmt.Errorf("candidate missing trigger %s", name)
		}
	}
	wantIndexes := make(map[string]struct{}, len(survivingEventsIndexNames))
	for _, name := range survivingEventsIndexNames {
		wantIndexes[name] = struct{}{}
	}
	idxRows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='events' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("list candidate events indexes: %w", err)
	}
	defer func() { _ = idxRows.Close() }()
	gotIndexes := map[string]struct{}{}
	for idxRows.Next() {
		var name string
		if err := idxRows.Scan(&name); err != nil {
			return fmt.Errorf("scan candidate index: %w", err)
		}
		gotIndexes[name] = struct{}{}
	}
	if err := idxRows.Err(); err != nil {
		return fmt.Errorf("iterate candidate indexes: %w", err)
	}
	for name := range wantIndexes {
		if _, ok := gotIndexes[name]; !ok {
			return fmt.Errorf("candidate missing index %s", name)
		}
	}
	return nil
}

func verifyEncodedPayloadWriteProbe(ctx context.Context, candidatePath string) error {
	db, err := sql.Open("sqlite", directSQLiteRWDSN(candidatePath))
	if err != nil {
		return fmt.Errorf("open candidate for write probe: %w", err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write probe: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	const probeID = "v82-write-probe"
	const probeBody = "hello"
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
VALUES (?, 'note', 'c', 'a', 's', 'w', ?, '2026-01-01T00:00:00.000000000Z', NULL)`, probeID, probeBody); err != nil {
		return fmt.Errorf("write probe insert event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO command_audits(event_id, command_text, command_wrapper, command_name, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed, failure_reason)
VALUES (?, 'cmd', '', 'unknown', '', '', 0, 0, 0, 0, NULL, 0, 'unknown')`, probeID); err != nil {
		return fmt.Errorf("write probe insert audit: %w", err)
	}
	var stored int
	if err := tx.QueryRowContext(ctx, `SELECT body_stored_bytes FROM event_metadata_projection WHERE id = ?`, probeID).Scan(&stored); err != nil {
		return fmt.Errorf("write probe projection: %w", err)
	}
	if stored != len(probeBody) {
		return fmt.Errorf("write probe body_stored_bytes = %d, want %d", stored, len(probeBody))
	}
	return nil
}
