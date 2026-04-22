package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteReplayHTML_EmitsSelfContainedFile(t *testing.T) {
	t.Parallel()

	outPath := filepath.Join(t.TempDir(), "replay.html")
	data := replayData{
		GeneratedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		DBPath:      "/tmp/traceary.db",
		Sessions: []replaySession{
			{
				SessionID: "abcdef1234567890",
				Workspace: "github.com/example/repo",
				Agents:    "claude/planner",
				Status:    "active",
				Label:     "incident triage",
				StartedAt: time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC),
				EndedAt:   "2026-04-21T11:30:00Z",
				Events: []replayEvent{
					{
						EventID:   "evt-1",
						Kind:      "prompt",
						CreatedAt: time.Date(2026, 4, 21, 10, 5, 0, 0, time.UTC),
						Body:      "investigate flaky test in replay module",
					},
					{
						EventID:   "evt-2",
						Kind:      "command_executed",
						CreatedAt: time.Date(2026, 4, 21, 10, 6, 0, 0, time.UTC),
						Body:      "go test ./...",
					},
				},
			},
		},
		Memories: []replayMemory{
			{
				MemoryID:  "mem-1",
				Type:      "decision",
				Scope:     "workspace=github.com/example/repo",
				Status:    "accepted",
				Fact:      "replay UI ships as static HTML, not TUI",
				ValidFrom: time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC),
				ValidTo:   "—",
				UpdatedAt: time.Date(2026, 4, 21, 10, 30, 0, 0, time.UTC),
			},
		},
		TimelineBlocks: []replayTimelineBlock{
			{
				Start:      "2026-04-21T09:00:00Z",
				End:        "2026-04-21T12:30:00Z",
				Duration:   "3h30m",
				EventCount: 42,
				Agents:     "claude",
				Workspaces: []replayTimelineWorkspace{
					{Workspace: "github.com/example/repo", EventCount: 42, Activity: "command_executed: 30, note: 10"},
				},
			},
		},
		FailureHotspots: []replayFailureHotspot{
			{Command: "go", Workspace: "github.com/example/repo", Count: 5, LastOccurredAt: "2026-04-21T11:20:00Z"},
		},
	}

	if err := writeReplayHTML(outPath, data); err != nil {
		t.Fatalf("writeReplayHTML() error = %v", err)
	}

	content, err := os.ReadFile(outPath) // #nosec G304 -- test-produced path
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	html := string(content)

	// Self-contained: no external script / link hrefs beyond inline CSS.
	if strings.Contains(html, "<script") {
		t.Errorf("replay HTML should not contain <script>, found one")
	}
	if strings.Contains(html, "<link ") {
		t.Errorf("replay HTML should not link external CSS / fonts")
	}
	// Session and memory content is rendered, plus the new timeline
	// and failure-hotspot panels added in v0.8-followup #630.
	for _, want := range []string{
		"incident triage",
		"github.com/example/repo",
		"investigate flaky test in replay module",
		"go test ./...",
		"replay UI ships as static HTML, not TUI",
		"Timeline blocks",
		"3h30m",
		"command_executed: 30",
		"Failure hotspots",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("replay HTML missing expected text %q", want)
		}
	}
	// File mode is operator-readable.
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o400 == 0 {
		t.Errorf("replay HTML must be readable, got mode %v", mode)
	}
}

func TestWriteReplayHTML_RefusesSymlinkTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.html")
	if err := os.WriteFile(victim, []byte("do-not-overwrite"), 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	link := filepath.Join(dir, "replay.html")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	err := writeReplayHTML(link, replayData{GeneratedAt: time.Now().UTC()})
	if err == nil {
		t.Fatalf("writeReplayHTML() error = nil, want symlink refusal")
	}
	data, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("ReadFile(victim) error = %v", readErr)
	}
	if string(data) != "do-not-overwrite" {
		t.Errorf("victim file was overwritten despite symlink refusal; got %q", string(data))
	}
}

func TestWriteReplayHTML_PreservesExistingOnTemplateError(t *testing.T) {
	// Intentionally not t.Parallel(): this test mutates the
	// package-level replayTemplateSource indirection to inject a
	// malformed template; running in parallel with the other replay
	// tests would race on that mutation.

	dir := t.TempDir()
	out := filepath.Join(dir, "replay.html")
	if err := os.WriteFile(out, []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile(out) error = %v", err)
	}
	// Swap the template source via the `replayTemplateSource`
	// indirection. `replayTemplateSource` is still a package-level
	// variable — the race avoidance here comes from this test
	// *not* calling t.Parallel() (see comment above), so go test's
	// "serial phase before parallel phase" ordering guarantees that
	// no other replay test runs while this one holds the swap.
	original := replayTemplateSource
	replayTemplateSource = func() string { return `{{.DoesNotExist}}` }
	defer func() { replayTemplateSource = original }()

	err := writeReplayHTML(out, replayData{GeneratedAt: time.Now().UTC()})
	if err == nil {
		t.Fatalf("writeReplayHTML() error = nil, want template render failure")
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("ReadFile(out) error = %v", readErr)
	}
	if string(data) != "original" {
		t.Errorf("existing file was overwritten after template error; got %q, want %q", string(data), "original")
	}
}

func TestWriteReplayHTML_EmptyCollections(t *testing.T) {
	t.Parallel()

	outPath := filepath.Join(t.TempDir(), "empty.html")
	if err := writeReplayHTML(outPath, replayData{GeneratedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("writeReplayHTML() error = %v", err)
	}
	content, err := os.ReadFile(outPath) // #nosec G304 -- test-produced path
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "No sessions recorded") {
		t.Errorf("empty replay should surface the empty-state notice for sessions")
	}
	if !strings.Contains(string(content), "No accepted memories") {
		t.Errorf("empty replay should surface the empty-state notice for memories")
	}
	if !strings.Contains(string(content), "No timeline blocks") {
		t.Errorf("empty replay should surface the empty-state notice for timeline blocks")
	}
	if !strings.Contains(string(content), "No command failures") {
		t.Errorf("empty replay should surface the empty-state notice for failure hotspots")
	}
}
