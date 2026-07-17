// Package usecase — bundle usecase implements the v0.9 portability
// primitive introduced for #572: a local-first, encrypted,
// content-verifiable archive that operators can move between their
// machines through any file-transport they already have (AirDrop,
// scp, Syncthing, etc.). Traceary never ships its own transport.
//
// Portability covers all five tables — events, sessions, command_audits,
// memories, and memory_edges — see docs/operations/cross-machine-handoff
// for the operator guide.
package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// BundleUsecase exports / imports a local-first portability bundle.
type BundleUsecase interface {
	Export(ctx context.Context, opts BundleExportOptions) error
	Import(ctx context.Context, opts BundleImportOptions) (BundleImportResult, error)
}

// BundleExportOptions controls what a single Export call writes.
type BundleExportOptions struct {
	// OutPath is the filesystem path where the encrypted archive
	// lands.
	OutPath string
	// Passphrase derives the archive's symmetric key via
	// Argon2id. Must be non-empty — Traceary does not produce
	// unencrypted bundles because the same artifact may later be
	// carried over an untrusted channel (email, Dropbox, etc.).
	Passphrase []byte
	// Since / Until narrow the event range. Zero value disables
	// the side of the range.
	Since time.Time
	Until time.Time
	// Workspace, when non-empty, restricts exported events to a
	// single workspace.
	Workspace types.Workspace
}

// BundleConflictPolicy controls how bundle import handles a row whose
// durable identity already exists in the destination store.
type BundleConflictPolicy string

const (
	// BundleConflictSkip keeps the destination row and counts a skip.
	BundleConflictSkip BundleConflictPolicy = "skip"
	// BundleConflictReplace overwrites the destination row with the bundle row.
	BundleConflictReplace BundleConflictPolicy = "replace"
	// BundleConflictError fails the import on the first conflict.
	BundleConflictError BundleConflictPolicy = "error"
)

func (p BundleConflictPolicy) normalized() (BundleConflictPolicy, error) {
	switch p {
	case "", BundleConflictSkip:
		return BundleConflictSkip, nil
	case BundleConflictReplace, "overwrite":
		return BundleConflictReplace, nil
	case BundleConflictError, "reject":
		return BundleConflictError, nil
	default:
		return "", xerrors.Errorf("unsupported bundle conflict policy %q (want skip, replace, or error)", p)
	}
}

// BundleMissingParentPolicy controls multi-table bundle imports where a
// child row (e.g. an imported session) can reference a parent that is
// absent from both the bundle and the destination store.
type BundleMissingParentPolicy string

const (
	// BundleMissingParentReject fails imports that reference missing parents.
	BundleMissingParentReject BundleMissingParentPolicy = "reject"
	// BundleMissingParentSkip skips rows that reference missing parents.
	BundleMissingParentSkip BundleMissingParentPolicy = "skip"
	// BundleMissingParentBackfill creates placeholder parents when supported.
	BundleMissingParentBackfill BundleMissingParentPolicy = "backfill"
)

func (p BundleMissingParentPolicy) normalized() (BundleMissingParentPolicy, error) {
	switch p {
	case "", BundleMissingParentReject:
		return BundleMissingParentReject, nil
	case BundleMissingParentSkip:
		return BundleMissingParentSkip, nil
	case BundleMissingParentBackfill:
		return BundleMissingParentBackfill, nil
	default:
		return "", xerrors.Errorf("unsupported bundle missing-parent policy %q (want reject, skip, or backfill)", p)
	}
}

// BundleOrphanEdgesPolicy controls how bundle import handles memory edges
// whose endpoints are absent from the destination store after memories import.
type BundleOrphanEdgesPolicy string

const (
	// BundleOrphanEdgesSkip skips orphan edges and emits a structured warning.
	BundleOrphanEdgesSkip BundleOrphanEdgesPolicy = "skip"
	// BundleOrphanEdgesReject fails the import on the first orphan edge.
	BundleOrphanEdgesReject BundleOrphanEdgesPolicy = "reject"
)

func (p BundleOrphanEdgesPolicy) normalized() (BundleOrphanEdgesPolicy, error) {
	switch p {
	case "", BundleOrphanEdgesSkip:
		return BundleOrphanEdgesSkip, nil
	case BundleOrphanEdgesReject, "error":
		return BundleOrphanEdgesReject, nil
	default:
		return "", xerrors.Errorf("unsupported bundle orphan-edges policy %q (want skip or reject)", p)
	}
}

