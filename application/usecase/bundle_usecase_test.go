package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"

	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/xerrors"
)

// Fake event query service that returns a fixed slice.
type fakeEventQuery struct{ events []*model.Event }

func (f fakeEventQuery) ListRecent(context.Context, int, int, types.EventKind, types.Client, types.Agent, types.SessionID, types.Workspace, bool, time.Time, time.Time, string) ([]*model.Event, error) {
	return f.events, nil
}
func (f fakeEventQuery) ListWindow(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return f.events, nil
}
func (f fakeEventQuery) Search(context.Context, string, types.Workspace, types.SessionID, types.Client, types.Agent, types.EventKind, time.Time, time.Time, int, int, bool) ([]*model.Event, error) {
	return nil, nil
}
func (f fakeEventQuery) GetContext(context.Context, types.Workspace, types.SessionID, int) ([]*model.Event, error) {
	return nil, nil
}
func (f fakeEventQuery) GetDetails(context.Context, types.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}
func (f fakeEventQuery) ListTimelineBlocks(context.Context, types.Workspace, time.Time, time.Time, int, int) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}

// Fake repository that tracks imports + schema version.
type fakeBundleRepo struct {
	schema   int
	events   map[string]bool
	forceErr error
}

func (r *fakeBundleRepo) SchemaVersion(context.Context) (int, error) { return r.schema, nil }
func (r *fakeBundleRepo) BeginBundleImport(context.Context) (usecase.BundleImportTransaction, error) {
	return &fakeBundleTx{repo: r}, nil
}

type fakeBundleTx struct{ repo *fakeBundleRepo }

func (tx *fakeBundleTx) ImportEvent(_ context.Context, event *model.Event, policy usecase.BundleConflictPolicy) (bool, error) {
	r := tx.repo
	if r.forceErr != nil {
		return false, r.forceErr
	}
	if r.events == nil {
		r.events = map[string]bool{}
	}
	id := event.EventID().String()
	if r.events[id] {
		if policy == usecase.BundleConflictError {
			return false, xerrors.Errorf("event conflict")
		}
		if policy == usecase.BundleConflictReplace {
			return true, nil
		}
		return false, nil
	}
	r.events[id] = true
	return true, nil
}
func (tx *fakeBundleTx) Commit(context.Context) error   { return nil }
func (tx *fakeBundleTx) Rollback(context.Context) error { return nil }

func mustEvent(t *testing.T, id string, ts time.Time) *model.Event {
	t.Helper()
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		t.Fatalf("EventIDFrom: %v", err)
	}
	agent, err := types.AgentFrom("test")
	if err != nil {
		t.Fatalf("AgentFrom: %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-x")
	if err != nil {
		t.Fatalf("SessionIDFrom: %v", err)
	}
	return model.EventOf(
		eventID, types.EventKindNote, types.Client("cli"), agent,
		sessionID, types.Workspace("ws"),
		"body-"+id, ts,
	)
}

func TestBundleUsecase_RoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	events := []*model.Event{
		mustEvent(t, "e-1", ts),
		mustEvent(t, "e-2", ts.Add(time.Minute)),
	}

	exporter := fakeEventQuery{events: events}
	exportRepo := &fakeBundleRepo{schema: 13}
	exportUC := usecase.NewBundleUsecase(exporter, exportRepo, func() time.Time { return ts })

	tmpDir := t.TempDir()
	out := filepath.Join(tmpDir, "bundle.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	importRepo := &fakeBundleRepo{schema: 13}
	// Import uses the same event query interface; it does not read
	// events from there, but constructor requires one.
	importUC := usecase.NewBundleUsecase(exporter, importRepo, func() time.Time { return ts })
	result, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.EventsImported != 2 || result.EventsSkipped != 0 {
		t.Fatalf("Import result = %+v, want 2 imported / 0 skipped", result)
	}

	// Re-import: both should be skipped.
	result2, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
	})
	if err != nil {
		t.Fatalf("Re-Import: %v", err)
	}
	if result2.EventsImported != 0 || result2.EventsSkipped != 2 {
		t.Fatalf("Re-Import result = %+v, want 0 imported / 2 skipped", result2)
	}
}

