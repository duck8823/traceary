package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// PageMetadataInspector opens a store mode=ro (not immutable, not
// coordinated) and reads only PRAGMA page_count/page_size/freelist_count
// plus the search_projection_state singleton.
type PageMetadataInspector struct{}

// NewPageMetadataInspector creates a path-based O(1) page-metadata reader.
func NewPageMetadataInspector() *PageMetadataInspector {
	return &PageMetadataInspector{}
}

var _ application.PageMetadataInspector = (*PageMetadataInspector)(nil)

// InspectPageMetadata returns pragma page accounting. A missing projection
// singleton is not an error: ProjectionPresent stays false. An invalid or
// unreadable store returns an error so the caller can fail-soft.
func (PageMetadataInspector) InspectPageMetadata(ctx context.Context, dbPath string) (_ apptypes.StorePageMetadata, err error) {
	db, err := sql.Open("sqlite", sqliteO1ReadOnlyDSN(dbPath))
	if err != nil {
		return apptypes.StorePageMetadata{}, xerrors.Errorf("open store for O(1) page metadata: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("close O(1) page metadata inspection: %w", closeErr)
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return apptypes.StorePageMetadata{}, xerrors.Errorf("ping store for O(1) page metadata: %w", err)
	}
	var meta apptypes.StorePageMetadata
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&meta.PageSizeBytes); err != nil {
		return apptypes.StorePageMetadata{}, xerrors.Errorf("read page_size: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&meta.PageCount); err != nil {
		return apptypes.StorePageMetadata{}, xerrors.Errorf("read page_count: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&meta.FreePages); err != nil {
		return apptypes.StorePageMetadata{}, xerrors.Errorf("read freelist_count: %w", err)
	}
	meta.DatabaseBytes = meta.PageSizeBytes * meta.PageCount
	meta.ReclaimableBytes = meta.PageSizeBytes * meta.FreePages
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(active_generation_id,''), COALESCE(state,''), COALESCE(phase,'') FROM search_projection_state WHERE singleton=1`).Scan(
		&meta.ProjectionGenerationID,
		&meta.ProjectionState,
		&meta.ProjectionPhase,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isMissingProjectionStateTable(err) {
			return meta, nil
		}
		return apptypes.StorePageMetadata{}, xerrors.Errorf("read search_projection_state singleton: %w", err)
	}
	meta.ProjectionPresent = true
	return meta, nil
}

func isMissingProjectionStateTable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") && strings.Contains(message, "search_projection_state")
}
