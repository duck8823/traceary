package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func testSegmentLimits() ArchiveSegmentLimits {
	return ArchiveSegmentLimits{MaxValuePlainBytes: 1 << 20, MaxValueStoredBytes: 1 << 20, MaxTotalPlainBytes: 4 << 20, MaxTotalStoredBytes: 4 << 20, MaxFileBytes: 16 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000}
}

func testSegmentSummary() domain.SegmentCatalogSummaryV1 {
	return domain.SegmentCatalogSummaryV1{FilterKeyID: "test-key-v1", TimeComplete: true,
		ExactTokens: []domain.SegmentSummaryToken{{Kind: domain.SummaryTokenWorkspace, Value: sha256.Sum256([]byte("workspace"))}},
		Blooms:      []domain.SegmentBloomV1{{Kind: domain.SummaryTokenSession, BitCount: 8, HashCount: 2, Bits: []byte{0x81}}},
		Sessions:    []domain.SegmentSessionAggregateV1{{SessionToken: sha256.Sum256([]byte("session")), UnitCount: 2, AuditCount: 1}},
	}
}

func TestArchiveSegmentMetadataVerificationDoesNotDecodePayload(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "metadata", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyArchiveSegmentMetadata(context.Background(), root, m, testSegmentLimits()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, m.Basename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE history_units SET codec='future'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	metadataExpected := m
	metadataExpected.FileDigest = ""
	if _, err = VerifyArchiveSegmentMetadata(context.Background(), root, metadataExpected, testSegmentLimits()); err != nil {
		t.Fatalf("metadata verifier decoded payload: %v", err)
	}
	if _, err = VerifyArchiveSegment(context.Background(), root, m, testSegmentLimits()); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("full verifier error = %v", err)
	}
}

