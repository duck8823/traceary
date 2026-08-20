package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
)

type hookDiagnosticSessionLookupStub struct {
	ended map[types.SessionID]struct{}
	err   error
	calls int
}

func (s *hookDiagnosticSessionLookupStub) FindEndedSessionIDs(_ context.Context, _ []types.SessionID) (map[types.SessionID]struct{}, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.ended, nil
}

func TestClassifyHookCancellationDiagnostics(t *testing.T) {
	const currentDBPath = "/store/current.db"
	endedID := types.SessionID("ended-session")
	activeID := types.SessionID("active-session")
	missingID := types.SessionID("missing-session")
	records := []hookCancellationDiagnostic{
		{SessionID: endedID.String(), DBPath: currentDBPath, Path: "ended.json"},
		{SessionID: activeID.String(), DBPath: currentDBPath, Path: "active.json"},
		{SessionID: missingID.String(), DBPath: currentDBPath, Path: "missing.json"},
	}
	lookup := &hookDiagnosticSessionLookupStub{ended: map[types.SessionID]struct{}{endedID: {}}}

	got, err := classifyHookCancellationDiagnostics(context.Background(), records, lookup, currentDBPath)
	if err != nil {
		t.Fatalf("classifyHookCancellationDiagnostics() error = %v", err)
	}
	if diff := cmp.Diff([]string{"active.json", "missing.json"}, diagnosticPaths(got.Actionable)); diff != "" {
		t.Fatalf("actionable paths mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ended.json"}, diagnosticPaths(got.Resolved)); diff != "" {
		t.Fatalf("resolved paths mismatch (-want +got):\n%s", diff)
	}
	if len(got.Unknown) != 0 {
		t.Fatalf("unknown paths = %v, want none", diagnosticPaths(got.Unknown))
	}
	if lookup.calls != 1 {
		t.Fatalf("FindEndedSessionIDs() calls = %d, want 1", lookup.calls)
	}
}

func TestClassifyHookCancellationDiagnosticsUnknownStore(t *testing.T) {
	const currentDBPath = "/store/current.db"
	endedID := types.SessionID("ended-session")
	records := []hookCancellationDiagnostic{
		// Marker written against a different --db-path store: the session
		// lookup below would happily report it "ended" if queried, but it
		// must never be classified from the wrong store's evidence.
		{SessionID: endedID.String(), DBPath: "/store/other.db", Path: "other-store.json"},
		// Marker predating db_path recording: also unknown, never actionable.
		{SessionID: "legacy-session", DBPath: "", Path: "legacy.json"},
	}
	lookup := &hookDiagnosticSessionLookupStub{ended: map[types.SessionID]struct{}{endedID: {}}}

	got, err := classifyHookCancellationDiagnostics(context.Background(), records, lookup, currentDBPath)
	if err != nil {
		t.Fatalf("classifyHookCancellationDiagnostics() error = %v", err)
	}
	if len(got.Actionable) != 0 {
		t.Fatalf("actionable paths = %v, want none", diagnosticPaths(got.Actionable))
	}
	if len(got.Resolved) != 0 {
		t.Fatalf("resolved paths = %v, want none", diagnosticPaths(got.Resolved))
	}
	if diff := cmp.Diff([]string{"other-store.json", "legacy.json"}, diagnosticPaths(got.Unknown)); diff != "" {
		t.Fatalf("unknown paths mismatch (-want +got):\n%s", diff)
	}
	// Records with no bearing on the current store never need a session
	// lookup at all.
	if lookup.calls != 0 {
		t.Fatalf("FindEndedSessionIDs() calls = %d, want 0", lookup.calls)
	}
}

func TestClassifyHookCancellationDiagnosticsCurrentDBPathEmpty(t *testing.T) {
	records := []hookCancellationDiagnostic{
		{SessionID: "some-session", DBPath: "/store/a.db", Path: "a.json"},
	}
	lookup := &hookDiagnosticSessionLookupStub{}

	got, err := classifyHookCancellationDiagnostics(context.Background(), records, lookup, "")
	if err != nil {
		t.Fatalf("classifyHookCancellationDiagnostics() error = %v", err)
	}
	if len(got.Actionable) != 0 || len(got.Resolved) != 0 {
		t.Fatalf("expected everything unknown when current db path is unresolved, got actionable=%v resolved=%v", got.Actionable, got.Resolved)
	}
	if diff := cmp.Diff([]string{"a.json"}, diagnosticPaths(got.Unknown)); diff != "" {
		t.Fatalf("unknown paths mismatch (-want +got):\n%s", diff)
	}
}

