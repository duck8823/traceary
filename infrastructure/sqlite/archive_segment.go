//nolint:wrapcheck // This infrastructure boundary preserves causes while adding typed segment errors where relevant.
package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	MaxSummaryBytes     int64
	MaxSummaryRows      uint64
}

func (l ArchiveSegmentLimits) validate() error {
	if l.MaxValuePlainBytes <= 0 || l.MaxValueStoredBytes <= 0 || l.MaxTotalPlainBytes <= 0 || l.MaxTotalStoredBytes <= 0 || l.MaxFileBytes <= 0 || l.MaxSummaryBytes <= 0 || l.MaxSummaryRows == 0 {
		return fmt.Errorf("%w: every cap must be positive", ErrSegmentLimit)
	}
	return nil
}

// ArchiveSegmentConfig fixes all logical format choices.
type ArchiveSegmentConfig struct {
	StoreID          string
	CompressionFloor int
	Limits           ArchiveSegmentLimits
	Summary          domain.SegmentCatalogSummaryV1
}

// ArchiveSegmentManifest is machine-independent aggregate evidence.
type ArchiveSegmentManifest struct {
	StoreID             string `json:"store_id"`
	FormatVersion       uint32 `json:"format_version"`
	StartSequence       uint64 `json:"start_sequence"`
	EndSequence         uint64 `json:"end_sequence"`
	UnitCount           uint64 `json:"unit_count"`
	AuditCount          uint64 `json:"audit_count"`
	MinCreatedAt        string `json:"min_created_at,omitempty"`
	MaxCreatedAt        string `json:"max_created_at,omitempty"`
	PlainValueCount     uint64 `json:"plain_value_count"`
	ZstdValueCount      uint64 `json:"zstd_value_count"`
	TotalPlainBytes     int64  `json:"total_plain_bytes"`
	TotalStoredBytes    int64  `json:"total_stored_bytes"`
	LogicalDigest       string `json:"logical_digest"`
	FileDigest          string `json:"file_digest"`
	Basename            string `json:"basename"`
	SummaryVersion      uint32 `json:"summary_version"`
	SummaryDigest       string `json:"summary_digest"`
	FilterKeyID         string `json:"filter_key_id"`
	TimeSummaryComplete bool   `json:"time_summary_complete"`
	SummaryRowCount     uint64 `json:"summary_row_count"`
	SummaryByteCount    int64  `json:"summary_byte_count"`
}

