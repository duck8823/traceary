package lessoneval_test

import (
	"os/exec"
	"testing"
)

func TestOfflineLessonScorer(t *testing.T) {
	cmd := exec.Command("python3", "-B", "-m", "unittest", "-v", "test_evaluate.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline lesson scorer tests failed: %v\n%s", err, output)
	}
}
