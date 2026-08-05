package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCatalogDatabase(t *testing.T) *Database {
	t.Helper()
	database := NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestArchiveSegmentCatalogRegisterAndListRoundtrip(t *testing.T) {
	ctx := context.Background()
	database := testCatalogDatabase(t)
	root := t.TempDir()
	manifest, err := BuildArchiveSegmentV1(ctx, root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "catalog", CompressionFloor: 32, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.RegisterArchiveSegment(ctx, root, manifest, testSegmentLimits()); err != nil {
		t.Fatal(err)
	}
	// At-least-once archive processing re-registers the identical manifest.
	if err = database.RegisterArchiveSegment(ctx, root, manifest, testSegmentLimits()); err != nil {
		t.Fatalf("idempotent re-registration failed: %v", err)
	}
	listed, err := database.ListArchiveSegments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0] != manifest {
		t.Fatalf("listed = %+v, want exactly the registered manifest", listed)
	}
}

func TestArchiveSegmentCatalogListsOverlappingRangesInSequenceOrder(t *testing.T) {
	ctx := context.Background()
	database := testCatalogDatabase(t)
	units := testArchiveUnits()
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first, err := BuildArchiveSegmentV1(ctx, firstRoot, units, ArchiveSegmentConfig{StoreID: "overlap", CompressionFloor: 32, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildArchiveSegmentV1(ctx, secondRoot, units[1:], ArchiveSegmentConfig{StoreID: "overlap", CompressionFloor: 32, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.RegisterArchiveSegment(ctx, secondRoot, second, testSegmentLimits()); err != nil {
		t.Fatal(err)
	}
	if err = database.RegisterArchiveSegment(ctx, firstRoot, first, testSegmentLimits()); err != nil {
		t.Fatalf("overlapping sequence range rejected: %v", err)
	}
	listed, err := database.ListArchiveSegments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0] != first || listed[1] != second {
		t.Fatalf("listed order = %+v", listed)
	}
}

func TestArchiveSegmentCatalogRejectsConflictWithoutMutatingRow(t *testing.T) {
	ctx := context.Background()
	database := testCatalogDatabase(t)
	units := testArchiveUnits()
	registeredRoot, rebuiltRoot := t.TempDir(), t.TempDir()
	registered, err := BuildArchiveSegmentV1(ctx, registeredRoot, units, ArchiveSegmentConfig{StoreID: "conflict", CompressionFloor: 32, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	// An at-least-once rebuild of the same logical content without compression
	// yields the same content-addressed basename but different sealed-file
	// evidence, so both manifests verify against their own installed files.
	rebuilt, err := BuildArchiveSegmentV1(ctx, rebuiltRoot, units, ArchiveSegmentConfig{StoreID: "conflict", CompressionFloor: 1 << 20, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Basename != registered.Basename || rebuilt == registered {
		t.Fatalf("rebuilt = %+v, want the basename of %+v with different evidence", rebuilt, registered)
	}
	if err = database.RegisterArchiveSegment(ctx, registeredRoot, registered, testSegmentLimits()); err != nil {
		t.Fatal(err)
	}
	if err = database.RegisterArchiveSegment(ctx, rebuiltRoot, rebuilt, testSegmentLimits()); !errors.Is(err, ErrSegmentCatalogConflict) {
		t.Fatalf("conflicting evidence error = %v", err)
	}
	listed, err := database.ListArchiveSegments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0] != registered {
		t.Fatalf("conflict mutated the stored row: %+v", listed)
	}
}

func TestArchiveSegmentCatalogRejectsUnverifiableManifests(t *testing.T) {
	ctx := context.Background()
	database := testCatalogDatabase(t)
	root := t.TempDir()
	manifest, err := BuildArchiveSegmentV1(ctx, root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "invalid", CompressionFloor: 32, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(m ArchiveSegmentManifest) ArchiveSegmentManifest{
		"missing file digest": func(m ArchiveSegmentManifest) ArchiveSegmentManifest { m.FileDigest = ""; return m },
		"invented file digest": func(m ArchiveSegmentManifest) ArchiveSegmentManifest {
			m.FileDigest = strings.Repeat("0", 64)
			return m
		},
		"missing logical digest": func(m ArchiveSegmentManifest) ArchiveSegmentManifest { m.LogicalDigest = ""; return m },
		"missing basename":       func(m ArchiveSegmentManifest) ArchiveSegmentManifest { m.Basename = ""; return m },
		"foreign basename": func(m ArchiveSegmentManifest) ArchiveSegmentManifest {
			m.StartSequence++
			m.EndSequence++
			return m
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if err := database.RegisterArchiveSegment(ctx, root, mutate(manifest), testSegmentLimits()); !errors.Is(err, ErrSegmentCatalogInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	listed, err := database.ListArchiveSegments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("invalid manifests were stored: %+v", listed)
	}
}

func TestArchiveSegmentCatalogRequiresInstalledSealedFile(t *testing.T) {
	ctx := context.Background()
	database := testCatalogDatabase(t)
	root := t.TempDir()
	manifest, err := BuildArchiveSegmentV1(ctx, root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: "installed", CompressionFloor: 32, Limits: testSegmentLimits()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, manifest.Basename)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = database.RegisterArchiveSegment(ctx, root, manifest, testSegmentLimits()); !errors.Is(err, ErrSegmentCatalogInvalid) {
		t.Fatalf("unsealed file error = %v", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = database.RegisterArchiveSegment(ctx, root, manifest, testSegmentLimits()); !errors.Is(err, ErrSegmentCatalogInvalid) {
		t.Fatalf("missing file error = %v", err)
	}
	listed, err := database.ListArchiveSegments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("uninstalled segment was registered: %+v", listed)
	}
}
