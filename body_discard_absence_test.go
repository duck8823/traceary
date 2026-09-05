package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

var bodyAvailabilityPattern = regexp.MustCompile(`BodyAvailability|body_availability`)
var rawBodyRetentionPattern = regexp.MustCompile(`raw_body_retention|RawBodyRetention`)
var coverRefusePattern = regexp.MustCompile(`ForceCoverSafeToDelete|refuse-unrefined|Unlimited`)
var orphanPattern = regexp.MustCompile(`session_orphan_ranges|OrphanRange|OrphanConsolidation`)

// Exact-path exemptions for the 083 semantic verifier and the migration-only
// decoder / pending-aware 082 survivor list. Shared with the #2328 migration-only
// decoder exemption: these names exist so 083 can drop the objects, not so live
// runtime can discard bodies.
var allowListedBodyAvailabilityFiles = map[string]bool{
	filepath.Join("infrastructure", "sqlite", "drop_body_retention.go"):        true,
	filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"): true,
	filepath.Join("infrastructure", "sqlite", "prepared_upgrade_verifier.go"):  true,
}

var allowListedRawBodyRetentionFiles = map[string]bool{
	filepath.Join("infrastructure", "sqlite", "drop_body_retention.go"):        true,
	filepath.Join("infrastructure", "sqlite", "drop_encoded_payloads.go"):      true,
	filepath.Join("infrastructure", "sqlite", "prepared_upgrade_verifier.go"):  true,
	filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"): true,
}

var allowListedOrphanFiles = map[string]bool{
	filepath.Join("infrastructure", "sqlite", "drop_body_retention.go"):        true,
	filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"): true,
}

func TestDroppedBodyDiscardSymbolsAreAbsentFromRuntimeSources(t *testing.T) {
	t.Parallel()
	assertRuntimeSymbolAbsence(t, bodyAvailabilityPattern, allowListedBodyAvailabilityFiles, "BodyAvailability/body_availability")
	assertRuntimeSymbolAbsence(t, rawBodyRetentionPattern, allowListedRawBodyRetentionFiles, "raw_body_retention/RawBodyRetention")
	assertRuntimeSymbolAbsence(t, coverRefusePattern, map[string]bool{}, "ForceCoverSafeToDelete/refuse-unrefined/Unlimited")
	assertRuntimeSymbolAbsence(t, orphanPattern, allowListedOrphanFiles, "session_orphan_ranges/OrphanRange/OrphanConsolidation")
}

func TestAllowListedBodyDiscardFilesStillNameTheDroppedObjects(t *testing.T) {
	t.Parallel()
	for path := range allowListedBodyAvailabilityFiles {
		assertFileMatches(t, path, bodyAvailabilityPattern)
	}
	for path := range allowListedRawBodyRetentionFiles {
		assertFileMatches(t, path, rawBodyRetentionPattern)
	}
	for path := range allowListedOrphanFiles {
		assertFileMatches(t, path, orphanPattern)
	}
}

func TestSessionRefinementRuntimeSymbolRemains(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(`SessionRefinement`)
	found := false
	err := filepath.WalkDir("application", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return xerrors.Errorf("read %s: %w", path, readErr)
		}
		if pattern.Match(contents) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("SessionRefinement disappeared from application runtime sources")
	}
}

func TestBodyDiscardExemptionsAreUnreachableFromOrdinaryDI(t *testing.T) {
	t.Parallel()
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if bodyAvailabilityPattern.Match(mainSrc) || rawBodyRetentionPattern.Match(mainSrc) ||
		coverRefusePattern.Match(mainSrc) || orphanPattern.Match(mainSrc) {
		t.Fatal("main.go still names a dropped body-discard symbol")
	}
}

func assertRuntimeSymbolAbsence(t *testing.T, pattern *regexp.Regexp, allow map[string]bool, label string) {
	t.Helper()
	roots := []string{"domain", "application", "presentation", "infrastructure", "main.go"}
	var unexpected []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			if allow[root] {
				continue
			}
			contents, readErr := os.ReadFile(root)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if loc := pattern.FindIndex(contents); loc != nil {
				unexpected = append(unexpected, root+": "+string(contents[loc[0]:loc[1]]))
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".sql") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(path, filepath.Join("schema", "sqlite", "migrations")) {
				return nil
			}
			if allow[path] {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return xerrors.Errorf("read %s: %w", path, readErr)
			}
			if loc := pattern.FindIndex(contents); loc != nil {
				unexpected = append(unexpected, path+": "+string(contents[loc[0]:loc[1]]))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to walk %s: %v", root, err)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("retired %s leaked into runtime sources:\n%s", label, strings.Join(unexpected, "\n"))
	}
}

func assertFileMatches(t *testing.T, path string, pattern *regexp.Regexp) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !pattern.Match(contents) {
		t.Fatalf("%s is allow-listed but no longer matches %s", path, pattern.String())
	}
}
