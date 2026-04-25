// Package usecase — bundle usecase implements the v0.9 portability
// primitive introduced for #572: a local-first, encrypted,
// content-verifiable archive that operators can move between their
// machines through any file-transport they already have (AirDrop,
// scp, Syncthing, etc.). Traceary never ships its own transport.
//
// v0.9 scope: events only. Memory / session / command-audit
// portability lands as follow-up work — see docs/operations for the
// published roadmap.
package usecase

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
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

// BundleMissingParentPolicy is reserved for multi-table bundle imports
// where child rows can reference parents that are absent from the bundle
// and the destination store. v2 only ships events, but wiring this now
// keeps #738/#739/#740 on the same CLI/API surface.
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

// BundleImportOptions controls a single Import call.
type BundleImportOptions struct {
	// InPath is the filesystem path of the archive to read.
	InPath string
	// Passphrase decrypts the archive. Must match what Export used.
	Passphrase []byte
	// OnConflict controls UNIQUE collisions. Empty defaults to skip for
	// v0.9-compatible idempotent re-imports.
	OnConflict BundleConflictPolicy
	// MissingParent is wired for forthcoming sessions / memories / edges
	// importers. Empty defaults to reject.
	MissingParent BundleMissingParentPolicy
}

// BundleImportResult summarises what changed during Import.
type BundleImportResult struct {
	// EventsImported / EventsSkipped count events that were newly
	// written vs dropped because of a pre-existing (event_id)
	// collision.
	EventsImported int
	EventsSkipped  int
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
	// BeginBundleImport starts the single transaction used by all table
	// importers in registry order.
	BeginBundleImport(ctx context.Context) (BundleImportTransaction, error)
}