// ArchiveSegmentManifestDigest returns the canonical binding digest for the
// fixed, field-ordered v1 manifest. JSON is deterministic for this struct and
// the domain separator prevents reuse as another digest class.
func ArchiveSegmentManifestDigest(manifest ArchiveSegmentManifest) (string, error) {
	if manifest.FormatVersion != domain.SegmentFormatV1 || manifest.SummaryVersion != domain.SegmentSummaryV1 || manifest.StoreID == "" || manifest.Basename == "" || manifest.FileDigest == "" || manifest.LogicalDigest == "" || manifest.SummaryDigest == "" {
		return "", ErrSegmentCorrupt
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode archive segment manifest: %w", err)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary/archive-segment-manifest/v1\x00"))
	_, _ = h.Write(encoded)
	return hex.EncodeToString(h.Sum(nil)), nil
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

	ownedRoot, err := os.OpenRoot(root)
	if err != nil {
		return manifest, fmt.Errorf("pin owned candidate root: %w", err)
	}
	defer func() { _ = ownedRoot.Close() }()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return manifest, fmt.Errorf("name segment candidate: %w", err)
	}
	tmpName := ".segment-v1-" + hex.EncodeToString(random[:]) + ".candidate"
	tmp, err := ownedRoot.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return manifest, fmt.Errorf("create segment candidate: %w", err)
	}
	tmpPath := filepath.Join(ownedRoot.Name(), tmpName)
	cleanupName := tmpName
	defer func() { _ = tmp.Close() }()
	defer func() {
		if err != nil {
			_ = tmp.Chmod(0o600)
			_ = ownedRoot.Remove(cleanupName)
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
	_, _ = logical.Write([]byte("traceary-segment-history-v1\x00"))
	var previous uint64
	var minCreatedAt, maxCreatedAt time.Time
	allTimesValid := true
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
		if !item.Unit.CreatedAt().IsZero() {
			ts := item.Unit.CreatedAt().UTC()
			if minCreatedAt.IsZero() || ts.Before(minCreatedAt) {
				minCreatedAt = ts
			}
			if maxCreatedAt.IsZero() || ts.After(maxCreatedAt) {
				maxCreatedAt = ts
			}
		}
		if item.Unit.CreatedAt().IsZero() {
			allTimesValid = false
		}
		_, _ = logical.Write(binary.BigEndian.AppendUint64(nil, uint64(len(plain))))
		_, _ = logical.Write(plain)
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
	if !allTimesValid || minCreatedAt.IsZero() || maxCreatedAt.IsZero() {
		cfg.Summary.TimeComplete = false
	}
	summaryRows := uint64(len(cfg.Summary.ExactTokens)) + uint64(len(cfg.Summary.Blooms)) + uint64(len(cfg.Summary.Sessions))
	if summaryRows > cfg.Limits.MaxSummaryRows {
		return manifest, fmt.Errorf("%w: summary rows", ErrSegmentLimit)
	}
	summaryBytes, summaryErr := cfg.Summary.CanonicalBytes(cfg.Limits.MaxSummaryBytes)
	if summaryErr != nil {
		return manifest, fmt.Errorf("encode segment summary: %w", summaryErr)
	}
	manifest.SummaryVersion = domain.SegmentSummaryV1
	manifest.SummaryDigest = hex.EncodeToString(digestBytes(summaryBytes))
	manifest.FilterKeyID = cfg.Summary.FilterKeyID
	manifest.TimeSummaryComplete = cfg.Summary.TimeComplete
	manifest.SummaryRowCount = summaryRows
	manifest.SummaryByteCount = int64(len(summaryBytes))
	if manifest.SummaryRowCount > cfg.Limits.MaxSummaryRows {
		return manifest, fmt.Errorf("%w: summary rows", ErrSegmentLimit)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO segment_summary(id,summary_version,filter_key_id,time_summary_complete,canonical_bytes,summary_digest) VALUES(1,?,?,?,?,?)`, manifest.SummaryVersion, manifest.FilterKeyID, manifest.TimeSummaryComplete, summaryBytes, digestBytes(summaryBytes)); err != nil {
		return manifest, fmt.Errorf("write segment summary: %w", err)
	}
	for _, token := range cfg.Summary.ExactTokens {
		if _, err = db.ExecContext(ctx, `INSERT INTO segment_exact_filters(kind,token) VALUES(?,?)`, token.Kind, token.Value[:]); err != nil {
			return manifest, fmt.Errorf("write exact filter: %w", err)
		}
	}
	for _, bloom := range cfg.Summary.Blooms {
		if _, err = db.ExecContext(ctx, `INSERT INTO segment_bloom_filters(kind,bit_count,hash_count,bits) VALUES(?,?,?,?)`, bloom.Kind, bloom.BitCount, bloom.HashCount, bloom.Bits); err != nil {
			return manifest, fmt.Errorf("write bloom filter: %w", err)
		}
	}
	for _, session := range cfg.Summary.Sessions {
		if _, err = db.ExecContext(ctx, `INSERT INTO segment_session_aggregates(session_token,unit_count,audit_count) VALUES(?,?,?)`, session.SessionToken[:], session.UnitCount, session.AuditCount); err != nil {
			return manifest, fmt.Errorf("write session aggregate: %w", err)
		}
	}
	_, _ = logical.Write([]byte("traceary-segment-summary-v1\x00"))
	_, _ = logical.Write(binary.BigEndian.AppendUint64(nil, uint64(len(summaryBytes))))
	_, _ = logical.Write(summaryBytes)
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
	if _, err = db.ExecContext(ctx, `INSERT INTO segment_manifest(id,format_version,store_id,start_sequence,end_sequence,unit_count,audit_count,min_created_at,max_created_at,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,logical_digest,basename,summary_version,summary_digest,filter_key_id,time_summary_complete,summary_row_count,summary_byte_count) VALUES(1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, manifest.FormatVersion, manifest.StoreID, manifest.StartSequence, manifest.EndSequence, manifest.UnitCount, manifest.AuditCount, manifest.MinCreatedAt, manifest.MaxCreatedAt, manifest.PlainValueCount, manifest.ZstdValueCount, manifest.TotalPlainBytes, manifest.TotalStoredBytes, logicalDigest[:], manifest.Basename, manifest.SummaryVersion, digestBytes(summaryBytes), manifest.FilterKeyID, manifest.TimeSummaryComplete, manifest.SummaryRowCount, manifest.SummaryByteCount); err != nil {
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
	if err = b.syncFile(tmp); err != nil {
		return manifest, fmt.Errorf("fsync sealed segment metadata: %w", err)
	}
	fileDigest, err := digestOpenFile(ctx, tmp, cfg.Limits.MaxFileBytes)
	if err != nil {
		return manifest, err
	}
	manifest.FileDigest = fileDigest
	if _, statErr := ownedRoot.Lstat(manifest.Basename); statErr == nil {
		return manifest, fmt.Errorf("seal candidate name: %w", os.ErrExist)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return manifest, fmt.Errorf("inspect candidate name: %w", statErr)
	}
	if err = ownedRoot.Rename(tmpName, manifest.Basename); err != nil {
		return manifest, fmt.Errorf("name sealed candidate: %w", err)
	}
	cleanupName = manifest.Basename
	if _, err = verifyArchiveSegmentPath(ctx, finalPath, manifest, cfg.Limits, true); err != nil {
		return manifest, fmt.Errorf("verify sealed segment candidate: %w", err)
	}
	return manifest, nil
}

const segmentSchemaV1 = `
CREATE TABLE segment_manifest(id INTEGER PRIMARY KEY CHECK(id=1), format_version INTEGER NOT NULL, store_id TEXT NOT NULL, start_sequence INTEGER NOT NULL, end_sequence INTEGER NOT NULL, unit_count INTEGER NOT NULL, audit_count INTEGER NOT NULL, min_created_at TEXT NOT NULL, max_created_at TEXT NOT NULL, plain_value_count INTEGER NOT NULL, zstd_value_count INTEGER NOT NULL, total_plain_bytes INTEGER NOT NULL, total_stored_bytes INTEGER NOT NULL, logical_digest BLOB NOT NULL, basename TEXT NOT NULL, summary_version INTEGER NOT NULL, summary_digest BLOB NOT NULL, filter_key_id TEXT NOT NULL, time_summary_complete INTEGER NOT NULL, summary_row_count INTEGER NOT NULL, summary_byte_count INTEGER NOT NULL);
CREATE TABLE history_units(sequence INTEGER PRIMARY KEY, codec TEXT NOT NULL, plaintext_length INTEGER NOT NULL, checksum BLOB NOT NULL, payload BLOB NOT NULL);
CREATE TABLE segment_summary(id INTEGER PRIMARY KEY CHECK(id=1), summary_version INTEGER NOT NULL, filter_key_id TEXT NOT NULL, time_summary_complete INTEGER NOT NULL CHECK(time_summary_complete IN (0,1)), canonical_bytes BLOB NOT NULL, summary_digest BLOB NOT NULL);
CREATE TABLE segment_exact_filters(kind INTEGER NOT NULL, token BLOB NOT NULL, PRIMARY KEY(kind,token));
CREATE TABLE segment_bloom_filters(kind INTEGER PRIMARY KEY, bit_count INTEGER NOT NULL CHECK(bit_count>0 AND bit_count<=8388608), hash_count INTEGER NOT NULL CHECK(hash_count BETWEEN 1 AND 16), bits BLOB NOT NULL CHECK(length(bits)*8=bit_count));
CREATE TABLE segment_session_aggregates(session_token BLOB PRIMARY KEY, unit_count INTEGER NOT NULL, audit_count INTEGER NOT NULL);
`

// InspectArchiveSegmentManifest performs bounded structural inspection of an
// untrusted sealed file. It neither decodes payloads nor computes a file digest;
// callers must use VerifyArchiveSegment before trusting the returned facts.
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
	return inspectArchiveSegmentOpen(ctx, db, pinned, expectedBasename, maxFileBytes)
}

func inspectArchiveSegmentOpen(ctx context.Context, db *sql.DB, pinned *os.File, expectedBasename string, maxFileBytes int64) (ArchiveSegmentManifest, error) {
	info, err := pinned.Stat()
	if err != nil {
		return ArchiveSegmentManifest{}, fmt.Errorf("stat segment: %w", err)
	}
	if info.Size() > maxFileBytes {
		return ArchiveSegmentManifest{}, fmt.Errorf("%w: file bytes", ErrSegmentLimit)
	}
	var m ArchiveSegmentManifest
	var digest, summaryDigest []byte
	var manifestRows int64
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM segment_manifest`).Scan(&manifestRows); err != nil || manifestRows != 1 {
		return m, fmt.Errorf("%w: manifest singleton", ErrSegmentCorrupt)
	}
	err = db.QueryRowContext(ctx, `SELECT format_version,store_id,start_sequence,end_sequence,unit_count,audit_count,min_created_at,max_created_at,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,logical_digest,basename,summary_version,summary_digest,filter_key_id,time_summary_complete,summary_row_count,summary_byte_count FROM segment_manifest`).Scan(&m.FormatVersion, &m.StoreID, &m.StartSequence, &m.EndSequence, &m.UnitCount, &m.AuditCount, &m.MinCreatedAt, &m.MaxCreatedAt, &m.PlainValueCount, &m.ZstdValueCount, &m.TotalPlainBytes, &m.TotalStoredBytes, &digest, &m.Basename, &m.SummaryVersion, &summaryDigest, &m.FilterKeyID, &m.TimeSummaryComplete, &m.SummaryRowCount, &m.SummaryByteCount)
	if err != nil {
		return m, fmt.Errorf("%w: read manifest: %v", ErrSegmentCorrupt, err)
	}
	if m.FormatVersion != domain.SegmentFormatV1 {
		return m, fmt.Errorf("%w: %d", ErrSegmentUnsupportedFormat, m.FormatVersion)
	}
	if (expectedBasename != "" && m.Basename != expectedBasename) || len(digest) != sha256.Size || len(summaryDigest) != sha256.Size || m.SummaryVersion != domain.SegmentSummaryV1 {
		return m, fmt.Errorf("%w: manifest identity", ErrSegmentCorrupt)
	}
	m.LogicalDigest = hex.EncodeToString(digest)
	m.SummaryDigest = hex.EncodeToString(summaryDigest)
	return m, nil
}

// VerifyArchiveSegmentMetadata validates identity and bounded immutable summary
// tables without loading or decoding any history_units payload.
func VerifyArchiveSegmentMetadata(ctx context.Context, root string, expected ArchiveSegmentManifest, limits ArchiveSegmentLimits) (ArchiveSegmentManifest, error) {
	if err := limits.validate(); err != nil {
		return ArchiveSegmentManifest{}, err
	}
	path, err := safeArchivePath(root, expected.Basename, true)
	if err != nil {
		return ArchiveSegmentManifest{}, err
	}
	return verifyArchiveSegmentMetadataPath(ctx, path, expected, limits, true)
}

func verifyArchiveSegmentMetadataPath(ctx context.Context, path string, expected ArchiveSegmentManifest, limits ArchiveSegmentLimits, requireSealed bool) (ArchiveSegmentManifest, error) {
	db, pinned, err := openPinnedImmutableSegment(ctx, path, requireSealed)
	if err != nil {
		return ArchiveSegmentManifest{}, err
	}
	defer func() { _ = db.Close(); _ = pinned.Close() }()
	return verifyArchiveSegmentMetadataOpen(ctx, db, pinned, expected, limits)
}

func verifyArchiveSegmentMetadataOpen(ctx context.Context, db *sql.DB, pinned *os.File, expected ArchiveSegmentManifest, limits ArchiveSegmentLimits) (ArchiveSegmentManifest, error) {
	m, err := inspectArchiveSegmentOpen(ctx, db, pinned, expected.Basename, limits.MaxFileBytes)
	if err != nil {
		return m, err
	}
	if !sameLogicalManifest(m, expected) || m.SummaryRowCount > limits.MaxSummaryRows || m.SummaryByteCount > limits.MaxSummaryBytes {
		return m, fmt.Errorf("%w: metadata manifest or cap", ErrSegmentCorrupt)
	}
	digest, err := hex.DecodeString(m.LogicalDigest)
	if err != nil || len(digest) != sha256.Size {
		return m, fmt.Errorf("%w: identity digest", ErrSegmentCorrupt)
	}
	var identityDigest [sha256.Size]byte
	copy(identityDigest[:], digest)
	identity, err := domain.NewSegmentIdentity(m.StoreID, m.StartSequence, m.EndSequence, identityDigest)
	if err != nil || identity.Basename() != m.Basename {
		return m, fmt.Errorf("%w: content-addressed identity", ErrSegmentCorrupt)
	}
	if err = validateSegmentTableSet(ctx, db); err != nil {
		return m, err
	}
	var singletonCount, canonicalLength, summaryDigestLength int64
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(length(canonical_bytes)),0),COALESCE(max(length(summary_digest)),0) FROM segment_summary`).Scan(&singletonCount, &canonicalLength, &summaryDigestLength); err != nil || singletonCount != 1 || canonicalLength != m.SummaryByteCount || canonicalLength > limits.MaxSummaryBytes || summaryDigestLength != sha256.Size {
		return m, fmt.Errorf("%w: summary singleton or preflight", ErrSegmentCorrupt)
	}
	var exactCount, bloomCount, sessionCount uint64
	var exactBytes, bloomBytes, sessionBytes int64
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(token)),0) FROM segment_exact_filters WHERE length(token)=?`, sha256.Size).Scan(&exactCount, &exactBytes); err != nil {
		return m, fmt.Errorf("%w: exact preflight", ErrSegmentCorrupt)
	}
	var allExact uint64
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM segment_exact_filters`).Scan(&allExact); err != nil || allExact != exactCount {
		return m, fmt.Errorf("%w: exact token shape", ErrSegmentCorrupt)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(bits)),0) FROM segment_bloom_filters WHERE bit_count>0 AND hash_count>0 AND bit_count=length(bits)*8`).Scan(&bloomCount, &bloomBytes); err != nil {
		return m, fmt.Errorf("%w: bloom preflight", ErrSegmentCorrupt)
	}
	var allBloom uint64
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM segment_bloom_filters`).Scan(&allBloom); err != nil || allBloom != bloomCount {
		return m, fmt.Errorf("%w: bloom shape", ErrSegmentCorrupt)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(session_token)),0) FROM segment_session_aggregates WHERE length(session_token)=? AND unit_count>0`, sha256.Size).Scan(&sessionCount, &sessionBytes); err != nil {
		return m, fmt.Errorf("%w: session preflight", ErrSegmentCorrupt)
	}
	var allSessions uint64
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM segment_session_aggregates`).Scan(&allSessions); err != nil || allSessions != sessionCount {
		return m, fmt.Errorf("%w: session shape", ErrSegmentCorrupt)
	}
	rows := exactCount + bloomCount + sessionCount
	normalizedBytes := exactBytes
	if exactBytes < 0 || bloomBytes < 0 || sessionBytes < 0 || normalizedBytes > limits.MaxSummaryBytes-bloomBytes {
		return m, fmt.Errorf("%w: summary aggregate preflight", ErrSegmentLimit)
	}
	normalizedBytes += bloomBytes
	if normalizedBytes > limits.MaxSummaryBytes-sessionBytes {
		return m, fmt.Errorf("%w: summary aggregate preflight", ErrSegmentLimit)
	}
	normalizedBytes += sessionBytes
	if rows != m.SummaryRowCount || rows > limits.MaxSummaryRows || canonicalLength > limits.MaxSummaryBytes-normalizedBytes {
		return m, fmt.Errorf("%w: summary aggregate preflight", ErrSegmentLimit)
	}
	var version uint32
	var keyID string
	var complete bool
	var canonical, storedDigest []byte
	if err = db.QueryRowContext(ctx, `SELECT summary_version,filter_key_id,time_summary_complete,canonical_bytes,summary_digest FROM segment_summary`).Scan(&version, &keyID, &complete, &canonical, &storedDigest); err != nil {
		return m, fmt.Errorf("%w: summary descriptor", ErrSegmentCorrupt)
	}
	if int64(len(canonical)) != m.SummaryByteCount || int64(len(canonical)) > limits.MaxSummaryBytes || version != m.SummaryVersion || keyID != m.FilterKeyID || complete != m.TimeSummaryComplete || !bytes.Equal(digestBytes(canonical), storedDigest) || hex.EncodeToString(storedDigest) != m.SummaryDigest {
		return m, fmt.Errorf("%w: summary binding", ErrSegmentCorrupt)
	}
	rebuilt := domain.SegmentCatalogSummaryV1{FilterKeyID: keyID, TimeComplete: complete}
	exactRows, queryErr := db.QueryContext(ctx, `SELECT kind,token FROM segment_exact_filters ORDER BY kind,token`)
	if queryErr != nil {
		return m, fmt.Errorf("%w: exact filters", ErrSegmentCorrupt)
	}
	for exactRows.Next() {
		var kind uint8
		var raw []byte
		if queryErr = exactRows.Scan(&kind, &raw); queryErr != nil || len(raw) != sha256.Size {
			_ = exactRows.Close()
			return m, fmt.Errorf("%w: exact filter", ErrSegmentCorrupt)
		}
		var value [sha256.Size]byte
		copy(value[:], raw)
		rebuilt.ExactTokens = append(rebuilt.ExactTokens, domain.SegmentSummaryToken{Kind: domain.SummaryTokenKind(kind), Value: value})
	}
	if queryErr = exactRows.Close(); queryErr != nil {
		return m, queryErr
	}
	bloomRows, queryErr := db.QueryContext(ctx, `SELECT kind,bit_count,hash_count,bits FROM segment_bloom_filters ORDER BY kind`)
	if queryErr != nil {
		return m, fmt.Errorf("%w: bloom filters", ErrSegmentCorrupt)
	}
	for bloomRows.Next() {
		var kind uint8
		var bits uint32
		var hashes uint8
		var raw []byte
		if queryErr = bloomRows.Scan(&kind, &bits, &hashes, &raw); queryErr != nil {
			_ = bloomRows.Close()
			return m, fmt.Errorf("%w: bloom filter", ErrSegmentCorrupt)
		}
		rebuilt.Blooms = append(rebuilt.Blooms, domain.SegmentBloomV1{Kind: domain.SummaryTokenKind(kind), BitCount: bits, HashCount: hashes, Bits: raw})
	}
	if queryErr = bloomRows.Close(); queryErr != nil {
		return m, queryErr
	}
	sessionRows, queryErr := db.QueryContext(ctx, `SELECT session_token,unit_count,audit_count FROM segment_session_aggregates ORDER BY session_token`)
	if queryErr != nil {
		return m, fmt.Errorf("%w: session aggregates", ErrSegmentCorrupt)
	}
	for sessionRows.Next() {
		var raw []byte
		var units, audits uint64
		if queryErr = sessionRows.Scan(&raw, &units, &audits); queryErr != nil || len(raw) != sha256.Size {
			_ = sessionRows.Close()
			return m, fmt.Errorf("%w: session aggregate", ErrSegmentCorrupt)
		}
		var token [sha256.Size]byte
		copy(token[:], raw)
		rebuilt.Sessions = append(rebuilt.Sessions, domain.SegmentSessionAggregateV1{SessionToken: token, UnitCount: units, AuditCount: audits})
	}
	if queryErr = sessionRows.Close(); queryErr != nil {
		return m, queryErr
	}
	rebuiltBytes, rebuildErr := rebuilt.CanonicalBytes(limits.MaxSummaryBytes)
	if rebuildErr != nil || !bytes.Equal(rebuiltBytes, canonical) {
		return m, fmt.Errorf("%w: normalized summary mismatch", ErrSegmentCorrupt)
	}
	if expected.FileDigest != "" {
		actual, digestErr := digestOpenFile(ctx, pinned, limits.MaxFileBytes)
		if digestErr != nil {
			return m, digestErr
		}
		if actual != expected.FileDigest {
			return m, fmt.Errorf("%w: expected file digest mismatch", ErrSegmentCorrupt)
		}
		m.FileDigest = actual
	}
	return m, nil
}

