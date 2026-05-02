package sqlite_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func newMemoryDatasource(
	t *testing.T,
	dbPath string,
	migrations fs.FS,
) (*infra.MemoryDatasource, *infra.StoreManagementDatasource) {
	t.Helper()
	return newMemoryDatasourceWithClock(t, dbPath, migrations, types.SystemClock{})
}

func newMemoryDatasourceWithClock(
	t *testing.T,
	dbPath string,
	migrations fs.FS,
	clock types.Clock,
) (*infra.MemoryDatasource, *infra.StoreManagementDatasource) {
	t.Helper()
	db := infra.NewDatabase(dbPath, migrations)
	return infra.NewMemoryDatasourceWithClock(db, clock), infra.NewStoreManagementDatasource(db)
}

func memoryDatasourceTestMigrations() fstest.MapFS {
	return fstest.MapFS{
		"000008_create_memories.sql": {
			Data: []byte(`CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    scope_kind TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    fact TEXT NOT NULL,
    status TEXT NOT NULL,
    confidence TEXT NOT NULL,
    source TEXT NOT NULL,
    supersedes_memory_id TEXT REFERENCES memories(id),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_memories_scope_status_updated
    ON memories(scope_kind, scope_value, status, updated_at DESC, id DESC);
CREATE INDEX idx_memories_type_status_updated
    ON memories(type, status, updated_at DESC, id DESC);
CREATE INDEX idx_memories_supersedes_memory_id
    ON memories(supersedes_memory_id);
CREATE TABLE memory_evidence_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);
CREATE INDEX idx_memory_evidence_refs_lookup
    ON memory_evidence_refs(ref_kind, ref_value);
CREATE TABLE memory_artifact_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);
CREATE INDEX idx_memory_artifact_refs_lookup
    ON memory_artifact_refs(ref_kind, ref_value);`),
		},
		"000009_add_memory_validity_window.sql": {
			Data: []byte(`ALTER TABLE memories ADD COLUMN valid_from TEXT;
ALTER TABLE memories ADD COLUMN valid_to TEXT;
UPDATE memories SET valid_from = created_at WHERE valid_from IS NULL;
CREATE INDEX idx_memories_valid_window ON memories(valid_to, valid_from);`),
		},
		"000010_normalize_memory_validity_precision.sql": {
			Data: []byte(`UPDATE memories
SET valid_from =
  CASE
    WHEN instr(valid_from, '.') = 0
      THEN substr(valid_from, 1, length(valid_from) - 1) || '.000000000Z'
    ELSE
      substr(valid_from, 1, instr(valid_from, '.')) ||
      substr(substr(valid_from, instr(valid_from, '.') + 1, length(valid_from) - instr(valid_from, '.') - 1) || '000000000', 1, 9) ||
      'Z'
  END
WHERE valid_from IS NOT NULL;
UPDATE memories
SET valid_to =
  CASE
    WHEN instr(valid_to, '.') = 0
      THEN substr(valid_to, 1, length(valid_to) - 1) || '.000000000Z'
    ELSE
      substr(valid_to, 1, instr(valid_to, '.')) ||
      substr(substr(valid_to, instr(valid_to, '.') + 1, length(valid_to) - instr(valid_to, '.') - 1) || '000000000', 1, 9) ||
      'Z'
  END
WHERE valid_to IS NOT NULL;`),
		},
	}
}

func mustMemoryID(t *testing.T, value string) types.MemoryID {
	t.Helper()
	memoryID, err := types.MemoryIDFrom(value)
	if err != nil {
		t.Fatalf("MemoryIDFrom() error = %v", err)
	}
	return memoryID
}

func mustWorkspaceScope(t *testing.T, value string) types.MemoryScope {
	t.Helper()
	workspace, err := types.WorkspaceFrom(value)
	if err != nil {
		t.Fatalf("WorkspaceFrom() error = %v", err)
	}
	return types.WorkspaceScopeOf(workspace)
}