func TestResolvedHookCancellationDiagnosticFix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolved.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fix := resolvedHookCancellationDiagnosticFix([]hookCancellationDiagnostic{{Path: path}})

	action, err := fix(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run fix error = %v", err)
	}
	if action != "would remove 1 Claude SessionEnd hook cancellation diagnostic(s)" {
		t.Fatalf("dry-run action = %q", action)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry-run removed marker: %v", err)
	}

	action, err = fix(context.Background(), false)
	if err != nil {
		t.Fatalf("apply fix error = %v", err)
	}
	if action != "removed 1 Claude SessionEnd hook cancellation diagnostic(s)" {
		t.Fatalf("apply action = %q", action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker still exists after apply: %v", err)
	}
}

func TestOlderDuplicateHookCancellationDiagnostics(t *testing.T) {
	t.Parallel()
	newer := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	records := []hookCancellationDiagnostic{
		{Path: "old.json", SessionID: "same", StartedAt: newer.Add(-48 * time.Hour)},
		{Path: "newest.json", SessionID: "same", StartedAt: newer},
		{Path: "mid.json", SessionID: "same", StartedAt: newer.Add(-time.Hour)},
		{Path: "other.json", SessionID: "other", StartedAt: newer.Add(-time.Hour)},
		{Path: "empty.json", SessionID: "", StartedAt: newer.Add(-time.Hour)},
	}
	got := olderDuplicateHookCancellationDiagnostics(records)
	if diff := cmp.Diff([]string{"mid.json", "old.json"}, diagnosticPaths(got)); diff != "" {
		t.Fatalf("older duplicate paths mismatch (-want +got):\n%s", diff)
	}
}

func TestHookCancellationDiagnosticCleanupRecords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	classification := hookCancellationDiagnosticClassification{
		Actionable: []hookCancellationDiagnostic{
			{Path: "newest-open.json", SessionID: "open", StartedAt: now.Add(-time.Hour)},
			{Path: "older-open.json", SessionID: "open", StartedAt: now.Add(-2 * time.Hour)},
			{Path: "ancient-open.json", SessionID: "ancient", StartedAt: now.Add(-hookCancellationDiagnosticDoctorRetention - time.Hour)},
		},
		Resolved: []hookCancellationDiagnostic{
			{Path: "ended.json", SessionID: "ended", StartedAt: now.Add(-time.Hour)},
		},
		Unknown: []hookCancellationDiagnostic{
			{Path: "fresh-unknown.json", SessionID: "other-store", StartedAt: now.Add(-time.Hour)},
			{Path: "aged-unknown.json", SessionID: "other-store-old", StartedAt: now.Add(-hookCancellationDiagnosticDoctorRetention - time.Hour)},
		},
	}
	got := hookCancellationDiagnosticCleanupRecords(classification, now)
	if diff := cmp.Diff([]string{"ended.json", "older-open.json", "ancient-open.json", "aged-unknown.json"}, diagnosticPaths(got)); diff != "" {
		t.Fatalf("cleanup paths mismatch (-want +got):\n%s", diff)
	}
	remaining := excludeHookCancellationDiagnostics(classification.Actionable, got)
	if diff := cmp.Diff([]string{"newest-open.json"}, diagnosticPaths(remaining)); diff != "" {
		t.Fatalf("remaining actionable mismatch (-want +got):\n%s", diff)
	}
}