func validateSegmentTableSet(ctx context.Context, db *sql.DB) error {
	var userVersion uint32
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil || userVersion != domain.SegmentFormatV1 {
		return fmt.Errorf("%w: segment schema version", ErrSegmentCorrupt)
	}
	expected := make(map[string]string)
	for _, statement := range strings.Split(segmentSchemaV1, ";") {
		normalized := strings.Join(strings.Fields(statement), " ")
		if normalized == "" {
			continue
		}
		fields := strings.Fields(normalized)
		if len(fields) < 3 || fields[0] != "CREATE" || fields[1] != "TABLE" {
			return fmt.Errorf("invalid built-in segment schema")
		}
		name := strings.SplitN(fields[2], "(", 2)[0]
		expected[name] = normalized
	}
	rows, err := db.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_schema ORDER BY type,name`)
	if err != nil {
		return fmt.Errorf("%w: inspect summary schema", ErrSegmentCorrupt)
	}
	defer func() { _ = rows.Close() }()
	seen := make(map[string]bool)
	for rows.Next() {
		var typ, name string
		var sqlText sql.NullString
		if err = rows.Scan(&typ, &name, &sqlText); err != nil {
			return fmt.Errorf("%w: inspect summary schema", ErrSegmentCorrupt)
		}
		if typ == "index" && !sqlText.Valid && strings.HasPrefix(name, "sqlite_autoindex_") {
			continue
		}
		want, ok := expected[name]
		if !ok || typ != "table" || !sqlText.Valid || strings.Join(strings.Fields(sqlText.String), " ") != want {
			return fmt.Errorf("%w: unexpected segment schema object", ErrSegmentCorrupt)
		}
		seen[name] = true
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("%w: incomplete segment schema", ErrSegmentCorrupt)
	}
	return nil
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
	return verifyArchiveSegmentPathWithHook(ctx, path, expected, limits, requireFileDigest, nil)
}

func verifyArchiveSegmentPathWithHook(ctx context.Context, path string, expected ArchiveSegmentManifest, limits ArchiveSegmentLimits, requireFileDigest bool, afterManifest func() error) (ArchiveSegmentManifest, error) {
	db, pinned, err := openPinnedImmutableSegment(ctx, path, requireFileDigest)
	if err != nil {
		return ArchiveSegmentManifest{}, err
	}
	defer func() { _ = db.Close(); _ = pinned.Close() }()
	m, err := inspectArchiveSegmentOpen(ctx, db, pinned, expected.Basename, limits.MaxFileBytes)
	if err != nil {
		return m, err
	}
	if afterManifest != nil {
		if err = afterManifest(); err != nil {
			return m, err
		}
	}
	if !sameLogicalManifest(m, expected) {
		return m, fmt.Errorf("%w: expected manifest mismatch", ErrSegmentCorrupt)
	}
	if _, metadataErr := verifyArchiveSegmentMetadataOpen(ctx, db, pinned, expected, limits); metadataErr != nil {
		return m, metadataErr
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
	if requireFileDigest && expected.FileDigest == "" {
		return m, fmt.Errorf("%w: expected file digest missing", ErrSegmentCorrupt)
	}
	// Preflight lengths on the already-open connection before requesting any
	// payload BLOB. Keeping one connection is essential: reopening /dev/fd on
	// Linux may resolve through a pathname that has since been exchanged.
	preflight, err := db.QueryContext(ctx, `SELECT sequence,plaintext_length,length(payload) FROM history_units ORDER BY sequence`)
	if err != nil {
		return m, fmt.Errorf("%w: preflight payloads: %v", ErrSegmentCorrupt, err)
	}
	var preCount, prePrevious uint64
	var prePlain, preStored int64
	for preflight.Next() {
		var seq uint64
		var plainLen, storedLen int64
		if err = preflight.Scan(&seq, &plainLen, &storedLen); err != nil {
			_ = preflight.Close()
			return m, fmt.Errorf("%w: preflight payload", ErrSegmentCorrupt)
		}
		if preCount > 0 && seq != prePrevious+1 {
			_ = preflight.Close()
			return m, fmt.Errorf("%w: non-contiguous sequence", ErrSegmentCorrupt)
		}
		if plainLen < 0 || plainLen > limits.MaxValuePlainBytes || storedLen < 0 || storedLen > limits.MaxValueStoredBytes || plainLen > limits.MaxTotalPlainBytes-prePlain || storedLen > limits.MaxTotalStoredBytes-preStored {
			_ = preflight.Close()
			return m, fmt.Errorf("%w: value", ErrSegmentLimit)
		}
		prePrevious = seq
		preCount++
		prePlain += plainLen
		preStored += storedLen
	}
	if err = preflight.Err(); err != nil {
		_ = preflight.Close()
		return m, err
	}
	if err = preflight.Close(); err != nil {
		return m, err
	}
	if preCount != m.UnitCount || prePlain != m.TotalPlainBytes || preStored != m.TotalStoredBytes {
		return m, fmt.Errorf("%w: payload preflight aggregate", ErrSegmentCorrupt)
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
	_, _ = logical.Write([]byte("traceary-segment-history-v1\x00"))
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
		var checksum []byte
		var stored []byte
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
		storedLen := int64(len(stored))
		if plainLen < 0 || plainLen > limits.MaxValuePlainBytes || storedLen < 0 || storedLen > limits.MaxValueStoredBytes || totalPlain+plainLen > limits.MaxTotalPlainBytes || totalStored+storedLen > limits.MaxTotalStoredBytes {
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
		if !unit.CreatedAt().IsZero() && (minCreatedAt.IsZero() || unit.CreatedAt().Before(minCreatedAt)) {
			minCreatedAt = unit.CreatedAt()
		}
		if !unit.CreatedAt().IsZero() && (maxCreatedAt.IsZero() || unit.CreatedAt().After(maxCreatedAt)) {
			maxCreatedAt = unit.CreatedAt()
		}
		totalPlain += int64(len(plain))
		totalStored += int64(len(stored))
		if totalPlain > limits.MaxTotalPlainBytes || totalStored > limits.MaxTotalStoredBytes {
			return m, fmt.Errorf("%w: aggregate", ErrSegmentLimit)
		}
		_, _ = logical.Write(binary.BigEndian.AppendUint64(nil, uint64(len(plain))))
		_, _ = logical.Write(plain)
		count++
	}
	if err = rows.Err(); err != nil {
		return m, err
	}
	var summaryBytes []byte
	if err = db.QueryRowContext(ctx, `SELECT canonical_bytes FROM segment_summary`).Scan(&summaryBytes); err != nil {
		return m, fmt.Errorf("%w: summary frame", ErrSegmentCorrupt)
	}
	_, _ = logical.Write([]byte("traceary-segment-summary-v1\x00"))
	_, _ = logical.Write(binary.BigEndian.AppendUint64(nil, uint64(len(summaryBytes))))
	_, _ = logical.Write(summaryBytes)
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
