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
				Agent:     "claude/planner",
				Client:    "active",
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
	// Session and memory content is rendered.
	for _, want := range []string{
		"incident triage",
		"github.com/example/repo",
		"investigate flaky test in replay module",
		"go test ./...",
		"replay UI ships as static HTML, not TUI",
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
}
