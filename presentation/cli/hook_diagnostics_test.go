package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	endedID := types.SessionID("ended-session")
	activeID := types.SessionID("active-session")
	missingID := types.SessionID("missing-session")
	records := []hookCancellationDiagnostic{
		{SessionID: endedID.String(), Path: "ended.json"},
		{SessionID: activeID.String(), Path: "active.json"},
		{SessionID: missingID.String(), Path: "missing.json"},
	}
	lookup := &hookDiagnosticSessionLookupStub{ended: map[types.SessionID]struct{}{endedID: {}}}

	got, err := classifyHookCancellationDiagnostics(context.Background(), records, lookup)
	if err != nil {
		t.Fatalf("classifyHookCancellationDiagnostics() error = %v", err)
	}
	if diff := cmp.Diff([]string{"active.json", "missing.json"}, diagnosticPaths(got.Actionable)); diff != "" {
		t.Fatalf("actionable paths mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ended.json"}, diagnosticPaths(got.Resolved)); diff != "" {
		t.Fatalf("resolved paths mismatch (-want +got):\n%s", diff)
	}
	if lookup.calls != 1 {
		t.Fatalf("FindEndedSessionIDs() calls = %d, want 1", lookup.calls)
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

func diagnosticPaths(records []hookCancellationDiagnostic) []string {
	paths := make([]string, 0, len(records))
	for _, record := range records {
		paths = append(paths, record.Path)
	}
	return paths
}
