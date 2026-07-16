package cli

import (
	"testing"
	"time"
)

func TestDetectRetryLoops_FindsIdenticalFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	base := now.Add(-10 * time.Minute)
	inputs := []retryLoopInput{
		{EventID: "e1", Workspace: "ws", Agent: "claude", Command: "Read /tmp", Output: "EISDIR: is a directory", ExitCode: "1", CreatedAt: base},
		{EventID: "e2", Workspace: "ws", Agent: "claude", Command: "Read /tmp", Output: "EISDIR: is a directory", ExitCode: "1", CreatedAt: base.Add(time.Minute)},
		{EventID: "e3", Workspace: "ws", Agent: "claude", Command: "Read /tmp", Output: "EISDIR: is a directory", ExitCode: "1", CreatedAt: base.Add(2 * time.Minute)},
	}
	groups := detectRetryLoops(inputs, now)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if groups[0].ErrorClass != "eisdir" || groups[0].Count != 3 {
		t.Fatalf("group = %+v", groups[0])
	}
	if len(groups[0].SampleIDs) != 3 {
		t.Fatalf("sample ids = %v", groups[0].SampleIDs)
	}
}

func TestDetectRetryLoops_SandboxBypassClass(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	base := now.Add(-5 * time.Minute)
	cmd := "make validate"
	out := "This command requires access to files outside the workspace. Retry with BypassSandbox"
	inputs := []retryLoopInput{
		{EventID: "a1", Workspace: "ws", Agent: "antigravity", Command: cmd, Output: out, ExitCode: "1", CreatedAt: base},
		{EventID: "a2", Workspace: "ws", Agent: "antigravity", Command: cmd, Output: out, ExitCode: "1", CreatedAt: base.Add(time.Minute)},
		{EventID: "a3", Workspace: "ws", Agent: "antigravity", Command: cmd, Output: out, ExitCode: "1", CreatedAt: base.Add(2 * time.Minute)},
	}
	groups := detectRetryLoops(inputs, now)
	if len(groups) != 1 || groups[0].ErrorClass != "sandbox_bypass_required" {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestDetectRetryLoops_IgnoresSparseLegitimateReruns(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	// Only two failures — below min count.
	inputs := []retryLoopInput{
		{EventID: "e1", Workspace: "ws", Agent: "claude", Command: "go test ./...", Output: "FAIL", ExitCode: "1", CreatedAt: now.Add(-30 * time.Minute)},
		{EventID: "e2", Workspace: "ws", Agent: "claude", Command: "go test ./...", Output: "FAIL", ExitCode: "1", CreatedAt: now.Add(-20 * time.Minute)},
	}
	if groups := detectRetryLoops(inputs, now); len(groups) != 0 {
		t.Fatalf("groups = %+v, want none for count < 3", groups)
	}
}

func TestDetectRetryLoops_IgnoresOutsideWindowFromFirst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	base := now.Add(-3 * time.Hour)
	inputs := []retryLoopInput{
		{EventID: "e1", Workspace: "ws", Agent: "claude", Command: "Read missing", Output: "no such file", ExitCode: "1", CreatedAt: base},
		{EventID: "e2", Workspace: "ws", Agent: "claude", Command: "Read missing", Output: "no such file", ExitCode: "1", CreatedAt: base.Add(time.Minute)},
		// Outside window from first — should not join the cluster.
		{EventID: "e3", Workspace: "ws", Agent: "claude", Command: "Read missing", Output: "no such file", ExitCode: "1", CreatedAt: base.Add(3 * time.Hour)},
	}
	if groups := detectRetryLoops(inputs, now); len(groups) != 0 {
		t.Fatalf("groups = %+v, want none when third event is outside window", groups)
	}
}

func TestClassifyRetryLoopErrorClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd, out, code, want string
	}{
		{"Read x", "EISDIR", "1", "eisdir"},
		{"Read x", "no such file or directory", "1", "missing_path"},
		{"Read x", "file too large for tool", "1", "oversized_file"},
		{"make ci", "BypassSandbox required", "1", "sandbox_bypass_required"},
		{"go test", "FAIL", "1", "exit_1"},
	}
	for _, tc := range cases {
		if got := classifyRetryLoopErrorClass(tc.cmd, tc.out, tc.code); got != tc.want {
			t.Fatalf("class(%q,%q,%q)=%q want %q", tc.cmd, tc.out, tc.code, got, tc.want)
		}
	}
}
