package sqlite

import (
	"context"
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
	return ArchiveSegmentLimits{MaxValuePlainBytes: 1 << 20, MaxValueStoredBytes: 1 << 20, MaxTotalPlainBytes: 4 << 20, MaxTotalStoredBytes: 4 << 20}
}

func testArchiveUnits() []ArchiveHistoryUnit {
	return []ArchiveHistoryUnit{
		{Unit: domain.HistoryUnit{Sequence: 10, Event: []domain.SQLiteValue{domain.NullValue(), domain.TextValue([]byte(strings.Repeat("compressible-", 100))), domain.BlobValue([]byte{0, 0xff})}}, CreatedAt: time.Unix(2, 3)},
		{Unit: domain.HistoryUnit{Sequence: 11, Event: []domain.SQLiteValue{domain.IntegerValue(-5), domain.RealValue(1.25)}, Audit: []domain.SQLiteValue{domain.TextValue([]byte("cmd"))}}, CreatedAt: time.Unix(4, 5)},
	}
}

func TestArchiveSegmentBuildInspectAndVerify(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "store-a", CompressionFloor: 32, Limits: testSegmentLimits()})
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
	inspected, err := InspectArchiveSegmentManifest(context.Background(), root, m.Basename)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.LogicalDigest != m.LogicalDigest || inspected.FileDigest == "" {
		t.Fatalf("inspection mismatch: %+v", inspected)
	}
	verified, err := VerifyArchiveSegment(context.Background(), root, m.Basename, testSegmentLimits())
	if err != nil {
		t.Fatal(err)
	}
	if verified.Basename != m.Basename {
		t.Fatalf("verified basename = %q", verified.Basename)
	}
}

func TestArchiveSegmentLogicalOutputIsDeterministic(t *testing.T) {
	cfg := ArchiveSegmentConfig{StoreID: "same", CompressionFloor: 20, Limits: testSegmentLimits()}
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
		if _, err := InspectArchiveSegmentManifest(context.Background(), root, name); !errors.Is(err, ErrSegmentUnsafeLocation) {
			t.Fatalf("%q error = %v", name, err)
		}
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "archive")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildArchiveSegmentV1(context.Background(), link, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits()}); !errors.Is(err, ErrSegmentUnsafeLocation) {
		t.Fatalf("symlink root error = %v", err)
	}
	name := "segment-v1-" + strings.Repeat("a", 64) + ".sqlite"
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectArchiveSegmentManifest(context.Background(), root, name); !errors.Is(err, ErrSegmentUnsafeLocation) {
		t.Fatalf("symlink file error = %v", err)
	}
}

func TestArchiveSegmentCapsCancellationAndIncompleteOutput(t *testing.T) {
	cfg := ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits()}
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
	exact := ArchiveSegmentLimits{MaxValuePlainBytes: int64(len(encoded)), MaxValueStoredBytes: int64(len(encoded)), MaxTotalPlainBytes: int64(len(encoded)), MaxTotalStoredBytes: int64(len(encoded))}
	if _, err = BuildArchiveSegmentV1(context.Background(), t.TempDir(), unit, ArchiveSegmentConfig{StoreID: "exact", CompressionFloor: len(encoded) + 1, Limits: exact}); err != nil {
		t.Fatalf("exact maximum rejected: %v", err)
	}
}

func TestArchiveSegmentFsyncFailureDoesNotSeal(t *testing.T) {
	root := t.TempDir()
	sentinel := errors.New("sync failed")
	_, err := (archiveSegmentBuilder{syncFile: func(*os.File) error { return sentinel }}).build(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits()})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
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
			m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: tc.floor, Limits: testSegmentLimits()})
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
			_, err = VerifyArchiveSegment(context.Background(), root, m.Basename, testSegmentLimits())
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestArchiveSegmentRejectsUnknownFormatButManifestInspectionDoesNotDecodePayload(t *testing.T) {
	root := t.TempDir()
	m, err := BuildArchiveSegmentV1(context.Background(), root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "x", CompressionFloor: 1, Limits: testSegmentLimits()})
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
	if _, err = InspectArchiveSegmentManifest(context.Background(), root, m.Basename); err != nil {
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
	if _, err = InspectArchiveSegmentManifest(context.Background(), root, m.Basename); !errors.Is(err, ErrSegmentUnsupportedFormat) {
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
