package hooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGolden_SessionStartDelegation(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_FAKE_SESSION_OUTPUT=golden-session\n",
	)

	inputData, err := os.ReadFile("testdata/session_start_input.json")
	if err != nil {
		t.Fatalf("ReadFile(input) error = %v", err)
	}

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-session.sh"), env, string(inputData), "claude", "start")
	if err != nil {
		t.Fatalf("runHookScriptCapture(start) error = %v", err)
	}
	if diff := cmp.Diff("golden-session\n", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
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

	if diff := cmp.Diff(expected, calls[0]); diff != "" {
		t.Fatalf("golden mismatch (-want +got):\n%s", diff)
	}
}
