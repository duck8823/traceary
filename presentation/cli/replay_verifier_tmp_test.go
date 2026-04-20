package cli

import (
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestVerifierReplayRejectsSymlinkTarget(t *testing.T) {
    t.Parallel()

    dir := t.TempDir()
    victim := filepath.Join(dir, "victim.html")
    if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
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
}

func TestVerifierReplayCleansUpOnRenderError(t *testing.T) {
    t.Parallel()

    dir := t.TempDir()
    out := filepath.Join(dir, "replay.html")
    if err := os.WriteFile(out, []byte("original"), 0o644); err != nil {
        t.Fatalf("WriteFile(out) error = %v", err)
    }

    oldTemplate := replayTemplateHTML
    replayTemplateHTML = `{{.DoesNotExist}}`
    t.Cleanup(func() { replayTemplateHTML = oldTemplate })

    err := writeReplayHTML(out, replayData{GeneratedAt: time.Now().UTC()})
    if err == nil {
        t.Fatalf("writeReplayHTML() error = nil, want render failure")
    }

    data, readErr := os.ReadFile(out)
    if readErr != nil {
        t.Fatalf("ReadFile(out) error = %v", readErr)
    }
    if string(data) != "original" {
        t.Fatalf("output content = %q, want original content preserved or file cleaned up", string(data))
    }
}