func TestInspectClaudeHookCancellationDiagnostics_FixEndedDuplicatesAndAncient(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	diagDir := filepath.Join(stateDir, hookDiagnosticsDirName)
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const dbPath = "/store/current.db"
	write := func(name, sessionID string, startedAt time.Time) string {
		path := filepath.Join(diagDir, name)
		writeHookCancellationDiagnosticForTest(t, path, hookCancellationDiagnostic{
			SchemaVersion: hookCancellationDiagnosticSchemaVersion,
			Client:        "claude",
			HostEvent:     "SessionEnd",
			HookCommand:   "cmd",
			SessionID:     sessionID,
			DBPath:        dbPath,
			Status:        hookCancellationDiagnosticStatusStarted,
			StartedAt:     startedAt,
		})
		return path
	}
	endedPath := write("ended.json", "ended-session", now.Add(-2*time.Hour))
	newestOpen := write("open-new.json", "open-session", now.Add(-time.Hour))
	olderOpen := write("open-old.json", "open-session", now.Add(-3*time.Hour))
	ancientPath := write("ancient.json", "ancient-session", now.Add(-hookCancellationDiagnosticDoctorRetention-time.Hour))
	genuinePath := newestOpen

	lookup := &hookDiagnosticSessionLookupStub{ended: map[types.SessionID]struct{}{"ended-session": {}}}
	root := &RootCLI{}
	check := root.inspectClaudeHookCancellationDiagnosticsWithLookup(context.Background(), dbPath, "", lookup)
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q message=%q", check.Status, check.Message)
	}
	if !strings.Contains(check.Hint, "doctor --fix") {
		t.Fatalf("hint %q, want doctor --fix automatic path", check.Hint)
	}
	if !strings.Contains(check.Message, "found 1 unresolved") {
		t.Fatalf("message %q, want 1 unresolved remaining", check.Message)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("expected auto-fix: %+v", check)
	}

	action, err := check.FixFunc(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(action, "would remove 3") {
		t.Fatalf("dry-run action=%q, want 3 removals", action)
	}
	for _, path := range []string{endedPath, olderOpen, ancientPath, genuinePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run removed %s: %v", path, err)
		}
	}

	if _, err := check.FixFunc(context.Background(), false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(genuinePath); err != nil {
		t.Fatalf("genuine un-ended newest marker must remain: %v", err)
	}
	for _, path := range []string{endedPath, olderOpen, ancientPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived --fix: err=%v", path, err)
		}
	}
}

func TestInspectClaudeHookCancellationDiagnosticsFilesystem_DedupesWithoutLookup(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	diagDir := filepath.Join(stateDir, hookDiagnosticsDirName)
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const dbPath = "/store/current.db"
	for i, age := range []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour} {
		writeHookCancellationDiagnosticForTest(t, filepath.Join(diagDir, fmt.Sprintf("dup-%d.json", i)), hookCancellationDiagnostic{
			SchemaVersion: hookCancellationDiagnosticSchemaVersion,
			Client:        "claude",
			HostEvent:     "SessionEnd",
			SessionID:     "same-session",
			DBPath:        dbPath,
			Status:        hookCancellationDiagnosticStatusStarted,
			StartedAt:     now.Add(-age),
		})
	}
	root := &RootCLI{}
	check := root.inspectClaudeHookCancellationDiagnosticsFilesystem(context.Background(), dbPath, "")
	if !strings.Contains(check.Message, "found 1 unresolved") {
		t.Fatalf("filesystem inspect must dedupe without SQLite lookup: %q", check.Message)
	}
	if !check.AutoFixAvailable {
		t.Fatalf("older duplicates must still be auto-fixable without session lookup")
	}
}

func TestAgedHookCancellationDiagnostics(t *testing.T) {
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	records := []hookCancellationDiagnostic{
		{Path: "old.json", StartedAt: cutoff.Add(-time.Hour)},
		{Path: "new.json", StartedAt: cutoff.Add(time.Hour)},
	}
	got := agedHookCancellationDiagnostics(records, cutoff)
	if diff := cmp.Diff([]string{"old.json"}, diagnosticPaths(got)); diff != "" {
		t.Fatalf("aged paths mismatch (-want +got):\n%s", diff)
	}
}

