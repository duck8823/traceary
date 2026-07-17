package application

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// ArchiveTableData is a named table payload for store archive packages.
type ArchiveTableData struct {
	Name       string
	PrimaryKey []string
	Rows       []map[string]any
}

// StoreArchiver selects, deletes, and restores cold rows for archive-before-GC.
// Implemented by the SQLite store manager; optional for lightweight test stubs.
type StoreArchiver interface {
	// ListArchiveEligible returns full row maps for GC-eligible records.
	ListArchiveEligible(ctx context.Context, before time.Time, target apptypes.GarbageCollectionTarget) ([]ArchiveTableData, error)
	// DeleteArchiveRows deletes exact primary-key sets in FK-safe order.
	// idsByTable maps table name → composite id strings (NUL-separated for multi-column PKs).
	DeleteArchiveRows(ctx context.Context, idsByTable map[string][]string) (int, error)
	// RestoreArchiveRows inserts rows idempotently. Conflicts (same PK, different content) are counted.
	RestoreArchiveRows(ctx context.Context, tables []ArchiveTableData, dryRun bool) (inserted, skipped, conflicts int, err error)
}