// BundleImportOptions controls a single Import call.
type BundleImportOptions struct {
	// InPath is the filesystem path of the archive to read.
	InPath string
	// Passphrase decrypts the archive. Must match what Export used.
	Passphrase []byte
	// OnConflict controls UNIQUE collisions. Empty defaults to skip for
	// v0.9-compatible idempotent re-imports.
	OnConflict BundleConflictPolicy
	// MissingParent controls how the sessions importer handles an imported session whose parent session is absent. Empty defaults to reject.
	MissingParent BundleMissingParentPolicy
	// OrphanEdges controls memory_edges rows whose endpoints are missing after
	// memories import. Empty defaults to skip-with-warning.
	OrphanEdges BundleOrphanEdgesPolicy
}

// BundleImportResult summarises what changed during Import.
type BundleImportResult struct {
	// EventsImported / EventsSkipped count events that were newly
	// written vs dropped because of a pre-existing (event_id)
	// collision.
	EventsImported        int
	EventsSkipped         int
	SessionsImported      int
	SessionsSkipped       int
	CommandAuditsImported int
	CommandAuditsSkipped  int
	// MemoriesImported / MemoriesSkipped count durable memories that were newly
	// written vs dropped because of a pre-existing memory id collision.
	MemoriesImported int
	MemoriesSkipped  int
	// MemoryEdgesImported / MemoryEdgesSkipped count memory graph edges that were
	// newly written vs skipped due to idempotency or orphan-edge tolerance.
	MemoryEdgesImported int
	MemoryEdgesSkipped  int
	// BundleSchemaVersion is the schema_migrations version the
	// archive carried at Export time.
	BundleSchemaVersion int
}

// bundleManifestVersion is the only on-disk manifest version
// Traceary knows how to write and read. Bumping it is a
// migration-level change; readers that see a higher manifest version
// refuse to import rather than silently skipping unknown fields.
const bundleManifestVersion = 2
const minBundleReaderManifestVersion = 1

type bundleManifest struct {
	ManifestVersion        int                          `json:"manifest_version"`
	MinReaderSchemaVersion int                          `json:"min_reader_schema_version,omitempty"`
	CreatedAt              time.Time                    `json:"created_at"`
	BundleSchemaVersion    int                          `json:"schema_version"`
	Writer                 bundleManifestWriter         `json:"writer,omitempty"`
	ImportDefaults         bundleManifestImportDefaults `json:"import_defaults,omitempty"`
	Filters                bundleFilters                `json:"filters"`
	Tables                 map[string]bundleTableEntry  `json:"tables,omitempty"`
	FileChecksums          map[string]string            `json:"file_checksums,omitempty"`
}

type bundleManifestWriter struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type bundleManifestImportDefaults struct {
	OnConflict    string `json:"on_conflict"`
	MissingParent string `json:"missing_parent"`
	OrphanEdges   string `json:"orphan_edges"`
}

type bundleTableEntry struct {
	TableName string `json:"table_name"`
	File      string `json:"file"`
	RowCount  int    `json:"row_count"`
	Checksum  string `json:"checksum"`
}

