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

	"github.com/duck8823/traceary/domain/types"
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
	if action != "would remove 1 resolved Claude SessionEnd hook cancellation diagnostic(s)" {
		t.Fatalf("dry-run action = %q", action)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry-run removed marker: %v", err)
	}

	action, err = fix(context.Background(), false)
	if err != nil {
		t.Fatalf("apply fix error = %v", err)
	}
	if action != "removed 1 resolved Claude SessionEnd hook cancellation diagnostic(s)" {
		t.Fatalf("apply action = %q", action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker still exists after apply: %v", err)
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
