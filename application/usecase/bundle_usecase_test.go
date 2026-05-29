package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	"log/slog"

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
	schema                    int
	events                    map[string]bool
	sessions                  map[string]*model.Session
	commandAudits             map[string]*model.CommandAudit
	exportSessions            []*model.Session
	exportCommandAudits       []*model.CommandAudit
	memories                  map[string]*model.Memory
	memoryEdges               map[string]*model.MemoryEdge
	exportMemories            []apptypes.MemoryDetails
	exportMemoryEdges         []*model.MemoryEdge
	enforceMemorySupersedesFK bool
	forceErr                  error
}

func (r *fakeBundleRepo) SchemaVersion(context.Context) (int, error) { return r.schema, nil }
func (r *fakeBundleRepo) ListBundleSessions(context.Context) ([]*model.Session, error) {
	return r.exportSessions, nil
}
func (r *fakeBundleRepo) ListBundleCommandAudits(context.Context) ([]*model.CommandAudit, error) {
	return r.exportCommandAudits, nil
}
func (r *fakeBundleRepo) ListBundleMemories(context.Context) ([]apptypes.MemoryDetails, error) {
	return r.exportMemories, nil
}
func (r *fakeBundleRepo) ListBundleMemoryEdges(context.Context) ([]*model.MemoryEdge, error) {
	return r.exportMemoryEdges, nil
}
func (r *fakeBundleRepo) BeginBundleImport(context.Context) (usecase.BundleImportTransaction, error) {
	tx := &fakeBundleTx{
		repo:        r,
		events:      map[string]bool{},
		memories:    map[string]*model.Memory{},
		memoryEdges: map[string]*model.MemoryEdge{},
	}
	for id, value := range r.events {
		tx.events[id] = value
	}
	for id, value := range r.memories {
		tx.memories[id] = value
	}
	for id, value := range r.memoryEdges {
		tx.memoryEdges[id] = value
	}
	return tx, nil
}

type fakeBundleTx struct {
	repo        *fakeBundleRepo
	events      map[string]bool
	memories    map[string]*model.Memory
	memoryEdges map[string]*model.MemoryEdge
}

func (tx *fakeBundleTx) ImportSession(_ context.Context, session *model.Session, policy usecase.BundleConflictPolicy, missingParent usecase.BundleMissingParentPolicy) (bool, error) {
	r := tx.repo
	if r.forceErr != nil {
		return false, r.forceErr
	}
	if r.sessions == nil {
		r.sessions = map[string]*model.Session{}
	}
	if parent := session.ParentSessionID().String(); parent != "" {
		if _, ok := r.sessions[parent]; !ok {
			switch missingParent {
			case usecase.BundleMissingParentSkip:
				return false, nil
			case usecase.BundleMissingParentBackfill:
				r.sessions[parent] = model.SessionOf(session.ParentSessionID(), session.StartedAt(), types.None[time.Time](), session.Client(), session.Agent(), session.Workspace(), "traceary:bundle-backfilled-parent", "", "")
			default:
				return false, xerrors.Errorf("parent session not found: %s", parent)
			}
		}
	}
	id := session.SessionID().String()
	if _, ok := r.sessions[id]; ok {
		if policy == usecase.BundleConflictError {
			return false, xerrors.Errorf("session conflict")
		}
		if policy == usecase.BundleConflictReplace {
			r.sessions[id] = session
			return true, nil
		}
		return false, nil
	}
	r.sessions[id] = session
	return true, nil
}

func (tx *fakeBundleTx) ImportEvent(_ context.Context, event *model.Event, policy usecase.BundleConflictPolicy) (bool, error) {
	r := tx.repo
	if r.forceErr != nil {
		return false, r.forceErr
	}
	id := event.EventID().String()
	if tx.events[id] {
		if policy == usecase.BundleConflictError {
			return false, xerrors.Errorf("event conflict")
		}
		if policy == usecase.BundleConflictReplace {
			return true, nil
		}
		return false, nil
	}
	tx.events[id] = true
	return true, nil
}

