//nolint:wrapcheck // This infrastructure boundary preserves causes while adding typed segment errors where relevant.
package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"

	"github.com/duck8823/traceary/domain"
)

const (
	segmentCodecPlain = "plain"
	segmentCodecZstd  = "zstd"
)

var (
	// ErrSegmentUnsafeLocation rejects paths outside the archive root or through symlinks.
	ErrSegmentUnsafeLocation = errors.New("unsafe archive segment location")
	// ErrSegmentUnsupportedFormat identifies an unreadable future schema.
	ErrSegmentUnsupportedFormat = errors.New("unsupported archive segment format")
	// ErrSegmentUnsupportedCodec identifies an unreadable payload codec.
	ErrSegmentUnsupportedCodec = errors.New("unsupported archive segment codec")
	// ErrSegmentCorrupt identifies checksum, encoding, or aggregate mismatches.
	ErrSegmentCorrupt = errors.New("corrupt archive segment")
	// ErrSegmentLimit identifies a configured cap violation.
	ErrSegmentLimit   = errors.New("archive segment resource limit exceeded")
	segmentBasenameRE = regexp.MustCompile(`^segment-v1-[0-9a-f]{64}\.sqlite$`)
)

// ArchiveSegmentLimits bounds both construction and independent verification.
type ArchiveSegmentLimits struct {
	MaxValuePlainBytes  int64
	MaxValueStoredBytes int64
	MaxTotalPlainBytes  int64
	MaxTotalStoredBytes int64
	MaxFileBytes        int64
}

func (l ArchiveSegmentLimits) validate() error {
	if l.MaxValuePlainBytes <= 0 || l.MaxValueStoredBytes <= 0 || l.MaxTotalPlainBytes <= 0 || l.MaxTotalStoredBytes <= 0 || l.MaxFileBytes <= 0 {
		return fmt.Errorf("%w: every cap must be positive", ErrSegmentLimit)
	}
	return nil
}

// ArchiveSegmentConfig fixes all logical format choices.
type ArchiveSegmentConfig struct {
	StoreID          string
	CompressionFloor int
	Limits           ArchiveSegmentLimits
}

// ArchiveSegmentManifest is machine-independent aggregate evidence.
type ArchiveSegmentManifest struct {
	StoreID          string `json:"store_id"`
	FormatVersion    uint32 `json:"format_version"`
	StartSequence    uint64 `json:"start_sequence"`
	EndSequence      uint64 `json:"end_sequence"`
	UnitCount        uint64 `json:"unit_count"`
	AuditCount       uint64 `json:"audit_count"`
	MinCreatedAt     string `json:"min_created_at,omitempty"`
	MaxCreatedAt     string `json:"max_created_at,omitempty"`
	PlainValueCount  uint64 `json:"plain_value_count"`
	ZstdValueCount   uint64 `json:"zstd_value_count"`
	TotalPlainBytes  int64  `json:"total_plain_bytes"`
	TotalStoredBytes int64  `json:"total_stored_bytes"`
	LogicalDigest    string `json:"logical_digest"`
	FileDigest       string `json:"file_digest"`
	Basename         string `json:"basename"`
}

// ArchiveHistoryUnit carries the canonical unit and an optional ordering time.
type ArchiveHistoryUnit struct {
	Unit domain.HistoryUnit
}

type archiveSegmentBuilder struct {
	syncFile  func(*os.File) error
	sealFile  func(*os.File, os.FileMode) error
	afterUnit func(int) error
}

// BuildArchiveSegmentV1 creates and seals one immutable segment in an owned
// candidate directory. Publishing it into the archive root belongs to #1651.
func BuildArchiveSegmentV1(ctx context.Context, root string, units []ArchiveHistoryUnit, cfg ArchiveSegmentConfig) (ArchiveSegmentManifest, error) {
	return archiveSegmentBuilder{syncFile: func(f *os.File) error { return f.Sync() }, sealFile: func(f *os.File, m os.FileMode) error { return f.Chmod(m) }}.build(ctx, root, units, cfg)
}

