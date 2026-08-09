package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/select_recent_command_previews.sql
var selectRecentCommandPreviewsQuery string

//go:embed sql/select_latest_post_compact_summary.sql
var selectLatestPostCompactSummaryQuery string

//go:embed sql/select_event_by_id.sql
var selectEventByIDQuery string

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
	defer d.db.release(db)
	// bodyRuneLimit is applied after codec hydration; the SQL no longer binds
	// a substr length because it does not select command_text at all.
	rows, err := db.QueryContext(ctx, selectRecentCommandPreviewsQuery, sessionID.String(), sessionID.String(), limit)
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
		meta, err := scanCommandPreviewMetadata(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore command preview row: %w", err)
		}
		// Prefer the retained command_audits.command_text; fall back to the
		// legacy composed events.body for pre-#1675 rows.
		plain, err := loadCommandPreviewPlaintext(ctx, db, meta.eventID.String())
		if err != nil {
			return nil, xerrors.Errorf("decode command preview: %w", err)
		}
		runes := []rune(string(plain))
		if len(runes) > bodyRuneLimit {
			runes = runes[:bodyRuneLimit]
		}
		preview, err := apptypes.EventBodyPreviewOf(meta.eventID, string(runes), meta.storedBytes, meta.originalBytes, meta.ingestTruncated, meta.storageTruncated, meta.createdAt)
		if err != nil {
			return nil, xerrors.Errorf("rebuild decoded command preview: %w", err)
		}
		previews = append(previews, preview)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate command preview rows: %w", err)
	}
	return previews, nil
}

// loadCommandPreviewPlaintext returns the command line used for handoff
// recent-command summaries. command_audits is authoritative after #1675.
func loadCommandPreviewPlaintext(ctx context.Context, q queryRowContexter, eventID string) ([]byte, error) {
	command, err := hydrateAuditPayload(ctx, q, eventID, "command")
	if err != nil {
		return nil, err
	}
	if command.Valid && strings.TrimSpace(command.String) != "" {
		return []byte(command.String), nil
	}
	return loadEventPlaintext(ctx, q, eventID)
}

// commandPreviewMetadata is the non-body columns returned by
// select_recent_command_previews.sql. The command line is hydrated separately.
type commandPreviewMetadata struct {
	eventID          types.EventID
	storedBytes      int
	originalBytes    types.Optional[int]
	ingestTruncated  types.Optional[bool]
	storageTruncated types.Optional[bool]
	createdAt        time.Time
}

func scanCommandPreviewMetadata(row interface{ Scan(...any) error }) (commandPreviewMetadata, error) {
	var id, createdAtValue string
	var stored, original sql.NullInt64
	var ingest, storage sql.NullBool
	if err := row.Scan(&id, &stored, &original, &ingest, &storage, &createdAtValue); err != nil {
		return commandPreviewMetadata{}, xerrors.Errorf("failed to scan command preview metadata: %w", err)
	}
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		return commandPreviewMetadata{}, xerrors.Errorf("failed to restore preview event ID: %w", err)
	}
	if !stored.Valid {
		return commandPreviewMetadata{}, xerrors.Errorf("stored body bytes are missing for event %s", eventID)
	}
	storedBytes, err := checkedInt(stored.Int64, "stored body bytes")
	if err != nil {
		return commandPreviewMetadata{}, err
	}
	originalBytes, err := optionalInt(original, "original body bytes")
	if err != nil {
		return commandPreviewMetadata{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return commandPreviewMetadata{}, xerrors.Errorf("failed to restore preview created_at: %w", err)
	}
	return commandPreviewMetadata{
		eventID:          eventID,
		storedBytes:      storedBytes,
		originalBytes:    originalBytes,
		ingestTruncated:  optionalBool(ingest),
		storageTruncated: optionalBool(storage),
		createdAt:        createdAt,
	}, nil
}

// FindLatestPostCompactSummary pages over body-free candidates and hydrates
// only candidates needed to find the newest usable post-compact summary.
func (d *EventDatasource) FindLatestPostCompactSummary(ctx context.Context, sessionID types.SessionID, workspace types.Workspace) (types.Optional[*model.Event], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.Event](), xerrors.Errorf("failed to open DB for compact summary preview: %w", err)
	}
	defer d.db.release(db)

	const candidateBatchSize = 32
	anchorTime, anchorID := "", ""
	for {
		rows, queryErr := db.QueryContext(ctx, selectLatestPostCompactSummaryQuery,
			sessionID.String(), workspace.String(), workspace.String(),
			anchorTime, anchorTime, anchorTime, anchorID, candidateBatchSize,
		)
		if queryErr != nil {
			return types.None[*model.Event](), xerrors.Errorf("failed to query compact summary candidates: %w", queryErr)
		}
		type candidate struct{ id, createdAtNorm string }
		candidates := make([]candidate, 0, candidateBatchSize)
		for rows.Next() {
			var item candidate
			if scanErr := rows.Scan(&item.id, &item.createdAtNorm); scanErr != nil {
				_ = rows.Close()
				return types.None[*model.Event](), xerrors.Errorf("failed to scan compact summary candidate: %w", scanErr)
			}
			candidates = append(candidates, item)
		}
		iterationErr := rows.Err()
		closeErr := rows.Close()
		if iterationErr != nil {
			return types.None[*model.Event](), xerrors.Errorf("failed to iterate compact summary candidates: %w", iterationErr)
		}
		if closeErr != nil {
			return types.None[*model.Event](), xerrors.Errorf("failed to close compact summary candidates: %w", closeErr)
		}
		for _, item := range candidates {
			event, scanErr := scanEvent(db.QueryRowContext(ctx, selectEventByIDQuery, item.id))
			if scanErr != nil {
				return types.None[*model.Event](), xerrors.Errorf("failed to restore compact summary candidate: %w", scanErr)
			}
			event, scanErr = hydrateEventPayload(ctx, db, event)
			if scanErr != nil {
				return types.None[*model.Event](), scanErr
			}
			body := strings.TrimSpace(event.Body())
			if event.SourceHook() == "pre_compact" || strings.HasPrefix(body, types.EventBodyMarkerCompactPreSnapshot) {
				continue
			}
			return types.Some(event), nil
		}
		if len(candidates) < candidateBatchSize {
			return types.None[*model.Event](), nil
		}
		last := candidates[len(candidates)-1]
		anchorTime, anchorID = last.createdAtNorm, last.id
	}
}