func (tx *fakeBundleTx) ImportCommandAudit(_ context.Context, audit *model.CommandAudit, policy usecase.BundleConflictPolicy) (bool, error) {
	r := tx.repo
	if r.forceErr != nil {
		return false, r.forceErr
	}
	if r.commandAudits == nil {
		r.commandAudits = map[string]*model.CommandAudit{}
	}
	id := audit.EventID().String()
	if !tx.events[id] {
		return false, xerrors.Errorf("foreign key constraint failed: event_id %s is missing", id)
	}
	if _, ok := r.commandAudits[id]; ok {
		if policy == usecase.BundleConflictError {
			return false, xerrors.Errorf("command audit conflict")
		}
		if policy == usecase.BundleConflictReplace {
			r.commandAudits[id] = audit
			return true, nil
		}
		return false, nil
	}
	r.commandAudits[id] = audit
	return true, nil
}
func (tx *fakeBundleTx) ImportMemory(_ context.Context, memory *model.Memory, policy usecase.BundleConflictPolicy) (bool, error) {
	r := tx.repo
	if r.forceErr != nil {
		return false, r.forceErr
	}
	id := memory.MemoryID().String()
	if _, ok := tx.memories[id]; ok {
		if policy == usecase.BundleConflictError {
			return false, xerrors.Errorf("memory conflict")
		}
		if policy == usecase.BundleConflictReplace {
			tx.memories[id] = memory
			return true, nil
		}
		return false, nil
	}
	if r.enforceMemorySupersedesFK {
		if supersedes, ok := memory.Supersedes().Value(); ok {
			if _, exists := tx.memories[supersedes.String()]; !exists {
				return false, xerrors.Errorf("foreign key constraint failed: supersedes_memory_id %s is missing", supersedes)
			}
		}
	}
	tx.memories[id] = memory
	return true, nil
}
func (tx *fakeBundleTx) MemoryExists(_ context.Context, memoryID types.MemoryID) (bool, error) {
	_, ok := tx.memories[memoryID.String()]
	return ok, nil
}
func (tx *fakeBundleTx) MemoryEdgeExists(_ context.Context, edgeID types.MemoryEdgeID) (bool, error) {
	_, ok := tx.memoryEdges[edgeID.String()]
	return ok, nil
}
func (tx *fakeBundleTx) ImportMemoryEdge(_ context.Context, edge *model.MemoryEdge, policy usecase.BundleConflictPolicy) (bool, error) {
	r := tx.repo
	if r.forceErr != nil {
		return false, r.forceErr
	}
	id := edge.EdgeID().String()
	if _, ok := tx.memoryEdges[id]; ok {
		if policy == usecase.BundleConflictError {
			return false, xerrors.Errorf("memory edge conflict")
		}
		if policy == usecase.BundleConflictReplace {
			tx.memoryEdges[id] = edge
			return true, nil
		}
		return false, nil
	}
	tx.memoryEdges[id] = edge
	return true, nil
}
func (tx *fakeBundleTx) Commit(context.Context) error {
	tx.repo.events = tx.events
	tx.repo.memories = tx.memories
	tx.repo.memoryEdges = tx.memoryEdges
	return nil
}
func (tx *fakeBundleTx) Rollback(context.Context) error { return nil }

func mustEvent(t *testing.T, id string, ts time.Time) *model.Event {
	t.Helper()
	return mustEventInSessionWorkspace(t, id, ts, "session-x", types.Workspace("ws"))
}

func mustEventInSessionWorkspace(t *testing.T, id string, ts time.Time, session string, workspace types.Workspace) *model.Event {
	t.Helper()
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		t.Fatalf("EventIDFrom: %v", err)
	}
	agent, err := types.AgentFrom("test")
	if err != nil {
		t.Fatalf("AgentFrom: %v", err)
	}
	sessionID, err := types.SessionIDFrom(session)
	if err != nil {
		t.Fatalf("SessionIDFrom: %v", err)
	}
	return model.EventOf(
		eventID, types.EventKindNote, types.Client("cli"), agent,
		sessionID, workspace,
		"body-"+id, ts,
	)
}

