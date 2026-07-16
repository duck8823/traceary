package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestSummarizeToolAwareCommandBody_Edit(t *testing.T) {
	t.Parallel()
	body := "Edit path=/Users/me/repo/main.go\nINPUT:\n" + strings.Repeat("old line\n", 40) + "OUTPUT:\n" + strings.Repeat("new line\n", 40)
	got, ok := summarizeToolAwareCommandBody(body, "evt-edit-1")
	if !ok {
		t.Fatal("expected tool-aware summary")
	}
	for _, want := range []string{"tool=Edit", "path=", "input_runes=", "sha256=", "truncated=true", "traceary show evt-edit-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
	if strings.Contains(got, strings.Repeat("old line", 5)) {
		t.Fatalf("summary leaked full edit body: %q", got)
	}
}

func TestSummarizeToolAwareCommandBody_Read(t *testing.T) {
	t.Parallel()
	body := "Read path=docs/cli/README.md\n" + strings.Repeat("# heading\ncontent line\n", 50)
	got, ok := summarizeToolAwareCommandBody(body, "evt-read-1")
	if !ok {
		t.Fatal("expected tool-aware summary")
	}
	if !strings.Contains(got, "tool=Read") || !strings.Contains(got, "docs/cli/README.md") {
		t.Fatalf("summary = %q", got)
	}
}

func TestSummarizeToolAwareCommandBody_SkipsSmallBodies(t *testing.T) {
	t.Parallel()
	if _, ok := summarizeToolAwareCommandBody("Edit path=x\nshort", "evt"); ok {
		t.Fatal("small body should not use tool-aware summary")
	}
}

func TestNewTruncatedEventOutput_UsesToolAwareSummary(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	body := "Write path=/tmp/out.txt\nINPUT:\n" + strings.Repeat("payload-line\n", 60)
	ev := model.EventOf(
		domtypes.EventID("evt-write-1"),
		domtypes.EventKindCommandExecuted,
		domtypes.Client("hook"),
		domtypes.Agent("claude"),
		domtypes.SessionID("sess-1"),
		domtypes.Workspace("github.com/duck8823/traceary"),
		body,
		createdAt,
	)
	out := newTruncatedEventOutput(ev, 500)
	if !out.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if !strings.Contains(out.Message, "tool=Write") {
		t.Fatalf("message = %q, want tool-aware Write summary", out.Message)
	}
	if strings.Contains(out.Message, "payload-line\npayload-line") {
		t.Fatalf("message leaked write body: %q", out.Message)
	}
}