func TestArchiveSegmentMetadataVerifierBindsNormalizedSummary(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "summary-binding", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, m.Basename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE segment_session_aggregates SET unit_count=3`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	expected := m
	expected.FileDigest = ""
	if _, err = VerifyArchiveSegmentMetadata(context.Background(), root, expected, testSegmentLimits()); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("tampered summary error = %v", err)
	}
}

func TestArchiveSegmentMixedMalformedTimeMarksSummaryIncomplete(t *testing.T) {
	units := testArchiveUnits()
	values := units[1].Unit.Event.Values()
	values[5] = domain.TextValue([]byte("not-a-time"))
	malformed, err := domain.NewArchiveEventV1(values)
	if err != nil {
		t.Fatal(err)
	}
	units[1].Unit.Event = malformed
	m, err := BuildArchiveSegmentV1(context.Background(), t.TempDir(), units, ArchiveSegmentConfig{StoreID: "mixed-time", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	if m.TimeSummaryComplete {
		t.Fatal("mixed valid and malformed timestamps reported complete")
	}
}

func TestArchiveSegmentMetadataRejectsUnexpectedPlaintextTable(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "schema", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, m.Basename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE leaked_plaintext(value TEXT); INSERT INTO leaked_plaintext VALUES('secret')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	expected := m
	expected.FileDigest = ""
	if _, err = VerifyArchiveSegmentMetadata(context.Background(), root, expected, testSegmentLimits()); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("unexpected schema error = %v", err)
	}
}

func TestArchiveSegmentMetadataPreflightsHugeBloomBeforeBlobScan(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "huge-summary", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, m.Basename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE segment_bloom_filters SET bit_count=8388608,bits=zeroblob(1048576)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	expected := m
	expected.FileDigest = ""
	limits := testSegmentLimits()
	limits.MaxSummaryBytes = 1024
	if _, err = VerifyArchiveSegmentMetadata(context.Background(), root, expected, limits); !errors.Is(err, ErrSegmentLimit) {
		t.Fatalf("huge summary error = %v", err)
	}
}

func TestArchiveSegmentFullVerifierPinsOneInodeAcrossPathExchange(t *testing.T) {
	root := t.TempDir()
	cfg := ArchiveSegmentConfig{StoreID: "pinned", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()}
	original, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	replacementRoot := t.TempDir()
	replacement, err := BuildArchiveSegmentV1(context.Background(), replacementRoot, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "replacement", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, original.Basename)
	_, err = verifyArchiveSegmentPathWithHook(context.Background(), path, original, testSegmentLimits(), true, func() error { return os.Rename(filepath.Join(replacementRoot, replacement.Basename), path) })
	if err != nil {
		t.Fatalf("pinned verifier mixed path inodes: %v", err)
	}
}

func testArchiveUnits() []ArchiveHistoryUnit {
	return []ArchiveHistoryUnit{
		{Unit: domain.HistoryUnit{Sequence: 10, Event: testArchiveEvent("event-10", time.Unix(2, 3), domain.TextValue([]byte(strings.Repeat("compressible-", 100))))}},
		{Unit: domain.HistoryUnit{Sequence: 11, Event: testArchiveEvent("event-11", time.Unix(4, 5), domain.TextValue([]byte("body"))), Audit: testArchiveAudit()}},
	}
}

func testArchiveEvent(id string, created time.Time, body domain.SQLiteValue) domain.ArchiveEventV1 {
	values := make([]domain.SQLiteValue, 23)
	for i := range values {
		values[i] = domain.NullValue()
	}
	values[0] = domain.TextValue([]byte(id))
	values[4] = body
	values[5] = domain.TextValue([]byte(created.UTC().Format(time.RFC3339Nano)))
	event, err := domain.NewArchiveEventV1(values)
	if err != nil {
		panic(err)
	}
	return event
}
func testArchiveAudit() *domain.ArchiveAuditV1 {
	values := make([]domain.SQLiteValue, 27)
	for i := range values {
		values[i] = domain.NullValue()
	}
	values[0] = domain.TextValue([]byte("cmd"))
	audit, err := domain.NewArchiveAuditV1(values)
	if err != nil {
		panic(err)
	}
	return &audit
}

func TestArchiveSegmentBuildInspectAndVerify(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "store-a", CompressionFloor: 500, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	if m.ZstdValueCount == 0 || m.PlainValueCount == 0 || m.AuditCount != 1 {
		t.Fatalf("unexpected codec/count facts: %+v", m)
	}
	path := filepath.Join(root, m.Basename)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	inspected, err := InspectArchiveSegmentManifest(context.Background(), root, m.Basename, testSegmentLimits())
	if err != nil {
		t.Fatal(err)
	}
	if inspected.LogicalDigest != m.LogicalDigest || inspected.FileDigest != "" {
		t.Fatalf("inspection mismatch: %+v", inspected)
	}
	verified, err := VerifyArchiveSegment(context.Background(), root, m, testSegmentLimits())
	if err != nil {
		t.Fatal(err)
	}
	if verified.Basename != m.Basename {
		t.Fatalf("verified basename = %q", verified.Basename)
	}
}

func TestArchiveSegmentTimeBoundsUseChronologicalOrder(t *testing.T) {
	units := testArchiveUnits()
	units[0].Unit.Event = testArchiveEvent("event-10", time.Unix(10, 1), domain.TextValue([]byte("body")))
	units[1].Unit.Event = testArchiveEvent("event-11", time.Unix(10, 0), domain.TextValue([]byte("body")))
	m, err := BuildArchiveSegmentV1(context.Background(), t.TempDir(), units, ArchiveSegmentConfig{StoreID: "time", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	if m.MinCreatedAt != time.Unix(10, 0).UTC().Format(time.RFC3339Nano) || m.MaxCreatedAt != time.Unix(10, 1).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("time bounds = %s .. %s", m.MinCreatedAt, m.MaxCreatedAt)
	}
}

func TestArchiveSegmentVerifierBindsExpectedManifestAndCaps(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "binding", CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	tampered := m
	tampered.FileDigest = strings.Repeat("0", 64)
	if _, err = VerifyArchiveSegment(context.Background(), root, tampered, testSegmentLimits()); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("file digest tamper error = %v", err)
	}
	info, err := os.Stat(filepath.Join(root, m.Basename))
	if err != nil {
		t.Fatal(err)
	}
	fileLimited := testSegmentLimits()
	fileLimited.MaxFileBytes = info.Size() - 1
	if _, err = VerifyArchiveSegment(context.Background(), root, m, fileLimited); !errors.Is(err, ErrSegmentLimit) {
		t.Fatalf("file cap error = %v", err)
	}
	for name, limits := range map[string]ArchiveSegmentLimits{
		"aggregate plaintext": {MaxValuePlainBytes: 1 << 20, MaxValueStoredBytes: 1 << 20, MaxTotalPlainBytes: m.TotalPlainBytes - 1, MaxTotalStoredBytes: 4 << 20, MaxFileBytes: 16 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000},
		"aggregate stored":    {MaxValuePlainBytes: 1 << 20, MaxValueStoredBytes: 1 << 20, MaxTotalPlainBytes: 4 << 20, MaxTotalStoredBytes: m.TotalStoredBytes - 1, MaxFileBytes: 16 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000},
		"decoded value":       {MaxValuePlainBytes: 16, MaxValueStoredBytes: 1 << 20, MaxTotalPlainBytes: 4 << 20, MaxTotalStoredBytes: 4 << 20, MaxFileBytes: 16 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyArchiveSegment(context.Background(), root, m, limits); !errors.Is(err, ErrSegmentLimit) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if err = os.Chmod(filepath.Join(root, m.Basename), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectArchiveSegmentManifest(context.Background(), root, m.Basename, testSegmentLimits()); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("unsealed mode error = %v", err)
	}
}

func TestArchiveSegmentLogicalOutputIsDeterministic(t *testing.T) {
	cfg := ArchiveSegmentConfig{StoreID: "same", CompressionFloor: 20, Limits: testSegmentLimits(), Summary: testSegmentSummary()}
	a, err := BuildArchiveSegmentV1(context.Background(), t.TempDir(), testArchiveUnits(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildArchiveSegmentV1(context.Background(), t.TempDir(), testArchiveUnits(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.FileDigest, b.FileDigest = "", ""
	if a != b {
		t.Fatalf("logical manifests differ:\n%+v\n%+v", a, b)
	}
}

func TestArchiveSegmentRejectsUnsafeLocations(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"/tmp/x", "../segment-v1-" + strings.Repeat("a", 64) + ".sqlite", "not-content-addressed.sqlite"} {
		if _, err := InspectArchiveSegmentManifest(context.Background(), root, name, testSegmentLimits()); !errors.Is(err, ErrSegmentUnsafeLocation) {
			t.Fatalf("%q error = %v", name, err)
		}
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "archive")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildArchiveSegmentV1(context.Background(), link, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits(), Summary: testSegmentSummary()}); !errors.Is(err, ErrSegmentUnsafeLocation) {
		t.Fatalf("symlink root error = %v", err)
	}
	name := "segment-v1-" + strings.Repeat("a", 64) + ".sqlite"
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectArchiveSegmentManifest(context.Background(), root, name, testSegmentLimits()); !errors.Is(err, ErrSegmentUnsafeLocation) {
		t.Fatalf("symlink file error = %v", err)
	}
}

func TestArchiveSegmentCapsCancellationAndIncompleteOutput(t *testing.T) {
	cfg := ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits(), Summary: testSegmentSummary()}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildArchiveSegmentV1(ctx, root, testArchiveUnits(), cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	assertNoSegmentFiles(t, root)
	cfg.Limits.MaxValuePlainBytes = 4
	if _, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), cfg); !errors.Is(err, ErrSegmentLimit) {
		t.Fatalf("limit error = %v", err)
	}
	assertNoSegmentFiles(t, root)
	unit := testArchiveUnits()[:1]
	encoded, err := unit[0].Unit.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	exact := ArchiveSegmentLimits{MaxValuePlainBytes: int64(len(encoded)), MaxValueStoredBytes: int64(len(encoded)), MaxTotalPlainBytes: int64(len(encoded)), MaxTotalStoredBytes: int64(len(encoded)), MaxFileBytes: 16 << 20, MaxSummaryBytes: 1 << 20, MaxSummaryRows: 1000}
	if _, err = BuildArchiveSegmentV1(context.Background(), t.TempDir(), unit, ArchiveSegmentConfig{StoreID: "exact", CompressionFloor: len(encoded) + 1, Limits: exact, Summary: testSegmentSummary()}); err != nil {
		t.Fatalf("exact maximum rejected: %v", err)
	}
}

func TestArchiveSegmentFsyncFailureDoesNotSeal(t *testing.T) {
	root := t.TempDir()
	sentinel := errors.New("sync failed")
	_, err := (archiveSegmentBuilder{syncFile: func(*os.File) error { return sentinel }}).build(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
	assertNoSegmentFiles(t, root)
}

func TestArchiveSegmentMidOperationCancellationAndSealFailureLeaveNoOutput(t *testing.T) {
	cfg := ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits(), Summary: testSegmentSummary()}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	b := archiveSegmentBuilder{syncFile: func(f *os.File) error { return f.Sync() }, sealFile: func(f *os.File, m os.FileMode) error { return f.Chmod(m) }, afterUnit: func(i int) error {
		if i == 0 {
			cancel()
		}
		return ctx.Err()
	}}
	if _, err := b.build(ctx, root, testArchiveUnits(), cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-operation error = %v", err)
	}
	assertNoSegmentFiles(t, root)
	root = t.TempDir()
	sentinel := errors.New("seal failed")
	b = archiveSegmentBuilder{syncFile: func(f *os.File) error { return f.Sync() }, sealFile: func(*os.File, os.FileMode) error { return sentinel }}
	if _, err := b.build(context.Background(), root, testArchiveUnits(), cfg); !errors.Is(err, sentinel) {
		t.Fatalf("seal error = %v", err)
	}
	assertNoSegmentFiles(t, root)
}

func TestArchiveSegmentVerifierSeparatesUnknownCodecAndCorruption(t *testing.T) {
	for _, tc := range []struct {
		name, codec string
		corrupt     bool
		floor       int
		want        error
	}{
		{name: "unknown codec", codec: "future", floor: 1 << 20, want: ErrSegmentUnsupportedCodec},
		{name: "corrupt zstd payload", codec: segmentCodecZstd, corrupt: true, floor: 1, want: ErrSegmentCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: tc.floor, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, m.Basename)
			if err = os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
			if err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				_, err = db.Exec(`UPDATE history_units SET payload=x'00' WHERE sequence=10`)
			} else {
				_, err = db.Exec(`UPDATE history_units SET codec=? WHERE sequence=10`, tc.codec)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(path, 0o400); err != nil {
				t.Fatal(err)
			}
			m.FileDigest, err = digestFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = VerifyArchiveSegment(context.Background(), root, m, testSegmentLimits())
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestArchiveSegmentVerifierRejectsOversizedStoredPayloadBeforeLoadingBlob(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "oversized", CompressionFloor: 500, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, m.Basename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE history_units SET payload=zeroblob(2097152), plaintext_length=2097152 WHERE sequence=10`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	m.FileDigest, err = digestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	limits := testSegmentLimits()
	limits.MaxValueStoredBytes = 1024
	if _, err = VerifyArchiveSegment(context.Background(), root, m, limits); !errors.Is(err, ErrSegmentLimit) {
		t.Fatalf("error = %v", err)
	}
}

func TestArchiveSegmentRejectsUnknownFormatButManifestInspectionDoesNotDecodePayload(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, m.Basename)
	_ = os.Chmod(path, 0o600)
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE history_units SET codec='future'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_ = os.Chmod(path, 0o400)
	if _, err = InspectArchiveSegmentManifest(context.Background(), root, m.Basename, testSegmentLimits()); err != nil {
		t.Fatalf("manifest inspection decoded payload: %v", err)
	}
	_ = os.Chmod(path, 0o600)
	db, err = sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE segment_manifest SET format_version=2`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_ = os.Chmod(path, 0o400)
	if _, err = InspectArchiveSegmentManifest(context.Background(), root, m.Basename, testSegmentLimits()); !errors.Is(err, ErrSegmentUnsupportedFormat) {
		t.Fatalf("error = %v", err)
	}
}

func assertNoSegmentFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sqlite") || strings.Contains(e.Name(), "candidate") {
			t.Fatalf("incomplete output remains: %s", e.Name())
		}
	}
}