func TestBundleUsecase_ExportWritesManifestV2Tables(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	events := []*model.Event{mustEvent(t, "e-1", ts)}
	uc := usecase.NewBundleUsecase(fakeEventQuery{events: events}, &fakeBundleRepo{schema: 13}, func() time.Time { return ts })

	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := uc.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	files := openTestBundle(t, out, []byte("pass1"))
	var manifest struct {
		ManifestVersion        int `json:"manifest_version"`
		MinReaderSchemaVersion int `json:"min_reader_schema_version"`
		Tables                 map[string]struct {
			TableName string `json:"table_name"`
			File      string `json:"file"`
			RowCount  int    `json:"row_count"`
			Checksum  string `json:"checksum"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	entry := manifest.Tables["events"]
	if manifest.ManifestVersion != 2 || manifest.MinReaderSchemaVersion != 1 {
		t.Fatalf("manifest versions = %+v, want manifest=2 min_reader=1", manifest)
	}
	if entry.TableName != "events" || entry.File != "events.ndjson" || entry.RowCount != 1 {
		t.Fatalf("events table entry = %+v", entry)
	}
	if got := hashForTest(files["events.ndjson"]); got != entry.Checksum {
		t.Fatalf("events checksum = %s, want %s", entry.Checksum, got)
	}
}

func TestBundleUsecase_ImportsV1Manifest(t *testing.T) {
	t.Parallel()

	row := mustEvent(t, "e-v1", time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC))
	eventsBuf := &bytes.Buffer{}
	if err := json.NewEncoder(eventsBuf).Encode(map[string]string{
		"event_id":   row.EventID().String(),
		"kind":       row.Kind().String(),
		"client":     row.Client().String(),
		"agent":      row.Agent().String(),
		"session_id": row.SessionID().String(),
		"workspace":  row.Workspace().String(),
		"body":       row.Body(),
		"created_at": row.CreatedAt().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("encode row: %v", err)
	}
	bundle := buildBundleWithManifestAndEvents(t, 1, map[string]string{
		"events.ndjson": hashForTest(eventsBuf.Bytes()),
	}, eventsBuf.Bytes(), nil)
	in := filepath.Join(t.TempDir(), "v1.tbun")
	if err := os.WriteFile(in, bundle, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	uc := usecase.NewBundleUsecase(fakeEventQuery{}, &fakeBundleRepo{schema: 13}, nil)
	result, err := uc.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     in,
		Passphrase: []byte("testpass"),
	})
	if err != nil {
		t.Fatalf("Import v1: %v", err)
	}
	if result.EventsImported != 1 || result.EventsSkipped != 0 {
		t.Fatalf("Import v1 result = %+v, want 1 imported / 0 skipped", result)
	}
}

func TestBundleUsecase_OnConflictErrorFails(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	events := []*model.Event{mustEvent(t, "e-1", ts)}
	repo := &fakeBundleRepo{schema: 13}
	uc := usecase.NewBundleUsecase(fakeEventQuery{events: events}, repo, nil)
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := uc.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if _, err := uc.Import(context.Background(), usecase.BundleImportOptions{InPath: out, Passphrase: []byte("pass1")}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	_, err := uc.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
		OnConflict: usecase.BundleConflictError,
	})
	if err == nil {
		t.Fatalf("Import with on-conflict=error unexpectedly succeeded")
	}
}

func TestBundleUsecase_WrongPassphraseFailsAEAD(t *testing.T) {
	t.Parallel()

	exporter := fakeEventQuery{events: []*model.Event{mustEvent(t, "e-1", time.Now())}}
	repo := &fakeBundleRepo{schema: 13}
	uc := usecase.NewBundleUsecase(exporter, repo, nil)

	out := filepath.Join(t.TempDir(), "b.tbun")
	if err := uc.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("correct"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	_, err := uc.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("wrong"),
	})
	if err == nil {
		t.Fatalf("Import with wrong passphrase unexpectedly succeeded")
	}
}

func TestBundleUsecase_RejectsMissingRequiredChecksum(t *testing.T) {
	t.Parallel()

	// Craft a bundle whose manifest omits the events.ndjson
	// checksum; Import must refuse rather than silently skip
	// verification.
	bundle := buildBundleWithManifest(t, map[string]string{"something-else.ndjson": ""})

	uc := usecase.NewBundleUsecase(
		fakeEventQuery{},
		&fakeBundleRepo{schema: 13},
		nil,
	)
	in := filepath.Join(t.TempDir(), "b.tbun")
	if err := os.WriteFile(in, bundle, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := uc.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     in,
		Passphrase: []byte("testpass"),
	})
	if err == nil {
		t.Fatalf("Import unexpectedly succeeded without required checksum")
	}
}

func TestBundleUsecase_RejectsNewerSchema(t *testing.T) {
	t.Parallel()

	exporter := fakeEventQuery{events: []*model.Event{mustEvent(t, "e-1", time.Now())}}
	exportRepo := &fakeBundleRepo{schema: 99} // far-future schema
	exportUC := usecase.NewBundleUsecase(exporter, exportRepo, nil)

	out := filepath.Join(t.TempDir(), "b.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	importRepo := &fakeBundleRepo{schema: 5} // older receiver
	importUC := usecase.NewBundleUsecase(exporter, importRepo, nil)
	_, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass"),
	})
	if err == nil {
		t.Fatalf("Import unexpectedly accepted a bundle from a newer schema")
	}
}

// buildBundleWithManifest produces a minimal bundle whose manifest
// lists the given checksum entries. Used by the
// RejectsMissingRequiredChecksum test to simulate a bundle that
// skipped the verification gate.
func buildBundleWithManifest(t *testing.T, checksums map[string]string) []byte {
	t.Helper()
	return buildBundleWithManifestAndEvents(t, 1, checksums, []byte(""), nil)
}

func buildBundleWithManifestAndEvents(
	t *testing.T,
	manifestVersion int,
	checksums map[string]string,
	events []byte,
	tables map[string]any,
) []byte {
	t.Helper()
	files := map[string][]byte{
		"events.ndjson": events,
	}
	manifest := map[string]any{
		"manifest_version": manifestVersion,
		"created_at":       time.Now().UTC(),
		"schema_version":   13,
		"filters":          map[string]any{},
	}
	if checksums != nil {
		manifest["file_checksums"] = checksums
	}
	if tables != nil {
		manifest["tables"] = tables
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	files["manifest.json"] = manifestBytes

	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)
	// Deterministic order.
	for _, name := range []string{"events.ndjson", "manifest.json"} {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatalf("tar hdr: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	magic := []byte{'T', 'R', 'B', 'U', 'N', 'D', 'L', 'E'}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("rand salt: %v", err)
	}
	key := argon2.IDKey([]byte("testpass"), salt, 3, 64*1024, 4, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		t.Fatalf("aead: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand nonce: %v", err)
	}
	ciphertext := aead.Seal(nil, nonce, buf.Bytes(), magic)

	out := &bytes.Buffer{}
	out.Write(magic)
	out.WriteByte(1)
	out.Write(salt)
	out.Write(nonce)
	out.Write(ciphertext)
	return out.Bytes()
}

func openTestBundle(t *testing.T, path string, passphrase []byte) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	magic := []byte{'T', 'R', 'B', 'U', 'N', 'D', 'L', 'E'}
	cursor := len(magic) + 1
	salt := data[cursor : cursor+16]
	cursor += 16
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 4, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		t.Fatalf("aead: %v", err)
	}
	nonce := data[cursor : cursor+aead.NonceSize()]
	cursor += aead.NonceSize()
	plaintext, err := aead.Open(nil, nonce, data[cursor:], magic)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	gzr, err := gzip.NewReader(bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = gzr.Close() }()
	tr := tar.NewReader(gzr)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %s: %v", hdr.Name, err)
		}
		files[hdr.Name] = content
	}
	return files
}

func hashForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