// BundleImportTransaction is the write-side transaction shared by all
// bundle table importers.
type BundleImportTransaction interface {
	ImportEvent(ctx context.Context, event *model.Event, policy BundleConflictPolicy) (bool, error)
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

	eventsImporter := u.bundleTableRegistry()["events"]
	eventsBuf, err := eventsImporter.Export(ctx, events)
	if err != nil {
		return xerrors.Errorf("failed to encode events: %w", err)
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
		},
		Filters: bundleFilters{
			Since:     formatOptionalTime(opts.Since),
			Until:     formatOptionalTime(opts.Until),
			Workspace: opts.Workspace.String(),
		},
		Tables: map[string]bundleTableEntry{
			"events": {
				TableName: "events",
				File:      eventsImporter.FileName(),
				RowCount:  len(events),
				Checksum:  hashSHA256(eventsBuf.Bytes()),
			},
		},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode manifest: %w", err)
	}

	plaintext, err := encodeTarGz(map[string][]byte{
		"manifest.json": manifestBytes,
		"events.ndjson": eventsBuf.Bytes(),
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
	if _, err := opts.MissingParent.normalized(); err != nil {
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
		imported, skipped, err := importer.Apply(ctx, tx, rows, onConflict)
		if err != nil {
			return result, xerrors.Errorf("failed to import %s: %w", entry.TableName, err)
		}
		if entry.TableName == "events" {
			result.EventsImported += imported
			result.EventsSkipped += skipped
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

type bundleTableImporter interface {
	Name() string
	FileName() string
	Export(context.Context, []*model.Event) (*bytes.Buffer, error)
	Decode(io.Reader) ([]bundleRow, error)
	Apply(context.Context, BundleImportTransaction, []bundleRow, BundleConflictPolicy) (imported int, skipped int, err error)
}

func (u *bundleUsecase) bundleTableRegistry() map[string]bundleTableImporter {
	events := bundleEventsTable{}
	return map[string]bundleTableImporter{
		events.Name(): events,
	}
}

type bundleEventsTable struct{}

func (bundleEventsTable) Name() string { return "events" }

func (bundleEventsTable) FileName() string { return "events.ndjson" }

func (bundleEventsTable) Export(_ context.Context, events []*model.Event) (*bytes.Buffer, error) {
	return encodeEventsNDJSON(events)
}

func (bundleEventsTable) Decode(r io.Reader) ([]bundleRow, error) {
	decoder := json.NewDecoder(r)
	rows := []bundleRow{}
	for decoder.More() {
		var row bundleEventRow
		if err := decoder.Decode(&row); err != nil {
			return nil, xerrors.Errorf("event row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (bundleEventsTable) Apply(
	ctx context.Context,
	tx BundleImportTransaction,
	rows []bundleRow,
	policy BundleConflictPolicy,
) (int, int, error) {
	imported := 0
	skipped := 0
	for _, generic := range rows {
		row, ok := generic.(bundleEventRow)
		if !ok {
			return imported, skipped, xerrors.Errorf("unexpected events row type %T", generic)
		}
		event, err := row.toEvent()
		if err != nil {
			return imported, skipped, xerrors.Errorf("restore event: %w", err)
		}
		didImport, err := tx.ImportEvent(ctx, event, policy)
		if err != nil {
			return imported, skipped, xerrors.Errorf("event %s: %w", event.EventID(), err)
		}
		if didImport {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, nil
}

func manifestTableEntries(
	manifest bundleManifest,
	registry map[string]bundleTableImporter,
) ([]bundleTableEntry, error) {
	switch manifest.ManifestVersion {
	case 1:
		const requiredChecksumFile = "events.ndjson"
		if _, ok := manifest.FileChecksums[requiredChecksumFile]; !ok {
			return nil, xerrors.Errorf(
				"bundle manifest is missing a checksum for %s (required for manifest_version=%d)",
				requiredChecksumFile, manifest.ManifestVersion,
			)
		}
		files := make([]string, 0, len(manifest.FileChecksums))
		for file := range manifest.FileChecksums {
			files = append(files, file)
		}
		sort.Strings(files)
		entries := make([]bundleTableEntry, 0, len(files))
		for _, file := range files {
			entry := bundleTableEntry{
				File:     file,
				Checksum: manifest.FileChecksums[file],
				RowCount: -1,
			}
			if file == requiredChecksumFile {
				entry.TableName = "events"
			}
			entries = append(entries, entry)
		}
		return entries, nil
	case 2:
		if len(manifest.Tables) == 0 {
			return nil, xerrors.Errorf("bundle manifest has no table entries")
		}
		names := make([]string, 0, len(manifest.Tables))
		for name := range manifest.Tables {
			names = append(names, name)
		}
		sort.Strings(names)
		entries := make([]bundleTableEntry, 0, len(names))
		for _, name := range names {
			entry := manifest.Tables[name]
			if entry.TableName == "" {
				entry.TableName = name
			}
			if entry.TableName != name {
				return nil, xerrors.Errorf("bundle table key %s does not match table_name %s", name, entry.TableName)
			}
			if _, ok := registry[entry.TableName]; !ok {
				return nil, xerrors.Errorf("bundle table %s is not supported by this build", entry.TableName)
			}
			if entry.File == "" {
				return nil, xerrors.Errorf("bundle table %s has an empty file", entry.TableName)
			}
			if entry.Checksum == "" {
				return nil, xerrors.Errorf("bundle table %s has an empty checksum", entry.TableName)
			}
			if entry.RowCount < 0 {
				return nil, xerrors.Errorf("bundle table %s has a negative row_count", entry.TableName)
			}
			entries = append(entries, entry)
		}
		return entries, nil
	default:
		return nil, xerrors.Errorf("unsupported bundle manifest version %d", manifest.ManifestVersion)
	}
}

func verifyBundleFiles(files map[string][]byte, entries []bundleTableEntry) error {
	covered := map[string]bool{}
	for _, entry := range entries {
		data, present := files[entry.File]
		if !present {
			return xerrors.Errorf("bundle missing %s referenced by manifest", entry.File)
		}
		got := hashSHA256(data)
		if got != entry.Checksum {
			return xerrors.Errorf(
				"checksum mismatch on %s (want %s, got %s)", entry.File, entry.Checksum, got,
			)
		}
		covered[entry.File] = true
	}
	for name := range files {
		if name == "manifest.json" {
			continue
		}
		if !covered[name] {
			return xerrors.Errorf("bundle entry %s is not covered by a manifest checksum", name)
		}
	}
	return nil
}

type bundleEventRow struct {
	EventID    string `json:"event_id"`
	Kind       string `json:"kind"`
	Client     string `json:"client"`
	Agent      string `json:"agent"`
	SessionID  string `json:"session_id"`
	Workspace  string `json:"workspace"`
	Body       string `json:"body"`
	SourceHook string `json:"source_hook,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func (r bundleEventRow) toEvent() (*model.Event, error) {
	eventID, err := types.EventIDFrom(r.EventID)
	if err != nil {
		return nil, xerrors.Errorf("event_id: %w", err)
	}
	kind, err := types.EventKindFrom(r.Kind)
	if err != nil {
		return nil, xerrors.Errorf("kind: %w", err)
	}
	agent, err := types.AgentFrom(r.Agent)
	if err != nil {
		return nil, xerrors.Errorf("agent: %w", err)
	}
	sessionID, err := types.SessionIDFrom(r.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session_id: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, xerrors.Errorf("created_at: %w", err)
	}
	return model.EventOfWithSourceHook(
		eventID, kind,
		types.Client(r.Client), agent, sessionID,
		types.Workspace(r.Workspace),
		r.Body, createdAt, r.SourceHook,
	), nil
}

func encodeEventsNDJSON(events []*model.Event) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	// Sort to make output reproducible for a given filter set.
	sort.Slice(events, func(i, j int) bool {
		if !events[i].CreatedAt().Equal(events[j].CreatedAt()) {
			return events[i].CreatedAt().Before(events[j].CreatedAt())
		}
		return events[i].EventID().String() < events[j].EventID().String()
	})
	enc := json.NewEncoder(buf)
	for _, event := range events {
		if err := enc.Encode(bundleEventRow{
			EventID:    event.EventID().String(),
			Kind:       event.Kind().String(),
			Client:     event.Client().String(),
			Agent:      event.Agent().String(),
			SessionID:  event.SessionID().String(),
			Workspace:  event.Workspace().String(),
			Body:       event.Body(),
			SourceHook: event.SourceHook(),
			CreatedAt:  event.CreatedAt().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return nil, xerrors.Errorf("encode event: %w", err)
		}
	}
	return buf, nil
}

func encodeTarGz(files map[string][]byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(data)),
		}); err != nil {
			return nil, xerrors.Errorf("tar header for %s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, xerrors.Errorf("tar write %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, xerrors.Errorf("tar close: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, xerrors.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func decodeTarGz(data []byte) (map[string][]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, xerrors.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()
	tr := tar.NewReader(gzr)
	out := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, xerrors.Errorf("tar next: %w", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, xerrors.Errorf("tar read %s: %w", hdr.Name, err)
		}
		out[hdr.Name] = content
	}
	return out, nil
}

// sealBundle encrypts plaintext with a key derived from the
// passphrase via Argon2id, using XChaCha20-Poly1305 (24-byte nonce)
// so we can safely generate nonces randomly per bundle. Argon2id
// parameters (3 iterations, 64 MiB, 4 lanes) follow the OWASP
// general-purpose recommendation and cost ~100ms on typical hardware.
func sealBundle(plaintext, passphrase []byte) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, xerrors.Errorf("salt: %w", err)
	}
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 4, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, xerrors.Errorf("aead init: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, xerrors.Errorf("nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, bundleMagic)

	out := &bytes.Buffer{}
	out.Write(bundleMagic)
	out.WriteByte(bundleEnvelope)
	out.Write(salt)
	out.Write(nonce)
	out.Write(ciphertext)
	return out.Bytes(), nil
}

func openBundle(data, passphrase []byte) ([]byte, error) {
	headerSize := len(bundleMagic) + 1 + 16 + 24
	if len(data) < headerSize {
		return nil, xerrors.Errorf("bundle is too short to be a Traceary archive")
	}
	if !bytes.Equal(data[:len(bundleMagic)], bundleMagic) {
		return nil, xerrors.Errorf("bundle does not have the Traceary magic prefix")
	}
	cursor := len(bundleMagic)
	version := data[cursor]
	cursor++
	if version != bundleEnvelope {
		return nil, xerrors.Errorf("unsupported bundle envelope version %d", version)
	}
	salt := data[cursor : cursor+16]
	cursor += 16
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 4, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, xerrors.Errorf("aead init: %w", err)
	}
	nonce := data[cursor : cursor+aead.NonceSize()]
	cursor += aead.NonceSize()
	ciphertext := data[cursor:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, bundleMagic)
	if err != nil {
		return nil, xerrors.Errorf("decryption failed (wrong passphrase or corrupt bundle): %w", err)
	}
	return plaintext, nil
}

func hashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
