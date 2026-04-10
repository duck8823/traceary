package hooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGolden_SessionStartOutput(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_REPO=golden-repo",
		"HOME="+homeDir,
	)

	inputData, err := os.ReadFile("testdata/session_start_input.json")
	if err != nil {
		t.Fatalf("ReadFile(input) error = %v", err)
	}

	if err := runHookScript(t, filepath.Join(".", "traceary-session.sh"), env, string(inputData), "claude", "start"); err != nil {
		t.Fatalf("runHookScript(start) error = %v", err)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}

	expectedData, err := os.ReadFile("testdata/session_start_expected.json")
	if err != nil {
		t.Fatalf("ReadFile(expected) error = %v", err)
	}

	var expected []string
	if err := json.Unmarshal(expectedData, &expected); err != nil {
		t.Fatalf("Unmarshal(expected) error = %v", err)
	}

	if !reflect.DeepEqual(calls[0], expected) {
		t.Fatalf("golden mismatch:\n  got:  %#v\n  want: %#v", calls[0], expected)
	}
}
