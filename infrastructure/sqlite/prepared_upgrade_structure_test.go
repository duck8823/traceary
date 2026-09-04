package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestCatalogStructuralCoverage(t *testing.T) {
	t.Parallel()
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := inventoryEmbeddedMigrations(migrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) == 0 {
		t.Fatal("empty catalog")
	}
	byVersion := map[int64]embeddedMigration{}
	for _, migration := range catalog {
		byVersion[migration.version] = migration
		law := conservationLawFor(migration.version)
		verifier := semanticVerifierFor(migration.version)
		if law == "" && verifier == "" {
			t.Errorf("catalog version %d has neither a Layer-1 law nor a Layer-2 verifier", migration.version)
		}
		if entry, ok := preparedMigrationManifest[migration.version]; ok {
			if entry.Name != migration.name || entry.SHA256 != migration.digest {
				t.Errorf("manifest/catalog mismatch at %d", migration.version)
			}
		}
	}
	for version, entry := range preparedMigrationManifest {
		migration, ok := byVersion[version]
		if !ok {
			t.Errorf("manifest version %d missing from catalog", version)
			continue
		}
		if migration.name != entry.Name || migration.digest != entry.SHA256 {
			t.Errorf("manifest version %d does not match catalog bytes", version)
		}
	}
}

func TestHistoricalOfflineOwners(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version  int64
		law      ConservationLawID
		verifier SemanticVerifierID
		owner    string
	}{
		{35, ConservationLawIndexPresentBasePreserved, "", "2328"},
		{45, ConservationLawIndexPresentBasePreserved, "", "2328"},
		{76, ConservationLawRewriteCollapse, SemanticVerifierCollapseSessionWorkspaceObservations, "2328"},
		{78, ConservationLawBaseConserving, SemanticVerifierRepairEpochZeroHookUsage, "2316"},
		{79, ConservationLawBaseConserving, SemanticVerifierDropRetiredTable, "2317"},
		{80, ConservationLawBaseConserving, SemanticVerifierDropSearchProjectionFamily, "2319"},
	}
	for _, tc := range cases {
		if conservationLawFor(tc.version) != tc.law {
			t.Errorf("version %d law = %q, want %q", tc.version, conservationLawFor(tc.version), tc.law)
		}
		if semanticVerifierFor(tc.version) != tc.verifier {
			t.Errorf("version %d verifier = %q, want %q", tc.version, semanticVerifierFor(tc.version), tc.verifier)
		}
		if ownerIssueFor(tc.version) != tc.owner {
			t.Errorf("version %d owner = %q, want %q", tc.version, ownerIssueFor(tc.version), tc.owner)
		}
	}
	if semanticVerifierFor(35) != "" || semanticVerifierFor(45) != "" {
		t.Fatal("35/45 must not require a custom Layer-2 verifier (CREATE INDEX only)")
	}
}

func TestUpgradeRecipeFileHasNoMigrationSQL(t *testing.T) {
	t.Parallel()
	path := filepath.Join("prepared_upgrade_migration_recipe.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, needle := range []string{"create table", "alter table", "drop table", "insert into", "create index"} {
		if strings.Contains(text, needle) {
			t.Fatalf("upgrade recipe must not contain migration SQL strings, found %q", needle)
		}
	}
}
