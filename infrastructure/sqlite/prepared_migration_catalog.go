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
	// 41/42 create projection bookkeeping tables and copy bounded projection
	// state, not events. Reclassified constant_in_place in #1852.
	41: {41, "000041_add_search_projection_generation_lifecycle.sql", "f5fefcb1389cff1889fdd05cf1654838ac2e57556565dd96df32695e48d4c964", MigrationConstantInPlace},
	42: {42, "000042_add_bounded_search_projection_inventory.sql", "8d272ed8157458c864ac3ed84e6cfb9969b91b2c3ac31464b084ff1b5f4f8a53", MigrationConstantInPlace},
	43: {43, "000043_add_payload_codec_compatibility_mode.sql", "020c6dbb5dbea86c3c9989ad29babe333dc7c423ded6152aeddb37a67aa907e0", MigrationConstantInPlace},
	44: {44, "000044_add_archive_segment_catalog.sql", "f272f78c9bed784b8ed487a71f801e172e994109092de19a6f0582a15318f38f", MigrationConstantInPlace},
	45: {45, "000045_index_retention_ledger_by_event.sql", "5d276bdeddc90b8db688460f4b18a87e5267ec568e0216a1cd701eba8c8a8a20", MigrationDataDependentOffline},
	46: {46, "000046_create_session_refinements.sql", "cc648f2f5da92fa5c742fe644db87325ecb8cc3eae4c6d0773aaf523ddf12181", MigrationConstantInPlace},
	// 47 is CREATE TABLE only (PRIMARY KEY already indexes session_id) — no
	// backfill, no rewrite of existing rows. Same class as 46
	// (session_refinements): constant_in_place.
	47: {47, "000047_create_session_orphan_ranges.sql", "8ff51b5100c663cc7cdcd7a2dc3bd66066635bccedeecadb86cccdb3eac00978", MigrationConstantInPlace},
	// 49 only adds three cutover evidence columns to search_projection_state.
	49: {49, "000049_add_search_projection_cutover_evidence.sql", "5ca7ad45bed4a49d0262fc66ae36ffbe434ade920787a1c1551e7eb62e750648", MigrationConstantInPlace},
	// 50 only drops and recreates two insert triggers (no data scan/rewrite).
	50: {50, "000050_fix_search_projection_insert_inventory_drift.sql", "6c80fdcceb682093a466fc6207e084644da4923c6762a4f4f927878aa2418b5b", MigrationConstantInPlace},
	// 51 only adds four cutover evidence columns to search_projection_state.
	51: {51, "000051_add_search_projection_cutover_evidence_status.sql", "0d5b692c1039301b2216ef8047e5980e74bfecb3865b40ccf1045a8c339121bc", MigrationConstantInPlace},
	// 52 only drops eight writer triggers, one view, and the control table.
	// The multi-GiB event_search_* tables stay until search-retire.
	52: {52, "000052_retire_legacy_search_writers.sql", "6ef7a1ff65c1859021a9ffba375d642d9b7c622d1595fe0e58d99c43fcdc3aeb", MigrationConstantInPlace},
	// 53 only drops and recreates two body-derivation triggers (no data scan).
	53: {53, "000053_codec_aware_body_derivations.sql", "dc8c32cc5397f5a6afe056e29c992f2e868c9e6ab22a3c4127afa31f3928d12b", MigrationConstantInPlace},
	// 54 only creates the payload_backfill_runs bookkeeping table (no data scan).
	54: {54, "000054_add_payload_backfill.sql", "8c59b480a1250eecf424bb014f4e2fc3c4c52079fcbf004db47903fd6fae3622", MigrationConstantInPlace},
	// 55 adds index_family_byte_limit and capacity columns; keeps recent_byte_limit
	// so a rolled-back binary still functions (additive only; no data scan).
	55: {55, "000055_add_search_projection_index_family_budget.sql", "501846cc12b93c78a90791b5460adccbbcad48fa1ac18e138077dc64d27eb1b5", MigrationConstantInPlace},
	// 56 only drops and recreates two body-derivation insert triggers (no data scan).
	56: {56, "000056_codec_aware_body_derivations_on_insert.sql", "266a02e9be0509b3067050319f4d5654c0623034f0ed4c18920e6b0ff8ae7f32", MigrationConstantInPlace},
	57: {57, "000057_add_search_projection_exclusions.sql", "8d7c63f873ef1d0358078a03a3704e8ae95615035d6521f466f23aba94283435", MigrationConstantInPlace},
	// 58 adds a constant-default column and updates only singleton/lifecycle rows; it does not scan store-sized data.
	58: {58, "000058_bound_recent_projection_source_bytes.sql", "9372a60c1c89910bae7d9ebd57c80e92837cb1ea55699a02122a987e53f5d155", MigrationConstantInPlace},
	// 59 drops an unused index; no row data or query semantics change. In place
	// despite the object's size: dropping an index moves its pages to the
	// freelist rather than rewriting them, measured at 0.096s for 118,489,088
	// bytes on a 7.1 GB store. Migration 52 forbids DROPs that would block
	// store open; this one is two orders of magnitude away from that.
	59: {59, "000059_drop_literal_search_fingerprint_candidate_index.sql", "70874ea5b244fc361b82f1e3fa9f1fbff30437dc77ebd7f9fedf7ab53ef6e62b", MigrationConstantInPlace},
	// 60 adds has_agent_reasoning and updates only session_refinements
	// (one row per session), not store-sized event tables.
	60: {60, "000060_add_session_refinement_has_agent_reasoning.sql", "213d2338ac324488a00a64ecf60592f1289b0c71a75884ba4672f5fd1403abcd", MigrationConstantInPlace},
	// 61 creates empty attestation tables and inserts the genesis head.
	// It does not scan events or command_audits.
	61: {61, "000061_create_attestation_chain.sql", "a7c7823b40615ff12d46e32b888d46f2fe2fa55a1b7e9deae899fd8404dad5e8", MigrationConstantInPlace},
	// 62 rebuilds search_projection_exclusions to add class=row_work.
	// Only existing exclusion rows are copied. That table is not store-sized
	// (one row per budget-rejected source event, not events), so this stays
	// constant_in_place rather than a prepared offline rewrite.
	62: {62, "000062_search_projection_exclusion_row_work.sql", "0c5999e55f7019cbe30235e30696a104b20e2261d8c7a0289fed11dfbc5e1d47", MigrationConstantInPlace},
	// 63 only drops and recreates three invalidator triggers plus one
	// events.id immutability guard. No data scan or rewrite.
	63: {63, "000063_watch_decoder_columns_on_search_invalidators.sql", "3cd33611da576a9b544c214f0e9b49ae6067f422f4c21ffd28f546f3bad25ef2", MigrationConstantInPlace},
	// 64 adds origin to the singleton search_projection_state row and updates
	// that row only. No store-sized scan.
	64: {64, "000064_search_projection_generation_origin.sql", "e1180e75dd2b4896a03123c1a914e162cacace7e76277e908885859e43608bf8", MigrationConstantInPlace},
	// 65 adds capacity_rederived to the singleton search_projection_state
	// row. No store-sized scan.
	65: {65, "000065_search_projection_capacity_rederived.sql", "0b02dc61ddfa243304a6d20a29f76ab3ccf780e37a5d5ae59c7ce7de152bb9b1", MigrationConstantInPlace},
	// 66 only drops two writer triggers. The unread recent FTS virtual
	// table stays until store compact (052: no multi-GiB DROP on open).
	66: {66, "000066_drop_recent_fts_writers.sql", "3de6d4706f043d23d6212def8e23344684e5a95444587c361ad7f970062bb892", MigrationConstantInPlace},
	// 67 only adds five nullable codec columns to event_content_dedupe_archive
	// (#1744). Same shape as 36 on events: no backfill, no row rewrite; SQLite
	// ADD COLUMN is O(1) regardless of table size.
	67: {67, "000067_add_payload_codec_to_event_content_dedupe_archive.sql", "f9f912f21290e64af38dffc86398f86d08503f8345926a99cf459901fff743c2", MigrationConstantInPlace},
	// 68 adds cleanup_no_progress_attempts to the singleton
	// search_projection_state row. No store-sized scan.
	68: {68, "000068_add_search_projection_cleanup_no_progress_attempts.sql", "21d36ae2a63af21c8bfd4688c362aeb555ddcbdb0555e5703ccdf01aa877a526", MigrationConstantInPlace},
	// 69 adds two empty TEXT columns, two indexes, and four stamp triggers.
	// Row backfill of started_at_norm / observed_at_norm is a bounded open
	// catch-up, not this migration.
	69: {69, "000069_add_report_window_norm_columns.sql", "85ab3ae7f4efc6a777eef43406e7882cc523c9a53bd8d758fe3eb8daa01a8455", MigrationConstantInPlace},
	// 70 recreates a fingerprint-first secondary index. CREATE INDEX does not
	// rewrite the fingerprint rows; previous binaries ignore the extra index.
	70: {70, "000070_restore_literal_search_fingerprint_by_fp.sql", "4ab560ca7ba1986758c6a40f6c0d0d58c9ca393090b550ae6995a8f6a9a93b3c", MigrationConstantInPlace},
	// 71 drops five unused raw-created_at events indexes. DROP INDEX moves
	// pages to the freelist (000059); no row rewrite.
	71: {71, "000071_drop_superseded_events_created_at_indexes.sql", "518555e257aa6f100713c370fafc45765449a8f875f1708438b139e0e7edb3f5", MigrationConstantInPlace},
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
// and validates the catalog using the strongest invariant available for each
// range. Below the first manifest version, the manifest declares nothing, so
// catalog versions must be contiguous from 1. At and above that boundary, the
// catalog must instead equal the manifest exactly; this permits deliberate
// omissions such as version 48 while still checking every declared migration's
// name and digest.
func BuildPreparedMigrationPlan(ctx context.Context, db *sql.DB, migrations fs.FS) (PreparedMigrationPlan, error) {
	catalog, err := inventoryEmbeddedMigrations(migrations)
	if err != nil {
		return PreparedMigrationPlan{}, err
	}
	manifestVersions := make([]int64, 0, len(preparedMigrationManifest))
	for version := range preparedMigrationManifest {
		manifestVersions = append(manifestVersions, version)
	}
	sort.Slice(manifestVersions, func(i, j int) bool { return manifestVersions[i] < manifestVersions[j] })
	manifestMinimum := manifestVersions[0]
	byVersion := make(map[int64]embeddedMigration, len(catalog))
	for _, migration := range catalog {
		byVersion[migration.version] = migration
	}
	for _, version := range manifestVersions {
		manifest := preparedMigrationManifest[version]
		migration, exists := byVersion[version]
		if !exists || migration.name != manifest.Name || migration.digest != manifest.SHA256 {
			return PreparedMigrationPlan{}, fmt.Errorf("migration %d does not match the prepared migration manifest", version)
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
	for index, expected := 0, int64(1); expected < manifestMinimum; index, expected = index+1, expected+1 {
		if index >= len(catalog) || catalog[index].version != expected {
			return PreparedMigrationPlan{}, fmt.Errorf("migration catalog has discontinuity at version %d", expected)
		}
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