type bundleFilters struct {
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// Envelope layout: magic (8) | version (1) | salt (16) | nonce (24)
// | ciphertext. `magic` identifies a Traceary bundle; `version`
// gates the envelope layout itself.
var (
	bundleMagic    = []byte{'T', 'R', 'B', 'U', 'N', 'D', 'L', 'E'}
	bundleEnvelope = byte(1)
)

// BundleEventRepository is the set of write operations the usecase
// needs to run Import. Export only reads through
// queryservice.EventQueryService, which already exists.
type BundleEventRepository interface {
	// SchemaVersion is the current schema_migrations max version.
	SchemaVersion(ctx context.Context) (int, error)
	ListBundleSessions(ctx context.Context) ([]*model.Session, error)
	ListBundleCommandAudits(ctx context.Context) ([]*model.CommandAudit, error)
	// ListBundleMemories returns all durable memories and their refs for bundle export.
	ListBundleMemories(ctx context.Context) ([]apptypes.MemoryDetails, error)
	// ListBundleMemoryEdges returns all memory graph edges for bundle export.
	ListBundleMemoryEdges(ctx context.Context) ([]*model.MemoryEdge, error)
	// BeginBundleImport starts the single transaction used by all table
	// importers in registry order.
	BeginBundleImport(ctx context.Context) (BundleImportTransaction, error)
}

// BundleImportTransaction is the write-side transaction shared by all
// bundle table importers.
type BundleImportTransaction interface {
	ImportSession(ctx context.Context, session *model.Session, policy BundleConflictPolicy, missingParent BundleMissingParentPolicy) (bool, error)
	ImportEvent(ctx context.Context, event *model.Event, policy BundleConflictPolicy) (bool, error)
	ImportCommandAudit(ctx context.Context, audit *model.CommandAudit, policy BundleConflictPolicy) (bool, error)
	ImportMemory(ctx context.Context, memory *model.Memory, policy BundleConflictPolicy) (bool, error)
	MemoryExists(ctx context.Context, memoryID types.MemoryID) (bool, error)
	MemoryEdgeExists(ctx context.Context, edgeID types.MemoryEdgeID) (bool, error)
	ImportMemoryEdge(ctx context.Context, edge *model.MemoryEdge, policy BundleConflictPolicy) (bool, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type bundleUsecase struct {
	events     queryservice.EventQueryService
	repository BundleEventRepository
	nowFunc    func() time.Time
}

// NewBundleUsecase constructs a BundleUsecase.
func NewBundleUsecase(
	events queryservice.EventQueryService,
	repository BundleEventRepository,
	nowFunc func() time.Time,
) BundleUsecase {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	return &bundleUsecase{events: events, repository: repository, nowFunc: nowFunc}
}

func (u *bundleUsecase) Export(ctx context.Context, opts BundleExportOptions) error {
	if len(opts.Passphrase) == 0 {
		return xerrors.Errorf("passphrase must not be empty")
	}
	if opts.OutPath == "" {
		return xerrors.Errorf("out path must not be empty")
	}

	schemaVersion, err := u.repository.SchemaVersion(ctx)
	if err != nil {
		return xerrors.Errorf("failed to resolve schema version: %w", err)
	}

	criteria := apptypes.NewEventListCriteriaBuilder(1000).
		From(opts.Since).
		To(opts.Until).
		Workspace(opts.Workspace).
		Build()
	events, err := u.events.ListWindow(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("failed to list events for bundle: %w", err)
	}

	sessions, err := u.repository.ListBundleSessions(ctx)
	if err != nil {
		return xerrors.Errorf("failed to list sessions for bundle: %w", err)
	}
	sessions = filterSessionsForBundleExport(sessions, events, opts)
	commandAudits, err := u.repository.ListBundleCommandAudits(ctx)
	if err != nil {
		return xerrors.Errorf("failed to list command audits for bundle: %w", err)
	}
	commandAudits = filterCommandAuditsForEvents(commandAudits, events)
	memories, err := u.repository.ListBundleMemories(ctx)
	if err != nil {
		return xerrors.Errorf("failed to list memories for bundle: %w", err)
	}
	memoryEdges, err := u.repository.ListBundleMemoryEdges(ctx)
	if err != nil {
		return xerrors.Errorf("failed to list memory edges for bundle: %w", err)
	}

	registry := u.bundleTableRegistry()
	sessionsImporter := registry["sessions"]
	sessionsBuf, err := sessionsImporter.Export(ctx, bundleExportInputRows{Sessions: sessions})
	if err != nil {
		return xerrors.Errorf("failed to encode sessions: %w", err)
	}
	eventsImporter := registry["events"]
	eventsBuf, err := eventsImporter.Export(ctx, bundleExportInputRows{Events: events})

	if err != nil {
		return xerrors.Errorf("failed to encode events: %w", err)
	}
	commandAuditsImporter := registry["command_audits"]
	commandAuditsBuf, err := commandAuditsImporter.Export(ctx, bundleExportInputRows{CommandAudits: commandAudits})
	if err != nil {
		return xerrors.Errorf("failed to encode command audits: %w", err)
	}
	memoriesImporter := registry["memories"]
	memoriesBuf, err := memoriesImporter.Export(ctx, bundleExportInputRows{Memories: memories})

	if err != nil {
		return xerrors.Errorf("failed to encode memories: %w", err)
	}
	memoryEdgesImporter := registry["memory_edges"]
	memoryEdgesBuf, err := memoryEdgesImporter.Export(ctx, bundleExportInputRows{MemoryEdges: memoryEdges})
	if err != nil {
		return xerrors.Errorf("failed to encode memory edges: %w", err)
	}

	manifest := bundleManifest{
		ManifestVersion:        bundleManifestVersion,
		MinReaderSchemaVersion: minBundleReaderManifestVersion,
		CreatedAt:              u.nowFunc().UTC(),
		BundleSchemaVersion:    schemaVersion,
		Writer:                 bundleManifestWriter{Name: "traceary", Version: "dev"},
		ImportDefaults: bundleManifestImportDefaults{
			OnConflict:    string(BundleConflictSkip),
			MissingParent: string(BundleMissingParentReject),
			OrphanEdges:   string(BundleOrphanEdgesSkip),
		},
		Filters: bundleFilters{
			Since:     formatOptionalTime(opts.Since),
			Until:     formatOptionalTime(opts.Until),
			Workspace: opts.Workspace.String(),
		},
		Tables: map[string]bundleTableEntry{
			"sessions": {
				TableName: "sessions",
				File:      sessionsImporter.FileName(),
				RowCount:  len(sessions),
				Checksum:  hashSHA256(sessionsBuf.Bytes()),
			},
			"events": {
				TableName: "events",
				File:      eventsImporter.FileName(),
				RowCount:  len(events),
				Checksum:  hashSHA256(eventsBuf.Bytes()),
			},
			"command_audits": {
				TableName: "command_audits",
				File:      commandAuditsImporter.FileName(),
				RowCount:  len(commandAudits),
				Checksum:  hashSHA256(commandAuditsBuf.Bytes()),
			},
			"memories": {
				TableName: "memories",
				File:      memoriesImporter.FileName(),
				RowCount:  len(memories),
				Checksum:  hashSHA256(memoriesBuf.Bytes()),
			},
			"memory_edges": {
				TableName: "memory_edges",
				File:      memoryEdgesImporter.FileName(),
				RowCount:  len(memoryEdges),
				Checksum:  hashSHA256(memoryEdgesBuf.Bytes()),
			},
		},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode manifest: %w", err)
	}

	plaintext, err := encodeTarGz(map[string][]byte{
		"manifest.json":         manifestBytes,
		"sessions.ndjson":       sessionsBuf.Bytes(),
		"events.ndjson":         eventsBuf.Bytes(),
		"command_audits.ndjson": commandAuditsBuf.Bytes(),
		"memories.ndjson":       memoriesBuf.Bytes(),
		"memory_edges.ndjson":   memoryEdgesBuf.Bytes(),
	})
	if err != nil {
		return xerrors.Errorf("failed to build tar.gz: %w", err)
	}
	sealed, err := sealBundle(plaintext, opts.Passphrase)
	if err != nil {
		return xerrors.Errorf("failed to encrypt bundle: %w", err)
	}
	if err := os.WriteFile(opts.OutPath, sealed, 0o600); err != nil {
		return xerrors.Errorf("failed to write bundle to %s: %w", opts.OutPath, err)
	}
	return nil
}

func (u *bundleUsecase) Import(ctx context.Context, opts BundleImportOptions) (BundleImportResult, error) {
	if len(opts.Passphrase) == 0 {
		return BundleImportResult{}, xerrors.Errorf("passphrase must not be empty")
	}
	if opts.InPath == "" {
		return BundleImportResult{}, xerrors.Errorf("in path must not be empty")
	}
	onConflict, err := opts.OnConflict.normalized()
	if err != nil {
		return BundleImportResult{}, err
	}
	missingParent, err := opts.MissingParent.normalized()
	if err != nil {
		return BundleImportResult{}, err
	}
	orphanEdges, err := opts.OrphanEdges.normalized()
	if err != nil {
		return BundleImportResult{}, err
	}
	encrypted, err := os.ReadFile(opts.InPath)
	if err != nil {
		return BundleImportResult{}, xerrors.Errorf("failed to read bundle: %w", err)
	}
	plaintext, err := openBundle(encrypted, opts.Passphrase)
	if err != nil {
		return BundleImportResult{}, xerrors.Errorf("failed to decrypt bundle: %w", err)
	}
	files, err := decodeTarGz(plaintext)
	if err != nil {
		return BundleImportResult{}, xerrors.Errorf("failed to extract bundle: %w", err)
	}

	manifestBytes, ok := files["manifest.json"]
	if !ok {
		return BundleImportResult{}, xerrors.Errorf("bundle is missing manifest.json")
	}
	var manifest bundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return BundleImportResult{}, xerrors.Errorf("failed to parse manifest: %w", err)
	}
	if manifest.ManifestVersion < minBundleReaderManifestVersion || manifest.ManifestVersion > bundleManifestVersion {
		return BundleImportResult{}, xerrors.Errorf(
			"unsupported bundle manifest version %d (this build understands %d)",
			manifest.ManifestVersion, bundleManifestVersion,
		)
	}
	registry := u.bundleTableRegistry()
	tableEntries, err := manifestTableEntries(manifest, registry)
	if err != nil {
		return BundleImportResult{}, err
	}
	if err := verifyBundleFiles(files, tableEntries); err != nil {
		return BundleImportResult{}, err
	}
	currentSchema, err := u.repository.SchemaVersion(ctx)
	if err != nil {
		return BundleImportResult{}, xerrors.Errorf("failed to resolve current schema version: %w", err)
	}
	if manifest.BundleSchemaVersion > currentSchema {
		return BundleImportResult{}, xerrors.Errorf(
			"bundle was exported from a newer schema (%d) than this store runs (%d); upgrade Traceary before importing",
			manifest.BundleSchemaVersion, currentSchema,
		)
	}

	result := BundleImportResult{BundleSchemaVersion: manifest.BundleSchemaVersion}
	tx, err := u.repository.BeginBundleImport(ctx)
	if err != nil {
		return result, xerrors.Errorf("failed to begin bundle import transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	for _, entry := range tableEntries {
		importer, ok := registry[entry.TableName]
		if !ok {
			continue
		}
		raw := files[entry.File]
		rows, err := importer.Decode(bytes.NewReader(raw))
		if err != nil {
			return result, xerrors.Errorf("failed to decode %s rows: %w", entry.TableName, err)
		}
		if entry.RowCount >= 0 && len(rows) != entry.RowCount {
			return result, xerrors.Errorf(
				"bundle table %s row count mismatch (manifest=%d, decoded=%d)",
				entry.TableName, entry.RowCount, len(rows),
			)
		}
		imported, skipped, err := importer.Apply(ctx, tx, rows, bundleImportPolicy{OnConflict: onConflict, MissingParent: missingParent, OrphanEdges: orphanEdges})
		if err != nil {
			return result, xerrors.Errorf("failed to import %s: %w", entry.TableName, err)
		}
		switch entry.TableName {
		case "sessions":
			result.SessionsImported += imported
			result.SessionsSkipped += skipped
		case "events":
			result.EventsImported += imported
			result.EventsSkipped += skipped
		case "command_audits":
			result.CommandAuditsImported += imported
			result.CommandAuditsSkipped += skipped
		case "memories":
			result.MemoriesImported += imported
			result.MemoriesSkipped += skipped
		case "memory_edges":
			result.MemoryEdgesImported += imported
			result.MemoryEdgesSkipped += skipped
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, xerrors.Errorf("failed to commit bundle import transaction: %w", err)
	}
	committed = true
	return result, nil
}

// ---- Table registry + NDJSON row + tar.gz + AEAD helpers ----

type bundleRow any

type bundleExportInputRows struct {
	Sessions      []*model.Session
	Events        []*model.Event
	CommandAudits []*model.CommandAudit
	Memories      []apptypes.MemoryDetails
	MemoryEdges   []*model.MemoryEdge
}

type bundleImportPolicy struct {
	OnConflict    BundleConflictPolicy
	MissingParent BundleMissingParentPolicy
	OrphanEdges   BundleOrphanEdgesPolicy
}

type bundleTableImporter interface {
	Name() string
	FileName() string
	Export(context.Context, bundleExportInputRows) (*bytes.Buffer, error)
	Decode(io.Reader) ([]bundleRow, error)
	Apply(context.Context, BundleImportTransaction, []bundleRow, bundleImportPolicy) (imported int, skipped int, err error)
}
