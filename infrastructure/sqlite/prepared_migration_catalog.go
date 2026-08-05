package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
)

// MigrationExecutionClass is an explicit reviewed property of a migration.
type MigrationExecutionClass string

const (
	// MigrationConstantInPlace identifies bounded data-independent installation.
	MigrationConstantInPlace MigrationExecutionClass = "constant_in_place"
	// MigrationDataDependentOffline requires preparation outside publication.
	MigrationDataDependentOffline MigrationExecutionClass = "data_dependent_offline"
)

type migrationManifestEntry struct {
	Version int64
	Name    string
	SHA256  string
	Class   MigrationExecutionClass
}

// preparedMigrationManifest is source-owned and must change whenever one of
// the classified SQL bodies changes. No SQL-text heuristic is used.
var preparedMigrationManifest = map[int64]migrationManifestEntry{
	35: {35, "000035_add_session_lookup_projection_index.sql", "38a6cb3be6021bf7ae0fc7133ebc4d90fdf8f502a5082d551031a891f59e9e5c", MigrationDataDependentOffline},
	36: {36, "000036_add_payload_codec_foundation.sql", "cfdf4268291cb6dff331dd7f0e26f4a46d0625059447570f0e03c7466687b9a9", MigrationConstantInPlace},
	37: {37, "000037_add_payload_rehearsal.sql", "578e98291a1264af5e507e81688a3630de644bd66c7bb017b9c56fd49b631719", MigrationConstantInPlace},
	38: {38, "000038_add_bounded_search_projections.sql", "8f08275464c6b1d4e1e523664dddf28c8171b9984688bad34c35c2168cf85b58", MigrationConstantInPlace},
	39: {39, "000039_add_literal_search_fingerprints.sql", "991cdb789ffd5d2f59eb47013c5ec8d87c2f3f41ca7d479c8c2f927fc3040993", MigrationConstantInPlace},
	40: {40, "000040_add_search_maintenance_control.sql", "1a844f96ee3919203f42049d09162fa08301658a37c3b06b1d0cf916e895c25a", MigrationConstantInPlace},
	41: {41, "000041_add_search_projection_generation_lifecycle.sql", "f5fefcb1389cff1889fdd05cf1654838ac2e57556565dd96df32695e48d4c964", MigrationDataDependentOffline},
	42: {42, "000042_add_bounded_search_projection_inventory.sql", "8d272ed8157458c864ac3ed84e6cfb9969b91b2c3ac31464b084ff1b5f4f8a53", MigrationDataDependentOffline},
	43: {43, "000043_add_payload_codec_compatibility_mode.sql", "020c6dbb5dbea86c3c9989ad29babe333dc7c423ded6152aeddb37a67aa907e0", MigrationConstantInPlace},
	44: {44, "000044_add_archive_sequence_inventory.sql", "18e270f1b4b974f54529fac3aa2ab1daf273797a51217c3bf3601bdbcbcc79f6", MigrationConstantInPlace},
	45: {45, "000045_add_segment_catalog_ledger.sql", "00802c1fe48e30ef1fd5ffc2e1c22e33bc8f32c6ea66d52e869d0362172d0982", MigrationConstantInPlace},
	46: {46, "000046_add_segment_target_plans.sql", "f6759e9883580436e6717d9009e91108212bff9d5376ede8f82c9700231ccc65", MigrationConstantInPlace},
	47: {47, "000047_add_catalog_summary_reconciliation.sql", "885c32336046359393f7ef35a96b81e418058d706233edab4ccf1fca60c1fb47", MigrationConstantInPlace},
}

// PreparedMigration identifies one exact pending embedded migration.
type PreparedMigration struct {
	Version int64
	Name    string
	SHA256  string
	Class   MigrationExecutionClass
	body    []byte
}

// PreparedMigrationPlan is the exact ordered suffix authorized for a target.
type PreparedMigrationPlan struct {
	Pending []PreparedMigration
	Digest  string
	Offline bool
	Current int64
	Latest  int64
}

type embeddedMigration struct {
	version int64
	name    string
	body    []byte
	digest  string
}

// executeExactMigration is the sole SQL migration executor. Both normal store
// initialization and owned offline candidate construction pass catalog entries
// through this boundary so version, basename and SQL bytes cannot drift.
func executeExactMigration(ctx context.Context, db *sql.DB, migration embeddedMigration) error {
	version, err := parseMigrationVersion(migration.name)
	if err != nil {
		return fmt.Errorf("validate migration filename: %w", err)
	}
	if version != migration.version || filepath.Base(migration.name) != migration.name {
		return errors.New("migration catalog version/name mismatch")
	}
	sum := sha256.Sum256(migration.body)
	if hex.EncodeToString(sum[:]) != migration.digest {
		return fmt.Errorf("migration %d SQL digest mismatch", migration.version)
	}
	return applyMigration(ctx, db, migration.version, migration.name, string(migration.body))
}

