package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// PageMetadataInspector opens a store mode=ro (not immutable, not
// coordinated) and reads only PRAGMA page_count/page_size/freelist_count.
type PageMetadataInspector struct{}

// NewPageMetadataInspector creates a path-based O(1) page-metadata reader.
func NewPageMetadataInspector() *PageMetadataInspector {
	return &PageMetadataInspector{}
}

var _ application.PageMetadataInspector = (*PageMetadataInspector)(nil)

// InspectPageMetadata returns pragma page accounting. An invalid or
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
	return meta, nil
}