func mustBundleMemoryID(t *testing.T, id string) types.MemoryID {
	t.Helper()
	memoryID, err := types.MemoryIDFrom(id)
	if err != nil {
		t.Fatalf("MemoryIDFrom: %v", err)
	}
	return memoryID
}

func mustMemoryEdge(t *testing.T, id, fromID, toID string, ts time.Time) *model.MemoryEdge {
	t.Helper()
	edgeID, err := types.MemoryEdgeIDFrom(id)
	if err != nil {
		t.Fatalf("MemoryEdgeIDFrom: %v", err)
	}
	edge, err := model.NewMemoryEdge(
		edgeID,
		mustBundleMemoryID(t, fromID),
		mustBundleMemoryID(t, toID),
		types.MemoryEdgeRelationSupports,
		ts.Add(-time.Minute),
		types.None[time.Time](),
		ts,
	)
	if err != nil {
		t.Fatalf("NewMemoryEdge: %v", err)
	}
	return edge
}

func mustSession(t *testing.T, id, parent string, ts time.Time) *model.Session {
	t.Helper()
	return mustSessionInWorkspace(t, id, parent, ts, types.Workspace("ws"))
}

func mustSessionInWorkspace(t *testing.T, id, parent string, ts time.Time, workspace types.Workspace) *model.Session {
	t.Helper()
	sessionID, err := types.SessionIDFrom(id)
	if err != nil {
		t.Fatalf("SessionIDFrom: %v", err)
	}
	agent, err := types.AgentFrom("test")
	if err != nil {
		t.Fatalf("AgentFrom: %v", err)
	}
	return model.SessionOf(
		sessionID,
		ts,
		types.None[time.Time](),
		types.Client("cli"),
		agent,
		workspace,
		"label-"+id,
		"summary-"+id,
		types.SessionID(parent),
	)
}

func mustCommandAudit(t *testing.T, eventID string) *model.CommandAudit {
	t.Helper()
	id, err := types.EventIDFrom(eventID)
	if err != nil {
		t.Fatalf("EventIDFrom: %v", err)
	}
	audit, err := model.NewCommandAudit(id, "go test ./...", "stdin", "stdout", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit: %v", err)
	}
	audit.SetExitCode(types.Some(0))
	return audit
}

func mustMemoryDetails(t *testing.T, id string, status types.MemoryStatus, ts time.Time) apptypes.MemoryDetails {
	t.Helper()
	return mustMemoryDetailsSuperseding(t, id, "", status, ts)
}

func mustMemoryDetailsSuperseding(
	t *testing.T,
	id string,
	supersedesID string,
	status types.MemoryStatus,
	ts time.Time,
) apptypes.MemoryDetails {
	t.Helper()
	memoryID, err := types.MemoryIDFrom(id)
	if err != nil {
		t.Fatalf("MemoryIDFrom: %v", err)
	}
	evidence, err := types.EvidenceRefFrom(types.EvidenceRefKindEvent, "event-1")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	artifact, err := types.ArtifactRefFrom(types.ArtifactRefKindFile, "/tmp/artifact.txt")
	if err != nil {
		t.Fatalf("ArtifactRefFrom: %v", err)
	}
	supersedes := types.None[types.MemoryID]()
	if supersedesID != "" {
		parentID, err := types.MemoryIDFrom(supersedesID)
		if err != nil {
			t.Fatalf("MemoryIDFrom(supersedes): %v", err)
		}
		supersedes = types.Some(parentID)
	}
	memory := model.MemoryOf(
		memoryID,
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("ws")),
		"Bundle memory "+id,
		status,
		types.ConfidenceHigh,
		types.MemorySourceManual,
		[]types.EvidenceRef{evidence},
		[]types.ArtifactRef{artifact},
		supersedes,
		types.None[time.Time](),
		ts.Add(-time.Hour),
		types.None[time.Time](),
		ts,
		ts.Add(time.Minute),
	)
	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		t.Fatalf("MemoryDetailsFrom: %v", err)
	}
	return details
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