func mustAgentScope(t *testing.T, value string) types.MemoryScope {
	t.Helper()
	agent, err := types.AgentFrom(value)
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	return types.AgentScopeOf(agent)
}

func mustSessionFamilyScope(t *testing.T, value string) types.MemoryScope {
	t.Helper()
	sessionID, err := types.SessionIDFrom(value)
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	return types.SessionFamilyScopeOf(sessionID)
}

func mustEvidenceRef(t *testing.T, kind types.EvidenceRefKind, value string) types.EvidenceRef {
	t.Helper()
	ref, err := types.EvidenceRefFrom(kind, value)
	if err != nil {
		t.Fatalf("EvidenceRefFrom() error = %v", err)
	}
	return ref
}

func mustArtifactRef(t *testing.T, kind types.ArtifactRefKind, value string) types.ArtifactRef {
	t.Helper()
	ref, err := types.ArtifactRefFrom(kind, value)
	if err != nil {
		t.Fatalf("ArtifactRefFrom() error = %v", err)
	}
	return ref
}

func mustMemoryIDFromOptional(t *testing.T, value types.Optional[types.MemoryID]) types.MemoryID {
	t.Helper()
	memoryID, ok := value.Value()
	if !ok {
		t.Fatal("Optional.Value() ok = false, want true")
	}
	return memoryID
}

func memoryOf(
	t *testing.T,
	memoryID string,
	memoryType types.MemoryType,
	scope types.MemoryScope,
	fact string,
	status types.MemoryStatus,
	confidence types.Confidence,
	source types.MemorySource,
	evidenceRefs []types.EvidenceRef,
	artifactRefs []types.ArtifactRef,
	supersedes types.Optional[types.MemoryID],
	expiresAt types.Optional[time.Time],
	createdAt time.Time,
	updatedAt time.Time,
) *model.Memory {
	t.Helper()
	return model.MemoryOf(
		mustMemoryID(t, memoryID),
		memoryType,
		scope,
		fact,
		status,
		confidence,
		source,
		evidenceRefs,
		artifactRefs,
		supersedes,
		expiresAt,
		createdAt,
		types.None[time.Time](),
		createdAt,
		updatedAt,
	)
}

func memoryScopeToken(scope types.MemoryScope) string {
	if scope == nil {
		return "<nil>"
	}
	return scope.Kind().String() + ":" + scope.Key()
}

func evidenceRefTokens(refs []types.EvidenceRef) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref.Kind().String()+":"+ref.Value())
	}
	return result
}

func artifactRefTokens(refs []types.ArtifactRef) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref.Kind().String()+":"+ref.Value())
	}
	return result
}

