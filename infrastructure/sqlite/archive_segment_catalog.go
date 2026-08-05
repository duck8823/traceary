//nolint:wrapcheck // This infrastructure boundary preserves causes while adding typed segment errors where relevant.
package sqlite

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

var (
	// ErrSegmentCatalogInvalid rejects registration whose installed segment
	// file cannot be verified against the supplied manifest.
	ErrSegmentCatalogInvalid = errors.New("archive segment manifest is not registrable")
	// ErrSegmentCatalogConflict identifies a same-basename contradiction.
	ErrSegmentCatalogConflict = errors.New("archive segment catalog conflict")
)

const archiveSegmentColumns = `basename, store_id, format_version, start_sequence, end_sequence, unit_count, audit_count, min_created_at, max_created_at, time_complete, plain_value_count, zstd_value_count, total_plain_bytes, total_stored_bytes, logical_digest, file_digest`

// RegisterArchiveSegment verifies the installed sealed segment file and
// records its manifest in the catalog as one operation. Verification re-reads
// the file under root (safe basename, sealed read-only mode, manifest
// equality including the file digest, full payload decode), and the root
// directory entry is fsynced before insertion so a catalog row never
// references an installation that can disappear across a crash. Registering
// the identical manifest again is a no-op, so at-least-once archive
// processing converges; a differing manifest under an already-registered
// basename fails without changing the row. Catalog rows never carry Hot
// authority.
func (d *Database) RegisterArchiveSegment(ctx context.Context, root string, manifest ArchiveSegmentManifest, limits ArchiveSegmentLimits) error {
	if _, err := VerifyArchiveSegment(ctx, root, manifest, limits); err != nil {
		return fmt.Errorf("%w: %w", ErrSegmentCatalogInvalid, err)
	}
	if err := syncArchiveRoot(root); err != nil {
		return err
	}
	db, err := d.open(ctx)
	if err != nil {
		return err
	}
	defer d.release(db)
	if _, err = db.ExecContext(ctx, `INSERT INTO archive_segments(`+archiveSegmentColumns+`)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(basename) DO NOTHING`,
		manifest.Basename, manifest.StoreID, manifest.FormatVersion, manifest.StartSequence, manifest.EndSequence,
		manifest.UnitCount, manifest.AuditCount, manifest.MinCreatedAt, manifest.MaxCreatedAt, manifest.TimeComplete,
		manifest.PlainValueCount, manifest.ZstdValueCount, manifest.TotalPlainBytes, manifest.TotalStoredBytes,
		manifest.LogicalDigest, manifest.FileDigest); err != nil {
		return fmt.Errorf("register archive segment: %w", err)
	}
	stored, err := scanArchiveSegment(db.QueryRowContext(ctx, `SELECT `+archiveSegmentColumns+` FROM archive_segments WHERE basename = ?`, manifest.Basename))
	if err != nil {
		return fmt.Errorf("read back archive segment registration: %w", err)
	}
	if stored != manifest {
		return fmt.Errorf("%w: basename %s is already registered with different evidence", ErrSegmentCatalogConflict, manifest.Basename)
	}
	return nil
}

// syncArchiveRoot makes the sealed segment's directory entry durable before
// the catalog references it.
func syncArchiveRoot(root string) error {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open archive root for durability: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("fsync archive root: %w", err)
	}
	return nil
}

// ListArchiveSegments returns every registered segment manifest ordered by
// sequence range. Callers select candidates; ranges may overlap.
func (d *Database) ListArchiveSegments(ctx context.Context) ([]ArchiveSegmentManifest, error) {
	db, err := d.open(ctx)
	if err != nil {
		return nil, err
	}
	defer d.release(db)
	rows, err := db.QueryContext(ctx, `SELECT `+archiveSegmentColumns+` FROM archive_segments ORDER BY start_sequence, end_sequence, basename`)
	if err != nil {
		return nil, fmt.Errorf("list archive segments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var manifests []ArchiveSegmentManifest
	for rows.Next() {
		manifest, scanErr := scanArchiveSegment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan archive segment: %w", scanErr)
		}
		manifests = append(manifests, manifest)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate archive segments: %w", err)
	}
	return manifests, nil
}

func scanArchiveSegment(row interface{ Scan(...any) error }) (ArchiveSegmentManifest, error) {
	var m ArchiveSegmentManifest
	err := row.Scan(&m.Basename, &m.StoreID, &m.FormatVersion, &m.StartSequence, &m.EndSequence,
		&m.UnitCount, &m.AuditCount, &m.MinCreatedAt, &m.MaxCreatedAt, &m.TimeComplete,
		&m.PlainValueCount, &m.ZstdValueCount, &m.TotalPlainBytes, &m.TotalStoredBytes,
		&m.LogicalDigest, &m.FileDigest)
	return m, err
}