func TestBundleUsecase_RoundTripSessionsAndCommandAudits(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	parent := mustSession(t, "session-parent", "", ts)
	child := mustSession(t, "session-child", "session-parent", ts.Add(time.Minute))
	events := []*model.Event{mustEvent(t, "e-1", ts)}
	exportRepo := &fakeBundleRepo{
		schema:              13,
		exportSessions:      []*model.Session{child, parent},
		exportCommandAudits: []*model.CommandAudit{mustCommandAudit(t, "e-1")},
	}
	exportUC := usecase.NewBundleUsecase(fakeEventQuery{events: events}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	files := openTestBundle(t, out, []byte("pass1"))
	if _, ok := files["sessions.ndjson"]; !ok {
		t.Fatalf("bundle missing sessions.ndjson")
	}
	if _, ok := files["command_audits.ndjson"]; !ok {
		t.Fatalf("bundle missing command_audits.ndjson")
	}

	importRepo := &fakeBundleRepo{schema: 13}
	importUC := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, nil)
	result, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.SessionsImported != 2 || result.CommandAuditsImported != 1 {
		t.Fatalf("Import result = %+v, want sessions=2 audits=1", result)
	}
	gotChild := importRepo.sessions["session-child"]
	if gotChild == nil {
		t.Fatalf("child session not imported")
	}
	if got := gotChild.ParentSessionID().String(); got != "session-parent" {
		t.Fatalf("child parent_session_id = %q, want session-parent", got)
	}
	if importRepo.commandAudits["e-1"] == nil {
		t.Fatalf("command audit not imported")
	}
}

func TestBundleUsecase_ExportFiltersSessionsAndKeepsEventReferencedSessions(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	fooWorkspace := types.Workspace("foo")
	barWorkspace := types.Workspace("bar")
	fooSession := mustSessionInWorkspace(t, "session-foo", "", ts, fooWorkspace)
	fooUnreferencedSession := mustSessionInWorkspace(t, "session-foo-unreferenced", "", ts.Add(time.Minute), fooWorkspace)
	barReferencedSession := mustSessionInWorkspace(t, "session-bar-referenced", "", ts.Add(2*time.Minute), barWorkspace)
	barUnrelatedSession := mustSessionInWorkspace(t, "session-bar-unrelated", "", ts.Add(3*time.Minute), barWorkspace)
	oldFooSession := mustSessionInWorkspace(t, "session-foo-old", "", ts.Add(-2*time.Hour), fooWorkspace)

	events := []*model.Event{
		mustEventInSessionWorkspace(t, "e-foo", ts.Add(10*time.Minute), "session-foo", fooWorkspace),
		mustEventInSessionWorkspace(t, "e-bar-reference", ts.Add(11*time.Minute), "session-bar-referenced", fooWorkspace),
	}
	exportRepo := &fakeBundleRepo{
		schema: 13,
		exportSessions: []*model.Session{
			fooSession,
			fooUnreferencedSession,
			barReferencedSession,
			barUnrelatedSession,
			oldFooSession,
		},
	}
	exportUC := usecase.NewBundleUsecase(fakeEventQuery{events: events}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
		Workspace:  fooWorkspace,
		Since:      ts.Add(-time.Hour),
		Until:      ts.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	files := openTestBundle(t, out, []byte("pass1"))
	got := decodeBundleSessionIDs(t, files["sessions.ndjson"])
	want := []string{"session-bar-referenced", "session-foo", "session-foo-unreferenced"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("exported session IDs = %v, want %v", got, want)
	}
}

func TestBundleUsecase_MissingSessionParentRejectsOrBackfills(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	child := mustSession(t, "session-child", "session-parent", ts)
	exportRepo := &fakeBundleRepo{schema: 13, exportSessions: []*model.Session{child}}
	exportUC := usecase.NewBundleUsecase(fakeEventQuery{}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	rejectRepo := &fakeBundleRepo{schema: 13}
	rejectUC := usecase.NewBundleUsecase(fakeEventQuery{}, rejectRepo, nil)
	_, err := rejectUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
	})
	if err == nil || !strings.Contains(err.Error(), "parent session not found") {
		t.Fatalf("Import reject error = %v, want parent session not found", err)
	}

	backfillRepo := &fakeBundleRepo{schema: 13}
	backfillUC := usecase.NewBundleUsecase(fakeEventQuery{}, backfillRepo, nil)
	result, err := backfillUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:        out,
		Passphrase:    []byte("pass1"),
		MissingParent: usecase.BundleMissingParentBackfill,
	})
	if err != nil {
		t.Fatalf("Import backfill: %v", err)
	}
	if result.SessionsImported != 1 {
		t.Fatalf("Import result = %+v, want 1 child session imported", result)
	}
	stub := backfillRepo.sessions["session-parent"]
	if stub == nil {
		t.Fatalf("backfilled parent not found")
	}
	if got := stub.Label(); got != "traceary:bundle-backfilled-parent" {
		t.Fatalf("backfilled label = %q, want marker label", got)
	}
}