func exactMigrationFromPrepared(migration PreparedMigration) embeddedMigration {
	return embeddedMigration{
		version: migration.Version,
		name:    migration.Name,
		body:    migration.body,
		digest:  migration.SHA256,
	}
}

func inventoryEmbeddedMigrations(migrations fs.FS) ([]embeddedMigration, error) {
	paths, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("migration catalog is empty")
	}
	result := make([]embeddedMigration, 0, len(paths))
	seen := make(map[int64]struct{}, len(paths))
	for _, path := range paths {
		version, parseErr := parseMigrationVersion(path)
		if parseErr != nil {
			return nil, parseErr
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
		seen[version] = struct{}{}
		body, readErr := fs.ReadFile(migrations, path)
		if readErr != nil {
			return nil, fmt.Errorf("read embedded migration: %w", readErr)
		}
		sum := sha256.Sum256(body)
		result = append(result, embeddedMigration{version: version, name: filepath.Base(path), body: body, digest: hex.EncodeToString(sum[:])})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].version < result[j].version })
	return result, nil
}

// BuildPreparedMigrationPlan verifies the ledger is an exact catalog prefix
// and every pending migration has an exact reviewed manifest entry.
func BuildPreparedMigrationPlan(ctx context.Context, db *sql.DB, migrations fs.FS) (PreparedMigrationPlan, error) {
	catalog, err := inventoryEmbeddedMigrations(migrations)
	if err != nil {
		return PreparedMigrationPlan{}, err
	}
	for index, migration := range catalog {
		if migration.version != int64(index+1) {
			return PreparedMigrationPlan{}, fmt.Errorf("prepared migration catalog gap before version %d", migration.version)
		}
	}
	var tableExists int
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='schema_migrations')`).Scan(&tableExists); err != nil {
		return PreparedMigrationPlan{}, fmt.Errorf("inspect schema migration ledger: %w", err)
	}
	if tableExists == 0 {
		return PreparedMigrationPlan{}, errors.New("prepared migration requires an applied schema prefix")
	}
	rows, err := db.QueryContext(ctx, `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return PreparedMigrationPlan{}, fmt.Errorf("query schema migration ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := 0
	for rows.Next() {
		var version int64
		var name string
		if err = rows.Scan(&version, &name); err != nil {
			return PreparedMigrationPlan{}, fmt.Errorf("scan schema migration ledger: %w", err)
		}
		if applied >= len(catalog) || version != catalog[applied].version || name != catalog[applied].name {
			return PreparedMigrationPlan{}, errors.New("schema migration ledger is not an exact catalog prefix")
		}
		applied++
	}
	if err = rows.Err(); err != nil {
		return PreparedMigrationPlan{}, fmt.Errorf("iterate schema migration ledger: %w", err)
	}
	current := int64(0)
	if applied > 0 {
		current = catalog[applied-1].version
	}
	if current < 34 {
		return PreparedMigrationPlan{}, errors.New("prepared migration requires schema version 34 or newer")
	}
	plan := PreparedMigrationPlan{Current: current, Latest: catalog[len(catalog)-1].version}
	h := sha256.New()
	writeMigrationPlanFrame := func(value []byte) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(value)
	}
	writeMigrationPlanFrame([]byte("traceary/prepared-migration-plan/v1"))
	for _, migration := range catalog[applied:] {
		manifest, exists := preparedMigrationManifest[migration.version]
		if !exists || manifest.Version != migration.version || manifest.Name != migration.name || manifest.SHA256 != migration.digest || (manifest.Class != MigrationConstantInPlace && manifest.Class != MigrationDataDependentOffline) {
			return PreparedMigrationPlan{}, fmt.Errorf("migration %d has no matching reviewed classification", migration.version)
		}
		entry := PreparedMigration{Version: migration.version, Name: migration.name, SHA256: migration.digest, Class: manifest.Class, body: migration.body}
		plan.Pending = append(plan.Pending, entry)
		plan.Offline = plan.Offline || entry.Class == MigrationDataDependentOffline
		var version [8]byte
		binary.BigEndian.PutUint64(version[:], uint64(entry.Version))
		writeMigrationPlanFrame(version[:])
		writeMigrationPlanFrame([]byte(entry.Name))
		writeMigrationPlanFrame([]byte(entry.SHA256))
		writeMigrationPlanFrame([]byte(entry.Class))
	}
	plan.Digest = hex.EncodeToString(h.Sum(nil))
	return plan, nil
}