func TestBeginHookCancellationDiagnosticRecordsDBPathAndPhase(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())

	path, err := beginHookCancellationDiagnostic("claude", "SessionEnd", "cmd", types.SessionID("session-a"), "", "/store/current.db")
	if err != nil {
		t.Fatalf("beginHookCancellationDiagnostic() error = %v", err)
	}
	record := readHookCancellationDiagnosticForTest(t, path)
	if record.DBPath != "/store/current.db" {
		t.Fatalf("db_path = %q, want /store/current.db", record.DBPath)
	}
	if record.Phase != "" {
		t.Fatalf("phase = %q, want empty before any update", record.Phase)
	}
	if record.Status != hookCancellationDiagnosticStatusStarted {
		t.Fatalf("status = %q, want started", record.Status)
	}

	if err := updateHookCancellationDiagnosticPhase(path, hookCancellationDiagnosticPhaseWorkspaceResolved); err != nil {
		t.Fatalf("updateHookCancellationDiagnosticPhase(workspace_resolved) error = %v", err)
	}
	record = readHookCancellationDiagnosticForTest(t, path)
	if record.Phase != hookCancellationDiagnosticPhaseWorkspaceResolved {
		t.Fatalf("phase = %q, want %q", record.Phase, hookCancellationDiagnosticPhaseWorkspaceResolved)
	}

	if err := updateHookCancellationDiagnosticPhase(path, hookCancellationDiagnosticPhaseStoreInitialized); err != nil {
		t.Fatalf("updateHookCancellationDiagnosticPhase(store_initialized) error = %v", err)
	}
	record = readHookCancellationDiagnosticForTest(t, path)
	if record.Phase != hookCancellationDiagnosticPhaseStoreInitialized {
		t.Fatalf("phase = %q, want %q", record.Phase, hookCancellationDiagnosticPhaseStoreInitialized)
	}
	if record.DBPath != "/store/current.db" {
		t.Fatalf("db_path after phase update = %q, want unchanged /store/current.db", record.DBPath)
	}
}

func TestBeginHookCancellationDiagnosticGCByCap(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())

	var paths []string
	for i := 0; i < hookCancellationDiagnosticRetentionCap+5; i++ {
		sessionID := types.SessionID(fmt.Sprintf("session-%02d", i))
		path, err := beginHookCancellationDiagnostic("claude", "SessionEnd", "cmd", sessionID, "", "/store/current.db")
		if err != nil {
			t.Fatalf("beginHookCancellationDiagnostic() iteration %d error = %v", i, err)
		}
		paths = append(paths, path)
	}

	scan, err := scanHookCancellationDiagnostics("claude", "SessionEnd", "")
	if err != nil {
		t.Fatalf("scanHookCancellationDiagnostics() error = %v", err)
	}
	if len(scan.Records) != hookCancellationDiagnosticRetentionCap {
		t.Fatalf("markers remaining = %d, want cap %d", len(scan.Records), hookCancellationDiagnosticRetentionCap)
	}
	// The most recently written marker must survive GC.
	if _, err := os.Stat(paths[len(paths)-1]); err != nil {
		t.Fatalf("latest marker missing after GC: %v", err)
	}
	// The earliest marker must have been GC'd once the cap is exceeded.
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("earliest marker survived GC beyond cap: err=%v", err)
	}
}

func TestBeginHookCancellationDiagnosticGCByWindow(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)

	diagnosticsDir := filepath.Join(stateDir, hookDiagnosticsDirName)
	if err := os.MkdirAll(diagnosticsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	staleRecord := hookCancellationDiagnostic{
		SchemaVersion: hookCancellationDiagnosticSchemaVersion,
		Client:        "claude",
		HostEvent:     "SessionEnd",
		SessionID:     "stale-session",
		DBPath:        "/store/current.db",
		Status:        hookCancellationDiagnosticStatusStarted,
		StartedAt:     time.Now().UTC().Add(-hookCancellationDiagnosticRetentionWindow - time.Hour),
	}
	stalePath := filepath.Join(diagnosticsDir, "stale-marker.json")
	writeHookCancellationDiagnosticForTest(t, stalePath, staleRecord)

	if _, err := beginHookCancellationDiagnostic("claude", "SessionEnd", "cmd", types.SessionID("fresh-session"), "", "/store/current.db"); err != nil {
		t.Fatalf("beginHookCancellationDiagnostic() error = %v", err)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale marker survived begin-time GC: err=%v", err)
	}
	scan, err := scanHookCancellationDiagnostics("claude", "SessionEnd", "")
	if err != nil {
		t.Fatalf("scanHookCancellationDiagnostics() error = %v", err)
	}
	if len(scan.Records) != 1 {
		t.Fatalf("markers remaining = %d, want 1 (only the fresh marker)", len(scan.Records))
	}
}