func TestBundleUsecase_RoundTripMemoriesDowngradesStatusToCandidate(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	exportRepo := &fakeBundleRepo{
		schema: 13,
		exportMemories: []apptypes.MemoryDetails{
			mustMemoryDetails(t, "mem-accepted", types.MemoryStatusAccepted, ts),
		},
	}
	exportUC := usecase.NewBundleUsecase(fakeEventQuery{}, exportRepo, func() time.Time { return ts })

	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	files := openTestBundle(t, out, []byte("pass1"))
	if _, ok := files["memories.ndjson"]; !ok {
		t.Fatalf("bundle missing memories.ndjson")
	}

	importRepo := &fakeBundleRepo{schema: 13}
	importUC := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, func() time.Time { return ts })
	result, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.MemoriesImported != 1 || result.MemoriesSkipped != 0 {
		t.Fatalf("Import result = %+v, want 1 memory imported / 0 skipped", result)
	}
	got := importRepo.memories["mem-accepted"]
	if got == nil {
		t.Fatalf("imported memory not found")
	}
	if got.Status() != types.MemoryStatusCandidate {
		t.Fatalf("imported status = %s, want candidate", got.Status())
	}
	if len(got.EvidenceRefs()) != 1 || len(got.ArtifactRefs()) != 1 {
		t.Fatalf("refs not restored: evidence=%d artifacts=%d", len(got.EvidenceRefs()), len(got.ArtifactRefs()))
	}
}

