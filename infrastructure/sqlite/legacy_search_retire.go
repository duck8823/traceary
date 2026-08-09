//nolint:revive,wrapcheck // SQLite retirement errors are scoped by the operation boundary.
package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// legacySearchFamilyTables are the large migration-032 objects removed by
// store search-retire. Migration 052 already dropped writers/control.
var legacySearchFamilyTables = []string{
	"event_search_documents",
	"event_search_fts",
	"event_search_backfill_state",
}

// RetireLegacySearchProjection drops the migration-032 family in one
// transaction when present. It is idempotent: an already-removed family
// reports already_removed and exits successfully without mutation.
func (d *Database) RetireLegacySearchProjection(ctx context.Context) (apptypes.LegacySearchRetireReport, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, xerrors.Errorf("begin legacy search retirement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	present, err := legacySearchFamilyPresent(ctx, tx)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	if !present {
		physical, physErr := databasePhysicalBytes(ctx, tx)
		if physErr != nil {
			return apptypes.LegacySearchRetireReport{}, physErr
		}
		if err = tx.Commit(); err != nil {
			return apptypes.LegacySearchRetireReport{}, xerrors.Errorf("finish already-removed legacy search check: %w", err)
		}
		return apptypes.LegacySearchRetireReport{
			AlreadyRemoved:                true,
			PhysicalBytesBefore:           physical,
			PhysicalBytesAfter:            physical,
			FileSizeUnchangedUntilCompact: true,
		}, nil
	}

	logicalBefore, err := legacySearchLogicalBytes(ctx, tx)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	physicalBefore, err := databasePhysicalBytes(ctx, tx)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	// Straight DROP — do not DELETE rows first. FTS5 records deletions as
	// appended delete markers, so emptying the content table grows the index.
	if err = dropLegacySearchProjection(ctx, tx); err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	logicalAfter, err := legacySearchLogicalBytes(ctx, tx)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	physicalAfter, err := databasePhysicalBytes(ctx, tx)
	if err != nil {
		return apptypes.LegacySearchRetireReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.LegacySearchRetireReport{}, xerrors.Errorf("commit legacy search retirement: %w", err)
	}
	return apptypes.LegacySearchRetireReport{
		AlreadyRemoved:                false,
		LogicalBytesBefore:            logicalBefore,
		LogicalBytesAfter:             logicalAfter,
		PhysicalBytesBefore:           physicalBefore,
		PhysicalBytesAfter:            physicalAfter,
		FileSizeUnchangedUntilCompact: true,
	}, nil
}

func dropLegacySearchProjection(ctx context.Context, tx *sql.Tx) error {
	// DROP order: view first (if any remain on pre-052 stores), then FTS, then
	// content table, then backfill state. Table DROP removes their triggers.
	for _, statement := range []string{
		"DROP VIEW IF EXISTS event_search_projection",
		"DROP TABLE IF EXISTS event_search_fts",
		"DROP TABLE IF EXISTS event_search_documents",
		"DROP TABLE IF EXISTS event_search_backfill_state",
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return xerrors.Errorf("drop retired legacy search projection: %w", err)
		}
	}
	return nil
}

func legacySearchFamilyPresent(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	for _, name := range legacySearchFamilyTables {
		var exists int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type IN ('table','view') AND name=?)`, name).Scan(&exists); err != nil {
			return false, xerrors.Errorf("inspect legacy search family object %s: %w", name, err)
		}
		if exists != 0 {
			return true, nil
		}
	}
	return false, nil
}

// legacySearchLogicalBytes reports plaintext bytes stored in the content table,
// or zero when the table is already gone.
func legacySearchLogicalBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='event_search_documents')`).Scan(&exists); err != nil {
		return 0, xerrors.Errorf("inspect event_search_documents presence: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}
	var n int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(body_text AS BLOB))+length(CAST(command_text AS BLOB))+length(CAST(input_text AS BLOB))+length(CAST(output_text AS BLOB))),0) FROM event_search_documents`).Scan(&n); err != nil {
		return 0, xerrors.Errorf("measure legacy search logical bytes: %w", err)
	}
	return n, nil
}

func databasePhysicalBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var p, s int64
	if err := q.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&p); err != nil {
		return 0, xerrors.Errorf("read page_count: %w", err)
	}
	if err := q.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&s); err != nil {
		return 0, xerrors.Errorf("read page_size: %w", err)
	}
	return p * s, nil
}

// rejectLegacySearchFamilyForCompaction stops a plan that would copy multi-GiB
// dead index data into the candidate. It runs before the digest, so it saves
// hashing the whole source as well.
//
// A source that cannot be read as a database is not this check's business:
// apply time opens it properly and VerifyStoreCompatibility reports that far
// more precisely. Only a store that demonstrably still carries the family is
// rejected here.
func rejectLegacySearchFamilyForCompaction(ctx context.Context, path string) error {
	db, err := openDirectReadOnly(ctx, path)
	if err != nil {
		return nil //nolint:nilerr // unreadable sources are diagnosed at apply time, not here
	}
	defer func() { _ = db.Close() }()
	present, err := legacySearchFamilyPresent(ctx, db)
	if err != nil {
		return nil //nolint:nilerr // same: an unreadable schema is not evidence of the family
	}
	if present {
		return xerrors.New("legacy search index family is still present; run `traceary store search-retire` before compact plan/apply")
	}
	return nil
}