func diagnosticPaths(records []hookCancellationDiagnostic) []string {
	paths := make([]string, 0, len(records))
	for _, record := range records {
		paths = append(paths, record.Path)
	}
	return paths
}

func readHookCancellationDiagnosticForTest(t *testing.T, path string) hookCancellationDiagnostic {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var record hookCancellationDiagnostic
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return record
}

func writeHookCancellationDiagnosticForTest(t *testing.T, path string, record hookCancellationDiagnostic) {
	t.Helper()
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func TestScanHookCancellationDiagnosticsSkipsEmptyMarkers(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Now().UTC()
	diagDir := filepath.Join(stateDir, "diagnostics")
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	emptyAged := filepath.Join(diagDir, "claude-SessionEnd-empty-aged.json")
	emptyFresh := filepath.Join(diagDir, "claude-SessionEnd-empty-fresh.json")
	garbage := filepath.Join(diagDir, "claude-SessionEnd-garbage.json")
	for _, path := range []string{emptyAged, emptyFresh} {
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(garbage, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	agedAt := now.Add(-hookStateResidueRetention - time.Hour)
	if err := os.Chtimes(emptyAged, agedAt, agedAt); err != nil {
		t.Fatal(err)
	}

	scan, err := scanHookCancellationDiagnostics("claude", "SessionEnd", "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scan.Records) != 0 {
		t.Fatalf("records=%d, want 0", len(scan.Records))
	}
	if len(scan.Unreadable) != 1 || scan.Unreadable[0] != garbage {
		t.Fatalf("unreadable=%v, want only nonempty garbage", scan.Unreadable)
	}

	root := &RootCLI{}
	check := root.inspectClaudeHookCancellationDiagnosticsWithLookup(context.Background(), "", "", nil)
	if strings.Contains(check.Message, emptyAged) || strings.Contains(check.Message, emptyFresh) {
		t.Fatalf("empty markers must not be needs-attention: %q", check.Message)
	}
	if !strings.Contains(check.Message, "unreadable nonempty") || !strings.Contains(check.Message, garbage) {
		t.Fatalf("nonempty unreadable must remain reported: %q", check.Message)
	}
}

type endedSessionInspectorStub struct {
	ended   map[types.SessionID]struct{}
	err     error
	dbPaths []string
}

func (s *endedSessionInspectorStub) FindEndedSessionIDs(_ context.Context, dbPath string, _ []types.SessionID) (map[types.SessionID]struct{}, error) {
	s.dbPaths = append(s.dbPaths, dbPath)
	if s.err != nil {
		return nil, s.err
	}
	return s.ended, nil
}

// The filesystem (large-store) inspect must resolve markers whose sessions
// already ended instead of reporting every same-store marker actionable the
// way the previous nil lookup did (#2235).
func TestInspectClaudeHookCancellationDiagnosticsFilesystem_ResolvesEndedSession(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	diagDir := filepath.Join(stateDir, hookDiagnosticsDirName)
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const dbPath = "/store/current.db"
	writeHookCancellationDiagnosticForTest(t, filepath.Join(diagDir, "ended.json"), hookCancellationDiagnostic{
		SchemaVersion: hookCancellationDiagnosticSchemaVersion,
		Client:        "claude",
		HostEvent:     "SessionEnd",
		SessionID:     "ended-session",
		DBPath:        dbPath,
		Status:        hookCancellationDiagnosticStatusStarted,
		StartedAt:     time.Now().UTC().Add(-time.Hour),
	})

	inspector := &endedSessionInspectorStub{ended: map[types.SessionID]struct{}{"ended-session": {}}}
	root := &RootCLI{endedSessionInspector: inspector}
	check := root.inspectClaudeHookCancellationDiagnosticsFilesystem(context.Background(), dbPath, "")
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q message=%q", check.Status, check.Message)
	}
	if strings.Contains(check.Message, "unresolved") {
		t.Fatalf("ended session marker must not stay unresolved: %q", check.Message)
	}
	if !strings.Contains(check.Message, "resolved=1") {
		t.Fatalf("message %q, want resolved=1 cleanup path", check.Message)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("resolved marker must stay auto-fixable: %+v", check)
	}
	if len(inspector.dbPaths) != 1 || inspector.dbPaths[0] != dbPath {
		t.Fatalf("inspector dbPaths = %v, want [%s]", inspector.dbPaths, dbPath)
	}
}

// A marker without an ended (or any) session row stays actionable; only the
// marker backed by a real session_ended resolves. Drives the shipped
// filesystem inspect against a real store so the nil-lookup blind spot
// cannot regress (#2235).
func TestInspectClaudeHookCancellationDiagnosticsFilesystem_EndToEndStore(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	diagDir := filepath.Join(stateDir, hookDiagnosticsDirName)
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := sqliteinfra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionDS := sqliteinfra.NewSessionDatasource(database)
	agent, err := types.AgentFrom("claude")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	startedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	endedID := types.SessionID("ended-session")
	openID := types.SessionID("open-session")
	for _, sessionID := range []types.SessionID{endedID, openID} {
		session := model.NewSession(sessionID, startedAt, types.Client("cli"), agent, types.Workspace("workspace"))
		event := model.EventOf(types.EventID("start-"+sessionID.String()), types.EventKindSessionStarted, types.Client("cli"), agent, sessionID, types.Workspace("workspace"), "started", startedAt)
		if err := sessionDS.SaveBoundary(ctx, session, event); err != nil {
			t.Fatalf("SaveBoundary(start %s) error = %v", sessionID, err)
		}
	}
	ended, err := sessionDS.FindByID(ctx, endedID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	endedSession, ok := ended.Value()
	if !ok {
		t.Fatal("ended session is missing")
	}
	endedAt := startedAt.Add(time.Minute)
	if err := endedSession.End(endedAt, "done"); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	endEvent := model.EventOf(types.EventID("end-ended-session"), types.EventKindSessionEnded, types.Client("cli"), agent, endedID, types.Workspace("workspace"), "ended", endedAt)
	if err := sessionDS.SaveBoundary(ctx, endedSession, endEvent); err != nil {
		t.Fatalf("SaveBoundary(end) error = %v", err)
	}

	now := time.Now().UTC()
	write := func(name, sessionID string) string {
		path := filepath.Join(diagDir, name)
		writeHookCancellationDiagnosticForTest(t, path, hookCancellationDiagnostic{
			SchemaVersion: hookCancellationDiagnosticSchemaVersion,
			Client:        "claude",
			HostEvent:     "SessionEnd",
			SessionID:     sessionID,
			DBPath:        dbPath,
			Status:        hookCancellationDiagnosticStatusStarted,
			StartedAt:     now.Add(-time.Hour),
		})
		return path
	}
	endedPath := write("ended.json", endedID.String())
	openPath := write("open.json", openID.String())
	ghostPath := write("ghost.json", "ghost-session")

	root := &RootCLI{endedSessionInspector: sqliteinfra.NewEndedSessionInspector()}
	check := root.inspectClaudeHookCancellationDiagnosticsFilesystem(ctx, dbPath, "")
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q message=%q", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, "found 2 unresolved") {
		t.Fatalf("message %q, want open and ghost sessions still unresolved", check.Message)
	}
	if !strings.Contains(check.Message, "1 resolved") {
		t.Fatalf("message %q, want 1 resolved", check.Message)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("resolved marker must be auto-fixable: %+v", check)
	}

	action, err := check.FixFunc(ctx, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(action, "would remove 1") {
		t.Fatalf("dry-run action=%q, want 1 removal", action)
	}
	if _, err := check.FixFunc(ctx, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(endedPath); !os.IsNotExist(err) {
		t.Fatalf("ended marker survived --fix: err=%v", err)
	}
	for _, path := range []string{openPath, ghostPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unresolved marker %s must remain: %v", path, err)
		}
	}
}