func TestBundleUsecase_ReimportDoesNotDowngradeAlreadyAcceptedMemory(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	exportRepo := &fakeBundleRepo{
		schema: 13,
		exportMemories: []apptypes.MemoryDetails{
			mustMemoryDetails(t, "mem-existing", types.MemoryStatusAccepted, ts),
		},
	}
	exportUC := usecase.NewBundleUsecase(fakeEventQuery{}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := exportUC.Export(context.Background(), usecase.BundleExportOptions{
		OutPath:    out,
		Passphrase: []byte("pass1"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	existing := mustMemoryDetails(t, "mem-existing", types.MemoryStatusAccepted, ts).Summary()
	accepted := model.MemoryOf(
		existing.MemoryID(), existing.MemoryType(), existing.Scope(), existing.Fact(), types.MemoryStatusAccepted,
		existing.Confidence(), existing.Source(), nil, nil, existing.Supersedes(), existing.ExpiresAt(),
		existing.ValidFrom(), existing.ValidTo(), existing.CreatedAt(), existing.UpdatedAt(),
	)
	importRepo := &fakeBundleRepo{schema: 13, memories: map[string]*model.Memory{"mem-existing": accepted}}
	importUC := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, func() time.Time { return ts })
	result, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.MemoriesImported != 0 || result.MemoriesSkipped != 1 {
		t.Fatalf("Import result = %+v, want 0 memory imported / 1 skipped", result)
	}
	if got := importRepo.memories["mem-existing"].Status(); got != types.MemoryStatusAccepted {
		t.Fatalf("existing memory status = %s, want accepted", got)
	}
}

func TestBundleUsecase_ImportTopologicallySortsMemorySupersessionChain(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	memoriesBuf := &bytes.Buffer{}
	encoder := json.NewEncoder(memoriesBuf)
	// Intentionally emit rows in non-topological order. Lexicographic sorting
	// by supersedes_memory_id would put mem-m before mem-a here, even though
	// mem-m depends on mem-a and mem-a depends on mem-z.
	for _, row := range []map[string]string{
		bundleMemoryRowForTest("mem-m", "mem-a", ts),
		bundleMemoryRowForTest("mem-a", "mem-z", ts),
		bundleMemoryRowForTest("mem-z", "", ts),
	} {
		if err := encoder.Encode(row); err != nil {
			t.Fatalf("encode memory row: %v", err)
		}
	}
	events := []byte("")
	bundle := buildBundleWithManifestAndFiles(t, 2, nil, map[string][]byte{
		"events.ndjson":   events,
		"memories.ndjson": memoriesBuf.Bytes(),
	}, map[string]any{
		"events": map[string]any{
			"table_name": "events",
			"file":       "events.ndjson",
			"row_count":  0,
			"checksum":   hashForTest(events),
		},
		"memories": map[string]any{
			"table_name": "memories",
			"file":       "memories.ndjson",
			"row_count":  3,
			"checksum":   hashForTest(memoriesBuf.Bytes()),
		},
	})
	in := filepath.Join(t.TempDir(), "unordered-memory-chain.tbun")
	if err := os.WriteFile(in, bundle, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	importRepo := &fakeBundleRepo{schema: 13, enforceMemorySupersedesFK: true}
	uc := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, nil)
	result, err := uc.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     in,
		Passphrase: []byte("testpass"),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.MemoriesImported != 3 || result.MemoriesSkipped != 0 {
		t.Fatalf("Import result = %+v, want 3 memory imported / 0 skipped", result)
	}
	if len(importRepo.memories) != 3 {
		t.Fatalf("imported memories = %d, want 3", len(importRepo.memories))
	}
}

func TestBundleUsecase_ExportWritesManifestV2Tables(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	events := []*model.Event{mustEvent(t, "e-1", ts)}
	memories := []apptypes.MemoryDetails{mustMemoryDetails(t, "mem-a", types.MemoryStatusAccepted, ts)}
	edges := []*model.MemoryEdge{mustMemoryEdge(t, "edge-a", "mem-a", "mem-b", ts)}
	uc := usecase.NewBundleUsecase(fakeEventQuery{events: events}, &fakeBundleRepo{schema: 13, exportMemories: memories, exportMemoryEdges: edges}, func() time.Time { return ts })

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
	for table, file := range map[string]string{"memories": "memories.ndjson", "memory_edges": "memory_edges.ndjson"} {
		entry := manifest.Tables[table]
		if entry.TableName != table || entry.File != file || entry.RowCount != 1 {
			t.Fatalf("%s table entry = %+v", table, entry)
		}
		if got := hashForTest(files[file]); got != entry.Checksum {
			t.Fatalf("%s checksum = %s, want %s", table, entry.Checksum, got)
		}
	}
}

func TestBundleUsecase_OrphanMemoryEdgeDefaultSkipsWithStructuredWarning(t *testing.T) {
	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	exportRepo := &fakeBundleRepo{
		schema: 13,
		exportMemories: []apptypes.MemoryDetails{
			mustMemoryDetails(t, "mem-a", types.MemoryStatusAccepted, ts),
		},
		exportMemoryEdges: []*model.MemoryEdge{
			mustMemoryEdge(t, "edge-orphan", "mem-a", "mem-missing", ts),
		},
	}
	uc := usecase.NewBundleUsecase(fakeEventQuery{}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := uc.Export(context.Background(), usecase.BundleExportOptions{OutPath: out, Passphrase: []byte("pass1")}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	files := openTestBundle(t, out, []byte("pass1"))
	if _, ok := files["memory_edges.ndjson"]; !ok {
		t.Fatalf("bundle missing memory_edges.ndjson")
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previous)

	importRepo := &fakeBundleRepo{schema: 13}
	importUC := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, nil)
	result, err := importUC.Import(context.Background(), usecase.BundleImportOptions{InPath: out, Passphrase: []byte("pass1")})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.MemoriesImported != 1 || result.MemoryEdgesImported != 0 || result.MemoryEdgesSkipped != 1 {
		t.Fatalf("Import result = %+v, want memory imported and orphan edge skipped", result)
	}
	logText := logs.String()
	for _, want := range []string{"bundle import skipped orphan memory edge", "\"table\":\"memory_edges\"", "\"edge_id\":\"edge-orphan\"", "\"to_exists\":false"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("structured warning = %q, want containing %q", logText, want)
		}
	}
}

func TestBundleUsecase_OrphanMemoryEdgeRejectRollsBackTransaction(t *testing.T) {
	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	exportRepo := &fakeBundleRepo{
		schema: 13,
		exportMemories: []apptypes.MemoryDetails{
			mustMemoryDetails(t, "mem-a", types.MemoryStatusAccepted, ts),
		},
		exportMemoryEdges: []*model.MemoryEdge{
			mustMemoryEdge(t, "edge-orphan", "mem-a", "mem-missing", ts),
		},
	}
	uc := usecase.NewBundleUsecase(fakeEventQuery{}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := uc.Export(context.Background(), usecase.BundleExportOptions{OutPath: out, Passphrase: []byte("pass1")}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	importRepo := &fakeBundleRepo{schema: 13}
	importUC := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, nil)
	_, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:      out,
		Passphrase:  []byte("pass1"),
		OrphanEdges: usecase.BundleOrphanEdgesReject,
	})
	if err == nil {
		t.Fatalf("Import unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "references missing endpoint") {
		t.Fatalf("Import error = %q, want missing endpoint", err.Error())
	}
	if len(importRepo.memories) != 0 || len(importRepo.memoryEdges) != 0 {
		t.Fatalf("rollback failed: memories=%d edges=%d", len(importRepo.memories), len(importRepo.memoryEdges))
	}
}

func TestBundleUsecase_OrphanMemoryEdgeConflictErrorRollsBackBeforeSkip(t *testing.T) {
	ts := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	exportRepo := &fakeBundleRepo{
		schema: 13,
		exportMemories: []apptypes.MemoryDetails{
			mustMemoryDetails(t, "mem-new", types.MemoryStatusAccepted, ts),
		},
		exportMemoryEdges: []*model.MemoryEdge{
			mustMemoryEdge(t, "edge-collide", "mem-missing-from", "mem-missing-to", ts),
		},
	}
	uc := usecase.NewBundleUsecase(fakeEventQuery{}, exportRepo, func() time.Time { return ts })
	out := filepath.Join(t.TempDir(), "bundle.tbun")
	if err := uc.Export(context.Background(), usecase.BundleExportOptions{OutPath: out, Passphrase: []byte("pass1")}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	importRepo := &fakeBundleRepo{
		schema: 13,
		memoryEdges: map[string]*model.MemoryEdge{
			"edge-collide": mustMemoryEdge(t, "edge-collide", "mem-existing-from", "mem-existing-to", ts),
		},
	}
	importUC := usecase.NewBundleUsecase(fakeEventQuery{}, importRepo, nil)
	_, err := importUC.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     out,
		Passphrase: []byte("pass1"),
		OnConflict: usecase.BundleConflictError,
	})
	if err == nil {
		t.Fatalf("Import unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "memory edge conflict") {
		t.Fatalf("Import error = %q, want memory edge conflict", err.Error())
	}
	if len(importRepo.memories) != 0 {
		t.Fatalf("rollback failed: imported memories=%d, want 0", len(importRepo.memories))
	}
}

func TestBundleUsecase_ManifestV2FiveTableSpecDocReachable(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("..", "..", "docs", "operations", "cross-machine-handoff.md"))
	if err != nil {
		t.Fatalf("ReadFile(cross-machine-handoff.md): %v", err)
	}
	text := string(content)
	for _, want := range []string{"manifest_version = 2", "events.ndjson", "sessions.ndjson", "command_audits.ndjson", "memories.ndjson", "memory_edges.ndjson", "Conflict matrix", "Five-table inclusion rules"} {
		if !strings.Contains(text, want) {
			t.Fatalf("doc missing %q", want)
		}
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

func TestBundleUsecase_ImportsV1ManifestWithExtraChecksummedFile(t *testing.T) {
	t.Parallel()

	row := mustEvent(t, "e-v1-extra", time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC))
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
	extra := []byte("legacy sidecar data\n")
	bundle := buildBundleWithManifestAndFiles(t, 1, map[string]string{
		"events.ndjson": hashForTest(eventsBuf.Bytes()),
		"extra.ndjson":  hashForTest(extra),
	}, map[string][]byte{
		"events.ndjson": eventsBuf.Bytes(),
		"extra.ndjson":  extra,
	}, nil)
	in := filepath.Join(t.TempDir(), "v1-extra.tbun")
	if err := os.WriteFile(in, bundle, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	uc := usecase.NewBundleUsecase(fakeEventQuery{}, &fakeBundleRepo{schema: 13}, nil)
	result, err := uc.Import(context.Background(), usecase.BundleImportOptions{
		InPath:     in,
		Passphrase: []byte("testpass"),
	})
	if err != nil {
		t.Fatalf("Import v1 with extra file: %v", err)
	}
	if result.EventsImported != 1 || result.EventsSkipped != 0 {
		t.Fatalf("Import v1 result = %+v, want 1 imported / 0 skipped", result)
	}
}

func TestBundleUsecase_RejectsV1ManifestWithInvalidExtraChecksum(t *testing.T) {
	t.Parallel()

	events := []byte("")
	extra := []byte("legacy sidecar data\n")
	tests := []struct {
		name   string
		files  map[string][]byte
		wantIn string
	}{
		{
			name: "missing extra file",
			files: map[string][]byte{
				"events.ndjson": events,
			},
			wantIn: "bundle missing extra.ndjson referenced by manifest",
		},
		{
			name: "mismatched extra checksum",
			files: map[string][]byte{
				"events.ndjson": events,
				"extra.ndjson":  []byte("tampered\n"),
			},
			wantIn: "checksum mismatch on extra.ndjson",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bundle := buildBundleWithManifestAndFiles(t, 1, map[string]string{
				"events.ndjson": hashForTest(events),
				"extra.ndjson":  hashForTest(extra),
			}, tt.files, nil)
			in := filepath.Join(t.TempDir(), "v1-invalid-extra.tbun")
			if err := os.WriteFile(in, bundle, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			uc := usecase.NewBundleUsecase(fakeEventQuery{}, &fakeBundleRepo{schema: 13}, nil)
			_, err := uc.Import(context.Background(), usecase.BundleImportOptions{
				InPath:     in,
				Passphrase: []byte("testpass"),
			})
			if err == nil {
				t.Fatalf("Import unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Fatalf("Import error = %q, want containing %q", err.Error(), tt.wantIn)
			}
		})
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
	return buildBundleWithManifestAndFiles(t, manifestVersion, checksums, files, tables)
}

func buildBundleWithManifestAndFiles(
	t *testing.T,
	manifestVersion int,
	checksums map[string]string,
	files map[string][]byte,
	tables map[string]any,
) []byte {
	t.Helper()
	files = cloneBundleFilesForTest(files)
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
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
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

func cloneBundleFilesForTest(files map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(files))
	for name, data := range files {
		clone[name] = bytes.Clone(data)
	}
	return clone
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

func decodeBundleSessionIDs(t *testing.T, data []byte) []string {
	t.Helper()
	ids := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("unmarshal session row: %v", err)
		}
		ids = append(ids, row.SessionID)
	}
	sort.Strings(ids)
	return ids
}

func hashForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func bundleMemoryRowForTest(id string, supersedesID string, ts time.Time) map[string]string {
	row := map[string]string{
		"memory_id":   id,
		"type":        "decision",
		"scope_kind":  "workspace",
		"scope_value": "ws",
		"fact":        "Bundle memory " + id,
		"status":      "accepted",
		"confidence":  "high",
		"source":      "manual",
		"valid_from":  ts.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"created_at":  ts.UTC().Format(time.RFC3339Nano),
		"updated_at":  ts.Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
	if supersedesID != "" {
		row["supersedes_memory_id"] = supersedesID
	}
	return row
}
