package memoryeval_test

import (
	"os/exec"
	"testing"
)

// Keep the stdlib-only experimental scorer tests in the normal go test gate.
func TestOfflineEvaluationScorer(t *testing.T) {
	cmd := exec.Command("python3", "-B", "-m", "unittest", "-v", "test_evaluate.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline scorer tests failed: %v\n%s", err, output)
	}
}
