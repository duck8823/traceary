package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/select_recent_command_previews.sql
var selectRecentCommandPreviewsQuery string

var _ queryservice.EventPreviewQueryService = (*EventDatasource)(nil)

// ListRecentCommandPreviews returns bounded command bodies for summary generation.
func (d *EventDatasource) ListRecentCommandPreviews(ctx context.Context, sessionID types.SessionID, limit, bodyRuneLimit int) ([]apptypes.EventBodyPreview, error) {
	if limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if bodyRuneLimit <= 0 {
		return nil, xerrors.Errorf("body rune limit must be greater than or equal to 1")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for command previews: %w", err)
	}
	defer closeMetadataResource(db)
	rows, err := db.QueryContext(ctx, selectRecentCommandPreviewsQuery, bodyRuneLimit, sessionID.String(), sessionID.String(), limit)
	if err != nil {
		return nil, xerrors.Errorf("failed to query recent command previews: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close command preview rows", "error", err)
		}
	}()

	previews := make([]apptypes.EventBodyPreview, 0, limit)
	for rows.Next() {
		preview, err := scanEventBodyPreview(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore command preview row: %w", err)
		}
		previews = append(previews, preview)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate command preview rows: %w", err)
	}
	return previews, nil
}

func scanEventBodyPreview(row interface{ Scan(...any) error }) (apptypes.EventBodyPreview, error) {
	var id, body, createdAtValue string
	var stored, original sql.NullInt64
	var ingest, storage sql.NullBool
	if err := row.Scan(&id, &body, &stored, &original, &ingest, &storage, &createdAtValue); err != nil {
		return apptypes.EventBodyPreview{}, xerrors.Errorf("failed to scan event body preview: %w", err)
	}
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		return apptypes.EventBodyPreview{}, xerrors.Errorf("failed to restore preview event ID: %w", err)
	}
	if !stored.Valid {
		return apptypes.EventBodyPreview{}, xerrors.Errorf("stored body bytes are missing for event %s", eventID)
	}
	storedBytes, err := checkedInt(stored.Int64, "stored body bytes")
	if err != nil {
		return apptypes.EventBodyPreview{}, err
	}
	originalBytes, err := optionalInt(original, "original body bytes")
	if err != nil {
		return apptypes.EventBodyPreview{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return apptypes.EventBodyPreview{}, xerrors.Errorf("failed to restore preview created_at: %w", err)
	}
	preview, err := apptypes.EventBodyPreviewOf(eventID, body, storedBytes, originalBytes, optionalBool(ingest), optionalBool(storage), createdAt)
	if err != nil {
		return apptypes.EventBodyPreview{}, xerrors.Errorf("failed to restore event body preview: %w", err)
	}
	return preview, nil
}