func TestMemoryDatasource_SaveAndFindByID(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	ctx := context.Background()

	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	baseMemory := memoryOf(
		t,
		"mem-base",
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/duck8823/traceary"),
		"Release issues close only after tagged release",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindIssue, "454")},
		nil,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 10, 15, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, baseMemory); err != nil {
		t.Fatalf("Save(baseMemory) error = %v", err)
	}

	expiresAt := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	memory := memoryOf(
		t,
		"mem-1",
		types.MemoryTypeLesson,
		mustAgentScope(t, "codex"),
		"Use issue-specific branches for memory work",
		types.MemoryStatusExpired,
		types.ConfidenceHigh,
		types.MemorySourceExtracted,
		[]types.EvidenceRef{
			mustEvidenceRef(t, types.EvidenceRefKindIssue, "460"),
			mustEvidenceRef(t, types.EvidenceRefKindPR, "466"),
		},
		[]types.ArtifactRef{
			mustArtifactRef(t, types.ArtifactRefKindFile, "domain/model/memory.go"),
		},
		types.Some(baseMemory.MemoryID()),
		types.Some(expiresAt),
		time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 11, 30, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, memory); err != nil {
		t.Fatalf("Save(memory) error = %v", err)
	}

	gotOpt, err := sut.FindByID(ctx, memory.MemoryID())
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	got, ok := gotOpt.Value()
	if !ok {
		t.Fatalf("FindByID().Value() ok = false, want true")
	}

	if diff := cmp.Diff(memory.MemoryID(), got.MemoryID()); diff != "" {
		t.Fatalf("MemoryID mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(memory.Status(), got.Status()); diff != "" {
		t.Fatalf("Status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(memoryScopeToken(memory.Scope()), memoryScopeToken(got.Scope())); diff != "" {
		t.Fatalf("Scope mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(evidenceRefTokens(memory.EvidenceRefs()), evidenceRefTokens(got.EvidenceRefs())); diff != "" {
		t.Fatalf("EvidenceRefs mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(artifactRefTokens(memory.ArtifactRefs()), artifactRefTokens(got.ArtifactRefs())); diff != "" {
		t.Fatalf("ArtifactRefs mismatch (-want +got):\n%s", diff)
	}
	if gotSupersedes, ok := got.Supersedes().Value(); !ok || gotSupersedes != baseMemory.MemoryID() {
		t.Fatalf("Supersedes() = (%v, %v), want (%v, true)", gotSupersedes, ok, baseMemory.MemoryID())
	}
	if gotExpiresAt, ok := got.ExpiresAt().Value(); !ok || !gotExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt() = (%v, %v), want (%v, true)", gotExpiresAt, ok, expiresAt)
	}
}

func TestMemoryDatasource_SaveSupersession(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	ctx := context.Background()

	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	original := memoryOf(
		t,
		"mem-original",
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/duck8823/traceary"),
		"Original release memory",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindIssue, "#454")},
		nil,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 9, 30, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, original); err != nil {
		t.Fatalf("Save(original) error = %v", err)
	}
	if err := original.MarkSuperseded(); err != nil {
		t.Fatalf("MarkSuperseded() error = %v", err)
	}

	replacement := memoryOf(
		t,
		"mem-replacement",
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/duck8823/traceary"),
		"Replacement release memory",
		types.MemoryStatusAccepted,
		types.ConfidenceHigh,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindPR, "#468")},
		[]types.ArtifactRef{mustArtifactRef(t, types.ArtifactRefKindFile, "presentation/cli/memory.go")},
		types.Some(original.MemoryID()),
		types.None[time.Time](),
		time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 10, 15, 0, 0, time.UTC),
	)
	if err := sut.SaveSupersession(ctx, original, replacement); err != nil {
		t.Fatalf("SaveSupersession() error = %v", err)
	}

	gotOriginalOpt, err := sut.FindByID(ctx, original.MemoryID())
	if err != nil {
		t.Fatalf("FindByID(original) error = %v", err)
	}
	gotOriginal, ok := gotOriginalOpt.Value()
	if !ok {
		t.Fatal("FindByID(original) returned empty result")
	}
	if diff := cmp.Diff(types.MemoryStatusSuperseded, gotOriginal.Status()); diff != "" {
		t.Fatalf("original status mismatch (-want +got):\n%s", diff)
	}

	gotReplacementOpt, err := sut.FindByID(ctx, replacement.MemoryID())
	if err != nil {
		t.Fatalf("FindByID(replacement) error = %v", err)
	}
	gotReplacement, ok := gotReplacementOpt.Value()
	if !ok {
		t.Fatal("FindByID(replacement) returned empty result")
	}
	if diff := cmp.Diff(types.MemoryStatusAccepted, gotReplacement.Status()); diff != "" {
		t.Fatalf("replacement status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(original.MemoryID(), mustMemoryIDFromOptional(t, gotReplacement.Supersedes())); diff != "" {
		t.Fatalf("replacement supersedes mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryDatasource_GetDetails(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	ctx := context.Background()

	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	memory := memoryOf(
		t,
		"mem-details",
		types.MemoryTypeArtifact,
		mustWorkspaceScope(t, "github.com/duck8823/traceary"),
		"Release metadata PR prepares docs only",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindIssue, "218")},
		[]types.ArtifactRef{mustArtifactRef(t, types.ArtifactRefKindPR, "227")},
		types.None[types.MemoryID](),
		types.None[time.Time](),
		time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 12, 9, 10, 0, 0, time.UTC),
	)
	if err := sut.Save(ctx, memory); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	details, err := sut.GetDetails(ctx, memory.MemoryID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if diff := cmp.Diff(memory.MemoryID(), details.Summary().MemoryID()); diff != "" {
		t.Fatalf("MemoryID mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(evidenceRefTokens(memory.EvidenceRefs()), evidenceRefTokens(details.EvidenceRefs())); diff != "" {
		t.Fatalf("EvidenceRefs mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(artifactRefTokens(memory.ArtifactRefs()), artifactRefTokens(details.ArtifactRefs())); diff != "" {
		t.Fatalf("ArtifactRefs mismatch (-want +got):\n%s", diff)
	}

	_, err = sut.GetDetails(ctx, mustMemoryID(t, "missing"))
	if err == nil {
		t.Fatalf("GetDetails(missing) error = nil, want error")
	}
}

func TestMemoryDatasource_List(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	ctx := context.Background()

	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	fixtureMemories := []*model.Memory{
		memoryOf(t, "mem-accepted", types.MemoryTypeDecision, mustWorkspaceScope(t, "github.com/duck8823/traceary"), "Accepted memory", types.MemoryStatusAccepted, types.ConfidenceHigh, types.MemorySourceManual, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 8, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 8, 30, 0, 0, time.UTC)),
		memoryOf(t, "mem-candidate", types.MemoryTypeLesson, mustAgentScope(t, "codex"), "Candidate memory", types.MemoryStatusCandidate, types.ConfidenceLow, types.MemorySourceExtracted, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 9, 30, 0, 0, time.UTC)),
		memoryOf(t, "mem-rejected", types.MemoryTypeConstraint, mustWorkspaceScope(t, "github.com/duck8823/traceary"), "Rejected memory", types.MemoryStatusRejected, types.ConfidenceMedium, types.MemorySourceManual, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 10, 30, 0, 0, time.UTC)),
		memoryOf(t, "mem-superseded", types.MemoryTypeDecision, mustWorkspaceScope(t, "github.com/duck8823/traceary"), "Superseded memory", types.MemoryStatusSuperseded, types.ConfidenceHigh, types.MemorySourceManual, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 11, 30, 0, 0, time.UTC)),
		memoryOf(t, "mem-expired", types.MemoryTypeArtifact, mustSessionFamilyScope(t, "session-root"), "Expired memory", types.MemoryStatusExpired, types.ConfidenceHigh, types.MemorySourceImported, nil, nil, types.None[types.MemoryID](), types.Some(time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)), time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 12, 30, 0, 0, time.UTC)),
	}
	for _, memory := range fixtureMemories {
		if err := sut.Save(ctx, memory); err != nil {
			t.Fatalf("Save(%s) error = %v", memory.MemoryID(), err)
		}
	}

	t.Run("default active list excludes terminal statuses", func(t *testing.T) {
		t.Parallel()

		criteria := apptypes.NewMemoryListCriteriaBuilder(10).Build()
		summaries, err := sut.List(ctx, criteria)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if got := len(summaries); got != 2 {
			t.Fatalf("len(List()) = %d, want 2", got)
		}
		if diff := cmp.Diff([]string{"mem-candidate", "mem-accepted"}, []string{summaries[0].MemoryID().String(), summaries[1].MemoryID().String()}); diff != "" {
			t.Fatalf("IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("explicit rejected status is queryable", func(t *testing.T) {
		t.Parallel()

		criteria := apptypes.NewMemoryListCriteriaBuilder(10).
			Status(types.MemoryStatusRejected).
			Build()
		summaries, err := sut.List(ctx, criteria)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if got := len(summaries); got != 1 {
			t.Fatalf("len(List()) = %d, want 1", got)
		}
		if diff := cmp.Diff("mem-rejected", summaries[0].MemoryID().String()); diff != "" {
			t.Fatalf("MemoryID mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("typed session-family scope is queryable", func(t *testing.T) {
		t.Parallel()

		criteria := apptypes.NewMemoryListCriteriaBuilder(10).
			Scope(mustSessionFamilyScope(t, "session-root")).
			Statuses([]types.MemoryStatus{types.MemoryStatusExpired}).
			Build()
		summaries, err := sut.List(ctx, criteria)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if got := len(summaries); got != 1 {
			t.Fatalf("len(List()) = %d, want 1", got)
		}
		if diff := cmp.Diff("mem-expired", summaries[0].MemoryID().String()); diff != "" {
			t.Fatalf("MemoryID mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("source filter narrows the row set at the datasource layer", func(t *testing.T) {
		t.Parallel()

		// Pin the dedicated source column narrowing added for the inbox view:
		// the datasource must honour Sources() without relying on client-side
		// post-filtering so pagination stays consistent.
		criteria := apptypes.NewMemoryListCriteriaBuilder(10).
			Statuses([]types.MemoryStatus{types.MemoryStatusCandidate, types.MemoryStatusAccepted}).
			Source(types.MemorySourceExtracted).
			Build()
		summaries, err := sut.List(ctx, criteria)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if got := len(summaries); got != 1 {
			t.Fatalf("len(List()) = %d, want 1 (mem-candidate)", got)
		}
		if diff := cmp.Diff("mem-candidate", summaries[0].MemoryID().String()); diff != "" {
			t.Fatalf("MemoryID mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestMemoryDatasource_List_RememberIntentPriorityAppliesBeforePagination(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	ctx := context.Background()

	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	// Three candidates whose updated_at puts the remember-intent row in the
	// middle of the timeline. Without query-level priority, a LIMIT 1 page
	// would surface the most recent extracted candidate and leave the
	// remember-intent row hidden until later pages.
	scope := mustWorkspaceScope(t, "github.com/duck8823/traceary")
	fixtureMemories := []*model.Memory{
		memoryOf(t, "mem-extracted-newest", types.MemoryTypeLesson, scope, "newest extracted", types.MemoryStatusCandidate, types.ConfidenceLow, types.MemorySourceExtracted, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)),
		memoryOf(t, "mem-remember-intent-mid", types.MemoryTypePreference, scope, "remember-intent middle", types.MemoryStatusCandidate, types.ConfidenceLow, types.MemorySourceRememberIntent, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC)),
		memoryOf(t, "mem-extracted-oldest", types.MemoryTypeLesson, scope, "oldest extracted", types.MemoryStatusCandidate, types.ConfidenceLow, types.MemorySourceExtracted, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC), time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)),
	}
	for _, memory := range fixtureMemories {
		if err := sut.Save(ctx, memory); err != nil {
			t.Fatalf("Save(%s) error = %v", memory.MemoryID(), err)
		}
	}

	criteria := apptypes.NewMemoryListCriteriaBuilder(1).
		Statuses([]types.MemoryStatus{types.MemoryStatusCandidate}).
		RememberIntentPriority(true).
		Build()
	summaries, err := sut.List(ctx, criteria)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := len(summaries); got != 1 {
		t.Fatalf("len(List()) = %d, want 1 (LIMIT 1)", got)
	}
	if diff := cmp.Diff("mem-remember-intent-mid", summaries[0].MemoryID().String()); diff != "" {
		t.Fatalf("first page must surface remember-intent row before extracted rows; mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryDatasource_Search(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	ctx := context.Background()

	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	memories := []*model.Memory{
		memoryOf(
			t,
			"mem-fact",
			types.MemoryTypeDecision,
			mustWorkspaceScope(t, "github.com/duck8823/traceary"),
			"Release issues close only after tagged release",
			types.MemoryStatusAccepted,
			types.ConfidenceVerified,
			types.MemorySourceManual,
			[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindIssue, "454")},
			nil,
			types.None[types.MemoryID](),
			types.None[time.Time](),
			time.Date(2026, 4, 12, 7, 0, 0, 0, time.UTC),
			time.Date(2026, 4, 12, 7, 10, 0, 0, time.UTC),
		),
		memoryOf(
			t,
			"mem-artifact",
			types.MemoryTypeArtifact,
			mustWorkspaceScope(t, "github.com/duck8823/traceary"),
			"Homebrew formula update PR",
			types.MemoryStatusAccepted,
			types.ConfidenceHigh,
			types.MemorySourceManual,
			nil,
			[]types.ArtifactRef{mustArtifactRef(t, types.ArtifactRefKindPR, "458")},
			types.None[types.MemoryID](),
			types.None[time.Time](),
			time.Date(2026, 4, 12, 8, 0, 0, 0, time.UTC),
			time.Date(2026, 4, 12, 8, 10, 0, 0, time.UTC),
		),
		memoryOf(
			t,
			"mem-path",
			types.MemoryTypeLesson,
			mustWorkspaceScope(t, "github.com/duck8823/traceary"),
			`Windows path C:\traceary\memory.db`,
			types.MemoryStatusAccepted,
			types.ConfidenceMedium,
			types.MemorySourceImported,
			nil,
			nil,
			types.None[types.MemoryID](),
			types.None[time.Time](),
			time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 4, 12, 9, 10, 0, 0, time.UTC),
		),
	}
	for _, memory := range memories {
		if err := sut.Save(ctx, memory); err != nil {
			t.Fatalf("Save(%s) error = %v", memory.MemoryID(), err)
		}
	}

	searchByFact := apptypes.NewMemorySearchCriteriaBuilder(10).Query("tagged release").Build()
	factResults, err := sut.Search(ctx, searchByFact)
	if err != nil {
		t.Fatalf("Search(fact) error = %v", err)
	}
	if got := len(factResults); got != 1 {
		t.Fatalf("len(Search(fact)) = %d, want 1", got)
	}
	if diff := cmp.Diff("mem-fact", factResults[0].MemoryID().String()); diff != "" {
		t.Fatalf("fact result mismatch (-want +got):\n%s", diff)
	}

	searchByArtifact := apptypes.NewMemorySearchCriteriaBuilder(10).Query("458").Build()
	artifactResults, err := sut.Search(ctx, searchByArtifact)
	if err != nil {
		t.Fatalf("Search(artifact) error = %v", err)
	}
	if got := len(artifactResults); got != 1 {
		t.Fatalf("len(Search(artifact)) = %d, want 1", got)
	}
	if diff := cmp.Diff("mem-artifact", artifactResults[0].MemoryID().String()); diff != "" {
		t.Fatalf("artifact result mismatch (-want +got):\n%s", diff)
	}

	searchByPath := apptypes.NewMemorySearchCriteriaBuilder(10).Query(`C:\traceary\memory.db`).Build()
	pathResults, err := sut.Search(ctx, searchByPath)
	if err != nil {
		t.Fatalf("Search(path) error = %v", err)
	}
	if got := len(pathResults); got != 1 {
		t.Fatalf("len(Search(path)) = %d, want 1", got)
	}
	if diff := cmp.Diff("mem-path", pathResults[0].MemoryID().String()); diff != "" {
		t.Fatalf("path result mismatch (-want +got):\n%s", diff)
	}
}

// TestMemoryDatasource_ValiditySubSecondBoundaryRespectsPrecision
// regression test for #664: the validity filter must honour
// sub-second resolution rather than truncating to whole seconds via
// SQLite's datetime(). Two memories with valid_to at
// 00:00:00.100Z and 00:00:00.900Z are distinguishable when `as_of`
// falls at 00:00:00.500Z — before the fix, `datetime()` collapsed
// everything inside the same wall second to a single value so both
// rows either returned or didn't depending on the wall-clock
// truncation target.
func TestMemoryDatasource_ValiditySubSecondBoundaryRespectsPrecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "validity.db")
	sut, store := newMemoryDatasource(t, dbPath, memoryDatasourceTestMigrations())
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sameSecond := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	memoryEarlyExpiry := model.MemoryOf(
		mustMemoryID(t, "mem-validity-early"),
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/example/validity"),
		"expires at .100 of the second",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindURL, "https://example/early")},
		nil,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		sameSecond,
		types.Some(sameSecond.Add(100*time.Millisecond)),
		sameSecond,
		sameSecond,
	)
	memoryLateExpiry := model.MemoryOf(
		mustMemoryID(t, "mem-validity-late"),
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/example/validity"),
		"expires at .900 of the second",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRef(t, types.EvidenceRefKindURL, "https://example/late")},
		nil,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		sameSecond,
		types.Some(sameSecond.Add(900*time.Millisecond)),
		sameSecond,
		sameSecond,
	)
	for _, memory := range []*model.Memory{memoryEarlyExpiry, memoryLateExpiry} {
		if err := sut.Save(ctx, memory); err != nil {
			t.Fatalf("Save(%s) error = %v", memory.MemoryID(), err)
		}
	}

	// as_of at .500 should include only the late-expiry memory
	// (its window still covers that instant) and exclude the
	// early one whose window already closed at .100.
	asOfMidSecond := sameSecond.Add(500 * time.Millisecond)
	criteria := apptypes.NewMemoryListCriteriaBuilder(10).
		AsOf(asOfMidSecond).
		Build()
	summaries, err := sut.List(ctx, criteria)
	if err != nil {
		t.Fatalf("List(as_of=%s) error = %v", asOfMidSecond, err)
	}
	if got := len(summaries); got != 1 {
		var ids []string
		for _, s := range summaries {
			ids = append(ids, s.MemoryID().String())
		}
		t.Fatalf("List(as_of=.500) returned %d memories (%v), want 1 (mem-validity-late only)", got, ids)
	}
	if got, want := summaries[0].MemoryID().String(), "mem-validity-late"; got != want {
		t.Fatalf("List(as_of=.500)[0] = %q, want %q", got, want)
	}
}