func (b archiveSegmentBuilder) build(ctx context.Context, root string, units []ArchiveHistoryUnit, cfg ArchiveSegmentConfig) (manifest ArchiveSegmentManifest, err error) {
	if err := cfg.Limits.validate(); err != nil {
		return manifest, err
	}
	if cfg.StoreID == "" || len(units) == 0 || cfg.CompressionFloor < 0 {
		return manifest, fmt.Errorf("invalid archive segment configuration")
	}
	if err := validateArchiveRoot(root); err != nil {
		return manifest, err
	}

	tmp, err := os.CreateTemp(root, ".segment-v1-*.candidate")
	if err != nil {
		return manifest, fmt.Errorf("create segment candidate: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = tmp.Close() }()
	defer func() {
		if err != nil {
			_ = os.Chmod(tmpPath, 0o600)
			_ = os.Remove(tmpPath)
		}
	}()

	db, err := sql.Open("sqlite", segmentSQLiteDSN(tmpPath, "rw", false)+"&_pragma=journal_mode(OFF)&_pragma=synchronous(FULL)")
	if err != nil {
		return manifest, err
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(ctx, segmentSchemaV1); err != nil {
		return manifest, fmt.Errorf("create segment schema: %w", err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return manifest, err
	}
	defer func() { _ = encoder.Close() }()

	manifest = ArchiveSegmentManifest{StoreID: cfg.StoreID, FormatVersion: domain.SegmentFormatV1, StartSequence: units[0].Unit.Sequence, EndSequence: units[len(units)-1].Unit.Sequence, UnitCount: uint64(len(units))}
	logical := sha256.New()
	var previous uint64
	var minCreatedAt, maxCreatedAt time.Time
	for i, item := range units {
		if err = ctx.Err(); err != nil {
			return manifest, err
		}
		if i > 0 && item.Unit.Sequence != previous+1 {
			return manifest, fmt.Errorf("history unit sequence is not contiguous")
		}
		previous = item.Unit.Sequence
		if sizeErr := item.Unit.ValidateCanonicalSize(cfg.Limits.MaxValuePlainBytes); sizeErr != nil {
			return manifest, fmt.Errorf("%w: %v", ErrSegmentLimit, sizeErr)
		}
		plain, encodeErr := item.Unit.CanonicalBytes()
		if encodeErr != nil {
			return manifest, fmt.Errorf("encode history unit: %w", encodeErr)
		}
		if int64(len(plain)) > cfg.Limits.MaxValuePlainBytes {
			return manifest, fmt.Errorf("%w: plaintext value", ErrSegmentLimit)
		}
		codec := segmentCodecPlain
		stored := plain
		if len(plain) >= cfg.CompressionFloor {
			compressed := encoder.EncodeAll(plain, nil)
			if len(compressed) < len(plain) {
				codec, stored = segmentCodecZstd, compressed
			}
		}
		if int64(len(stored)) > cfg.Limits.MaxValueStoredBytes {
			return manifest, fmt.Errorf("%w: stored value", ErrSegmentLimit)
		}
		manifest.TotalPlainBytes += int64(len(plain))
		manifest.TotalStoredBytes += int64(len(stored))
		if manifest.TotalPlainBytes > cfg.Limits.MaxTotalPlainBytes || manifest.TotalStoredBytes > cfg.Limits.MaxTotalStoredBytes {
			return manifest, fmt.Errorf("%w: aggregate bytes", ErrSegmentLimit)
		}
		if codec == segmentCodecZstd {
			manifest.ZstdValueCount++
		} else {
			manifest.PlainValueCount++
		}
		if item.Unit.Audit != nil {
			manifest.AuditCount++
		}
		if !item.Unit.CreatedAt.IsZero() {
			ts := item.Unit.CreatedAt.UTC()
			if minCreatedAt.IsZero() || ts.Before(minCreatedAt) {
				minCreatedAt = ts
			}
			if maxCreatedAt.IsZero() || ts.After(maxCreatedAt) {
				maxCreatedAt = ts
			}
		}
		logical.Write(plain)
		checksum := sha256.Sum256(plain)
		if _, err = db.ExecContext(ctx, `INSERT INTO history_units(sequence, codec, plaintext_length, checksum, payload) VALUES(?,?,?,?,?)`, item.Unit.Sequence, codec, len(plain), checksum[:], stored); err != nil {
			return manifest, fmt.Errorf("write segment unit: %w", err)
		}
		if b.afterUnit != nil {
			if err = b.afterUnit(i); err != nil {
				return manifest, fmt.Errorf("archive segment interrupted after unit: %w", err)
			}
		}
	}
	manifest.MinCreatedAt = formatOptionalTime(minCreatedAt)
	manifest.MaxCreatedAt = formatOptionalTime(maxCreatedAt)
	var logicalDigest [sha256.Size]byte
	copy(logicalDigest[:], logical.Sum(nil))
	identity, identityErr := domain.NewSegmentIdentity(cfg.StoreID, manifest.StartSequence, manifest.EndSequence, logicalDigest)
	if identityErr != nil {
		return manifest, fmt.Errorf("create segment identity: %w", identityErr)
	}
	manifest.LogicalDigest = hex.EncodeToString(logicalDigest[:])
	manifest.Basename = identity.Basename()
	finalPath, pathErr := safeArchivePath(root, manifest.Basename, false)
	if pathErr != nil {
		return manifest, pathErr
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO segment_manifest(format_version,store_id,start_sequence,end_sequence,unit_count,audit_count,min_created_at,max_created_at,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,logical_digest,basename) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, manifest.FormatVersion, manifest.StoreID, manifest.StartSequence, manifest.EndSequence, manifest.UnitCount, manifest.AuditCount, manifest.MinCreatedAt, manifest.MaxCreatedAt, manifest.PlainValueCount, manifest.ZstdValueCount, manifest.TotalPlainBytes, manifest.TotalStoredBytes, logicalDigest[:], manifest.Basename); err != nil {
		return manifest, fmt.Errorf("write segment manifest: %w", err)
	}
	if _, err = db.ExecContext(ctx, `PRAGMA user_version=1`); err != nil {
		return manifest, err
	}
	if _, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return manifest, err
	}
	if err = db.Close(); err != nil {
		return manifest, fmt.Errorf("close segment: %w", err)
	}
	if _, err = verifyArchiveSegmentPath(ctx, tmpPath, manifest, cfg.Limits, false); err != nil {
		return manifest, fmt.Errorf("verify segment candidate before seal: %w", err)
	}
	if err = b.syncFile(tmp); err != nil {
		return manifest, fmt.Errorf("fsync segment: %w", err)
	}
	if err = b.sealFile(tmp, 0o400); err != nil {
		return manifest, fmt.Errorf("seal segment: %w", err)
	}
	fileDigest, err := digestOpenFile(ctx, tmp, cfg.Limits.MaxFileBytes)
	if err != nil {
		return manifest, err
	}
	manifest.FileDigest = fileDigest
	// A hard link installs the content-addressed name without replacing an
	// existing inode. Directory durability and publication journaling remain
	// the responsibility of #1651.
	if err = os.Link(tmpPath, finalPath); err != nil {
		return manifest, fmt.Errorf("install segment candidate without replacement: %w", err)
	}
	if err = os.Remove(tmpPath); err != nil {
		_ = os.Remove(finalPath)
		return manifest, fmt.Errorf("remove installed candidate name: %w", err)
	}
	if _, err = verifyArchiveSegmentPath(ctx, finalPath, manifest, cfg.Limits, true); err != nil {
		_ = os.Chmod(finalPath, 0o600)
		_ = os.Remove(finalPath)
		return manifest, fmt.Errorf("verify sealed segment candidate: %w", err)
	}
	return manifest, nil
}

const segmentSchemaV1 = `
CREATE TABLE segment_manifest(format_version INTEGER NOT NULL, store_id TEXT NOT NULL, start_sequence INTEGER NOT NULL, end_sequence INTEGER NOT NULL, unit_count INTEGER NOT NULL, audit_count INTEGER NOT NULL, min_created_at TEXT NOT NULL, max_created_at TEXT NOT NULL, plain_value_count INTEGER NOT NULL, zstd_value_count INTEGER NOT NULL, total_plain_bytes INTEGER NOT NULL, total_stored_bytes INTEGER NOT NULL, logical_digest BLOB NOT NULL, basename TEXT NOT NULL);
CREATE TABLE history_units(sequence INTEGER PRIMARY KEY, codec TEXT NOT NULL, plaintext_length INTEGER NOT NULL, checksum BLOB NOT NULL, payload BLOB NOT NULL);
`

// InspectArchiveSegmentManifest reads aggregate metadata without decoding payload rows.
func InspectArchiveSegmentManifest(ctx context.Context, root, basename string, limits ArchiveSegmentLimits) (ArchiveSegmentManifest, error) {
	if err := limits.validate(); err != nil {
		return ArchiveSegmentManifest{}, err
	}
	path, err := safeArchivePath(root, basename, true)
	if err != nil {
		return ArchiveSegmentManifest{}, err
	}
	return inspectArchiveSegmentPath(ctx, path, basename, true, limits.MaxFileBytes)
}

func inspectArchiveSegmentPath(ctx context.Context, path, expectedBasename string, requireSealed bool, maxFileBytes int64) (ArchiveSegmentManifest, error) {
	db, pinned, err := openPinnedImmutableSegment(ctx, path, requireSealed)
	if err != nil {
		return ArchiveSegmentManifest{}, err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = pinned.Close() }()
	info, err := pinned.Stat()
	if err != nil {
		return ArchiveSegmentManifest{}, fmt.Errorf("stat segment: %w", err)
	}
	if info.Size() > maxFileBytes {
		return ArchiveSegmentManifest{}, fmt.Errorf("%w: file bytes", ErrSegmentLimit)
	}
	var m ArchiveSegmentManifest
	var digest []byte
	err = db.QueryRowContext(ctx, `SELECT format_version,store_id,start_sequence,end_sequence,unit_count,audit_count,min_created_at,max_created_at,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,logical_digest,basename FROM segment_manifest`).Scan(&m.FormatVersion, &m.StoreID, &m.StartSequence, &m.EndSequence, &m.UnitCount, &m.AuditCount, &m.MinCreatedAt, &m.MaxCreatedAt, &m.PlainValueCount, &m.ZstdValueCount, &m.TotalPlainBytes, &m.TotalStoredBytes, &digest, &m.Basename)
	if err != nil {
		return m, fmt.Errorf("%w: read manifest: %v", ErrSegmentCorrupt, err)
	}
	if m.FormatVersion != domain.SegmentFormatV1 {
		return m, fmt.Errorf("%w: %d", ErrSegmentUnsupportedFormat, m.FormatVersion)
	}
	if (expectedBasename != "" && m.Basename != expectedBasename) || len(digest) != sha256.Size {
		return m, fmt.Errorf("%w: manifest identity", ErrSegmentCorrupt)
	}
	m.LogicalDigest = hex.EncodeToString(digest)
	return m, nil
}

// VerifyArchiveSegment fully decodes a sealed segment and binds it to the
// durable expected manifest supplied by its caller.
func VerifyArchiveSegment(ctx context.Context, root string, expected ArchiveSegmentManifest, limits ArchiveSegmentLimits) (ArchiveSegmentManifest, error) {
	if err := limits.validate(); err != nil {
		return ArchiveSegmentManifest{}, err
	}
	path, err := safeArchivePath(root, expected.Basename, true)
	if err != nil {
		return ArchiveSegmentManifest{}, err
	}
	return verifyArchiveSegmentPath(ctx, path, expected, limits, true)
}

func verifyArchiveSegmentPath(ctx context.Context, path string, expected ArchiveSegmentManifest, limits ArchiveSegmentLimits, requireFileDigest bool) (ArchiveSegmentManifest, error) {
	m, err := inspectArchiveSegmentPath(ctx, path, expected.Basename, requireFileDigest, limits.MaxFileBytes)
	if err != nil {
		return m, err
	}
	if !sameLogicalManifest(m, expected) {
		return m, fmt.Errorf("%w: expected manifest mismatch", ErrSegmentCorrupt)
	}
	digest, decodeErr := hex.DecodeString(m.LogicalDigest)
	if decodeErr != nil || len(digest) != sha256.Size {
		return m, fmt.Errorf("%w: logical digest", ErrSegmentCorrupt)
	}
	var logicalDigest [sha256.Size]byte
	copy(logicalDigest[:], digest)
	identity, identityErr := domain.NewSegmentIdentity(m.StoreID, m.StartSequence, m.EndSequence, logicalDigest)
	if identityErr != nil || identity.Basename() != m.Basename {
		return m, fmt.Errorf("%w: content-addressed identity", ErrSegmentCorrupt)
	}
	db, pinned, err := openPinnedImmutableSegment(ctx, path, requireFileDigest)
	if err != nil {
		return m, err
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = pinned.Close() }()
	if requireFileDigest {
		m.FileDigest, err = digestOpenFile(ctx, pinned, limits.MaxFileBytes)
		if err != nil {
			return m, err
		}
		if expected.FileDigest == "" || m.FileDigest != expected.FileDigest {
			return m, fmt.Errorf("%w: expected file digest mismatch", ErrSegmentCorrupt)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT sequence,codec,plaintext_length,checksum,payload FROM history_units ORDER BY sequence`)
	if err != nil {
		return m, fmt.Errorf("%w: query payloads: %v", ErrSegmentCorrupt, err)
	}
	defer func() { _ = rows.Close() }()
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(limits.MaxValuePlainBytes)))
	if err != nil {
		return m, err
	}
	defer decoder.Close()
	logical := sha256.New()
	var count uint64
	var auditCount, plainCount, zstdCount uint64
	var totalPlain, totalStored int64
	var first, previous uint64
	var minCreatedAt, maxCreatedAt time.Time
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return m, err
		}
		var seq uint64
		var codec string
		var plainLen int64
		var checksum, stored []byte
		if err = rows.Scan(&seq, &codec, &plainLen, &checksum, &stored); err != nil {
			return m, fmt.Errorf("%w: scan payload", ErrSegmentCorrupt)
		}
		if count > 0 && seq != previous+1 {
			return m, fmt.Errorf("%w: non-contiguous sequence", ErrSegmentCorrupt)
		}
		if count == 0 {
			first = seq
		}
		previous = seq
		if plainLen < 0 || plainLen > limits.MaxValuePlainBytes || int64(len(stored)) > limits.MaxValueStoredBytes {
			return m, fmt.Errorf("%w: value", ErrSegmentLimit)
		}
		var plain []byte
		switch codec {
		case segmentCodecPlain:
			plain = append([]byte(nil), stored...)
			plainCount++
		case segmentCodecZstd:
			zstdCount++
			plain, err = decoder.DecodeAll(stored, make([]byte, 0, plainLen))
			if err != nil {
				return m, fmt.Errorf("%w: zstd payload: %v", ErrSegmentCorrupt, err)
			}
		default:
			return m, fmt.Errorf("%w: %s", ErrSegmentUnsupportedCodec, codec)
		}
		if int64(len(plain)) != plainLen || !bytes.Equal(checksum, digestBytes(plain)) {
			return m, fmt.Errorf("%w: payload checksum", ErrSegmentCorrupt)
		}
		unit, decodeUnitErr := domain.DecodeHistoryUnitCanonical(plain)
		if decodeUnitErr != nil || unit.Sequence != seq {
			return m, fmt.Errorf("%w: canonical history unit", ErrSegmentCorrupt)
		}
		if unit.Audit != nil {
			auditCount++
		}
		if minCreatedAt.IsZero() || unit.CreatedAt.Before(minCreatedAt) {
			minCreatedAt = unit.CreatedAt
		}
		if maxCreatedAt.IsZero() || unit.CreatedAt.After(maxCreatedAt) {
			maxCreatedAt = unit.CreatedAt
		}
		totalPlain += int64(len(plain))
		totalStored += int64(len(stored))
		if totalPlain > limits.MaxTotalPlainBytes || totalStored > limits.MaxTotalStoredBytes {
			return m, fmt.Errorf("%w: aggregate", ErrSegmentLimit)
		}
		logical.Write(plain)
		count++
	}
	if err = rows.Err(); err != nil {
		return m, err
	}
	if count != m.UnitCount || auditCount != m.AuditCount || plainCount != m.PlainValueCount || zstdCount != m.ZstdValueCount || totalPlain != m.TotalPlainBytes || totalStored != m.TotalStoredBytes || hex.EncodeToString(logical.Sum(nil)) != m.LogicalDigest || formatOptionalTime(minCreatedAt) != m.MinCreatedAt || formatOptionalTime(maxCreatedAt) != m.MaxCreatedAt {
		return m, fmt.Errorf("%w: aggregate mismatch", ErrSegmentCorrupt)
	}
	if count == 0 || first != m.StartSequence || previous != m.EndSequence || m.StartSequence+count-1 != m.EndSequence {
		return m, fmt.Errorf("%w: range mismatch", ErrSegmentCorrupt)
	}
	return m, nil
}

func sameLogicalManifest(a, b ArchiveSegmentManifest) bool {
	a.FileDigest, b.FileDigest = "", ""
	return a == b
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func segmentSQLiteDSN(path, mode string, immutable bool) string {
	query := url.Values{"mode": []string{mode}}
	if immutable {
		query.Set("immutable", "1")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func openPinnedImmutableSegment(ctx context.Context, path string, requireSealed bool) (*sql.DB, *os.File, error) {
	dirFD, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("pin segment directory: %w", err)
	}
	defer func() { _ = unix.Close(dirFD) }()
	fd, err := unix.Openat(dirFD, filepath.Base(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open immutable segment without following symlinks: %w", err)
	}
	pinned := os.NewFile(uintptr(fd), filepath.Base(path))
	info, err := pinned.Stat()
	if err != nil || !info.Mode().IsRegular() || (requireSealed && info.Mode().Perm() != 0o400) {
		_ = pinned.Close()
		return nil, nil, fmt.Errorf("%w: segment must be a regular 0400 file", ErrSegmentCorrupt)
	}
	db, err := sql.Open("sqlite", segmentSQLiteDSN(filepath.Join("/dev/fd", strconv.Itoa(fd)), "ro", true))
	if err != nil {
		_ = pinned.Close()
		return nil, nil, fmt.Errorf("open pinned immutable segment: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = pinned.Close()
		return nil, nil, fmt.Errorf("ping pinned immutable segment: %w", err)
	}
	return db, pinned, nil
}

func validateArchiveRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrSegmentUnsafeLocation
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	// macOS exposes /var as an alias of /private/var. Ancestor resolution may
	// therefore differ from filepath.Abs even when the archive root itself is
	// not a symlink. Lstat above rejects a replaceable root symlink; resolving
	// ancestors here only proves that the complete root exists.
	if _, err := filepath.EvalSymlinks(abs); err != nil {
		return ErrSegmentUnsafeLocation
	}
	return nil
}
func safeArchivePath(root, basename string, mustExist bool) (string, error) {
	if filepath.IsAbs(basename) || filepath.Base(basename) != basename || !segmentBasenameRE.MatchString(basename) {
		return "", ErrSegmentUnsafeLocation
	}
	if err := validateArchiveRoot(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, basename)
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", ErrSegmentUnsafeLocation
	}
	if mustExist && err != nil {
		return "", err
	}
	return path, nil
}
func digestBytes(v []byte) []byte { d := sha256.Sum256(v); return d[:] }
func digestFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	return digestOpenFile(context.Background(), f, info.Size())
}

func digestOpenFile(ctx context.Context, f *os.File, maxBytes int64) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek segment digest input: %w", err)
	}
	h := sha256.New()
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("digest segment canceled: %w", err)
		}
		n, readErr := f.Read(buffer)
		total += int64(n)
		if total > maxBytes {
			return "", fmt.Errorf("%w: file bytes", ErrSegmentLimit)
		}
		if n > 0 {
			_, _ = h.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("digest segment file: %w", readErr)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
