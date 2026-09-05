package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

const decodePayloadPageRows = 500

// decodeWALCheckpointHook is an optional test sink fired after each successful
// candidate WAL TRUNCATE during decode.
var decodeWALCheckpointHook func()

func pendingHasDecodePayloads(plan PreparedMigrationPlan) bool {
	for _, migration := range plan.Pending {
		if conservationLawFor(migration.Version) == ConservationLawDecodePayloadsDropCodec {
			return true
		}
	}
	return false
}

type decodeLane struct {
	Table       string
	Field       string
	Column      string
	CodecPrefix string
	PKColumn    string
}

func decodeLanes() []decodeLane {
	return []decodeLane{
		{Table: "events", Field: "body", Column: "body", CodecPrefix: "body", PKColumn: "id"},
		{Table: "command_audits", Field: "command", Column: "command_text", CodecPrefix: "command", PKColumn: "event_id"},
		{Table: "command_audits", Field: "input", Column: "input_text", CodecPrefix: "input", PKColumn: "event_id"},
		{Table: "command_audits", Field: "output", Column: "output_text", CodecPrefix: "output", PKColumn: "event_id"},
	}
}

func decodePayloadsForMigrationOrRefuse(ctx context.Context, db *sql.DB) error {
	hasMeta, err := tableHasColumn(ctx, db, "events", "body"+"_"+"codec")
	if err != nil {
		return err
	}
	if !hasMeta {
		return nil
	}
	for _, lane := range decodeLanes() {
		if err := decodeLaneRows(ctx, db, lane); err != nil {
			return err
		}
	}
	// Bound WAL before catalog apply / VACUUM. The upgrade recipe sets
	// wal_autocheckpoint=0, so decode writes would otherwise grow the
	// candidate WAL without limit (same TRUNCATE as prepared_migration_recipe).
	return checkpointDecodeCandidateWAL(ctx, db)
}

func decodeLaneRows(ctx context.Context, db *sql.DB, lane decodeLane) error {
	codecCol := lane.CodecPrefix + "_" + "codec"
	versionCol := lane.CodecPrefix + "_" + "format_version"
	plainCol := lane.CodecPrefix + "_" + "plaintext_bytes"
	storedCol := lane.CodecPrefix + "_" + "encoded_bytes"
	shaCol := lane.CodecPrefix + "_" + "sha256"
	query := `SELECT ` + quoteIdentifier(lane.PKColumn) + `, ` + quoteIdentifier(lane.Column) + `, ` +
		quoteIdentifier(codecCol) + `, ` + quoteIdentifier(versionCol) + `, ` +
		quoteIdentifier(plainCol) + `, ` + quoteIdentifier(storedCol) + `, ` + quoteIdentifier(shaCol) +
		` FROM ` + quoteIdentifier(lane.Table) +
		` WHERE ` + quoteIdentifier(lane.PKColumn) + ` > ? ORDER BY ` + quoteIdentifier(lane.PKColumn) + ` LIMIT ?`
	update := `UPDATE ` + quoteIdentifier(lane.Table) + ` SET ` + quoteIdentifier(lane.Column) + ` = ?, ` +
		quoteIdentifier(codecCol) + ` = NULL, ` +
		quoteIdentifier(versionCol) + ` = NULL, ` +
		quoteIdentifier(plainCol) + ` = NULL, ` +
		quoteIdentifier(storedCol) + ` = NULL, ` +
		quoteIdentifier(shaCol) + ` = NULL WHERE ` + quoteIdentifier(lane.PKColumn) + ` = ?`
	after := ""
	for {
		page, err := db.QueryContext(ctx, query, after, decodePayloadPageRows)
		if err != nil {
			return fmt.Errorf("scan %s.%s for decode: %w", lane.Table, lane.Field, err)
		}
		type pendingUpdate struct {
			pk    string
			plain string
		}
		var updates []pendingUpdate
		n := 0
		lastPK := after
		for page.Next() {
			var pk string
			var row payloadRow
			if err := page.Scan(&pk, &row.Stored, &row.Codec, &row.FormatVersion, &row.PlaintextBytes, &row.StoredBytes, &row.SHA256); err != nil {
				_ = page.Close()
				return fmt.Errorf("scan %s.%s row: %w", lane.Table, lane.Field, err)
			}
			plain, err := row.decode(maxDecodedPayloadBytes)
			if err != nil {
				_ = page.Close()
				return decodeRefused(lane.Field, pk, err)
			}
			if row.Codec.Valid && row.Codec.String != payloadCodecIdentity {
				updates = append(updates, pendingUpdate{pk: pk, plain: string(plain)})
			}
			lastPK = pk
			n++
		}
		if err := page.Err(); err != nil {
			_ = page.Close()
			return fmt.Errorf("iterate %s.%s: %w", lane.Table, lane.Field, err)
		}
		if err := page.Close(); err != nil {
			return fmt.Errorf("close %s.%s: %w", lane.Table, lane.Field, err)
		}
		if len(updates) > 0 {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin %s.%s decode page: %w", lane.Table, lane.Field, err)
			}
			for _, item := range updates {
				if _, err := tx.ExecContext(ctx, update, item.plain, item.pk); err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("write decoded %s.%s %s: %w", lane.Table, lane.Field, item.pk, err)
				}
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit %s.%s decode page: %w", lane.Table, lane.Field, err)
			}
			if err := checkpointDecodeCandidateWAL(ctx, db); err != nil {
				return err
			}
		}
		if n < decodePayloadPageRows {
			return nil
		}
		after = lastPK
	}
}

// checkpointDecodeCandidateWAL folds the current WAL into the candidate and
// truncates it. PASSIVE would leave the WAL file growing; TRUNCATE matches
// prepared_migration_recipe.Build and compaction_sqlite.checkpointSQLite.
func checkpointDecodeCandidateWAL(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint decode candidate WAL: %w", err)
	}
	if decodeWALCheckpointHook != nil {
		decodeWALCheckpointHook()
	}
	return nil
}

func decodeRefused(lane, rowID string, err error) error {
	reason := "payload decode failed"
	var integrity *PayloadIntegrityError
	if errors.As(err, &integrity) && integrity.Reason != "" {
		reason = integrity.Reason
	} else if err != nil {
		reason = err.Error()
	}
	return &apptypes.PayloadDecodeRefusedError{Lane: lane, RowID: rowID, Reason: reason}
}