func TestMemoryDatasource_ListUsesInjectedClockForDefaultAsOf(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "clock.db")
	evaluationTime := time.Date(2026, 4, 10, 0, 0, 0, int(500*time.Millisecond), time.UTC)
	sut, store := newMemoryDatasourceWithClock(t, dbPath, memoryDatasourceTestMigrations(), fakeClock{now: evaluationTime})
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	validFrom := evaluationTime.Add(-time.Hour)
	activeMemory := model.MemoryOf(
		mustMemoryID(t, "mem-clock-active"),
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/example/clock"),
		"active at injected now",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		nil,
		nil,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		validFrom,
		types.Some(evaluationTime.Add(time.Hour)),
		validFrom,
		validFrom,
	)
	expiredMemory := model.MemoryOf(
		mustMemoryID(t, "mem-clock-expired"),
		types.MemoryTypeDecision,
		mustWorkspaceScope(t, "github.com/example/clock"),
		"expired before injected now",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		nil,
		nil,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		validFrom,
		types.Some(evaluationTime.Add(-time.Nanosecond)),
		validFrom,
		validFrom,
	)
	for _, memory := range []*model.Memory{activeMemory, expiredMemory} {
		if err := sut.Save(ctx, memory); err != nil {
			t.Fatalf("Save(%s) error = %v", memory.MemoryID(), err)
		}
	}

	summaries, err := sut.List(ctx, apptypes.NewMemoryListCriteriaBuilder(10).Build())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := len(summaries); got != 1 {
		t.Fatalf("List() returned %d memories, want 1", got)
	}
	if got, want := summaries[0].MemoryID().String(), "mem-clock-active"; got != want {
		t.Fatalf("List()[0] = %q, want %q", got, want)
	}
}
