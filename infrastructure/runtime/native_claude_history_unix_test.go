//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeHistoryFixture(t *testing.T) (nativeStartEvent, []byte) {
	t.Helper()
	plan, _, _ := nativeStartFixture()
	namespace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(namespace, 0700); err != nil {
		t.Fatal(err)
	}
	plan.Namespace = namespace
	event := nativeStartEvent{Plan: plan, TranscriptPath: filepath.Join(namespace, "project", plan.SessionID+".jsonl")}
	message := map[string]any{"model": "claude-opus-5-5", "content": []any{map[string]any{"type": "text", "text": "synthetic private body"}}}
	row := map[string]any{"type": "assistant", "sessionId": plan.SessionID, "cwd": plan.CWD, "version": claudeRecoveryVersion, "message": message}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return event, append(data, '\n')
}

func TestClaudeRecoveryHistoryReturnsOnlyRecordedModelAndNamespaceIdentity(t *testing.T) {
	event, data := nativeHistoryFixture(t)
	// Opaque payload remains legal even with keys our private journal forbids.
	opaque := []byte("{\"type\":\"api-request-blob\",\"body\":{\"A\":1,\"a\":2,\"deep\":{\"more\":{\"data\":true}}}}\n")
	data = append(opaque, data...)
	data = bytes.Replace(data, []byte("\"sessionId\":"), []byte("\"session_id\":\""+event.Plan.SessionID+"\",\"sessionId\":"), 1)
	project := filepath.Dir(event.TranscriptPath)
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(event.TranscriptPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, err := readClaudeRecoveryHistory(context.Background(), event)
	if err != nil || metadata.Model != "claude-opus-5-5" || len(metadata.NamespaceWitness) != 64 {
		t.Fatalf("metadata lost: %+v %v", metadata, err)
	}
	encoded, _ := json.Marshal(metadata)
	if strings.Contains(string(encoded), "private body") || strings.Contains(string(encoded), event.Plan.SessionID) {
		t.Fatal("native body/identity escaped reduced history result")
	}
	again, err := readClaudeRecoveryHistory(context.Background(), event)
	if err != nil || again != metadata {
		t.Fatalf("physical namespace identity unstable: %v", err)
	}
}

func TestClaudeRecoveryHistoryRejectsAmbiguousOrChangedMetadata(t *testing.T) {
	event, data := nativeHistoryFixture(t)
	for _, tc := range []struct{ name, old, new string }{
		{"session", "12345678-1234-4234-8234-123456789abc", "12345678-1234-4234-8234-123456789def"},
		{"cwd", "/project", "/other"},
		{"version", "2.1.318", "2.1.319"},
		{"model", "claude-opus-5-5", "foreign-model"},
		{"null model", "\"model\":\"claude-opus-5-5\"", "\"model\":null"},
		{"duplicate session", "\"sessionId\":", "\"sessionId\":\"foreign\",\"sessionId\":"},
		{"conflicting native secondary ID", "\"sessionId\":", "\"session_id\":\"foreign\",\"sessionId\":"},
		{"case alias", "sessionId", "SessionId"},
		{"underscore alias", "sessionId", "session_id"},
		{"duplicate model", "\"model\":", "\"model\":\"foreign\",\"model\":"},
		{"model case alias", "\"model\"", "\"Model\""},
		{"type alias", "\"type\":\"assistant\"", "\"Type\":\"assistant\""},
		{"missing identity", "\"cwd\":\"/project\",", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := bytes.Replace(data, []byte(tc.old), []byte(tc.new), 1)
			if bytes.Equal(changed, data) {
				t.Fatal("test mutation did not apply")
			}
			if _, err := parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(changed), event.Plan); err == nil {
				t.Fatal("invalid history accepted")
			}
		})
	}
	for _, bad := range [][]byte{nil, []byte("null\n"), []byte("{}\n"), data[:len(data)-2], append(append([]byte{}, data...), 255), append(append([]byte{}, data...), []byte("{\"type\":\"system\",\"sessionId\":\"foreign\"}\n")...)} {
		if _, err := parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(bad), event.Plan); err == nil {
			t.Fatal("incomplete/trailing/foreign history accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseClaudeRecoveryHistory(ctx, bytes.NewReader(data), event.Plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestClaudeRecoveryHistoryCannotFollowUnsafeNativeFiles(t *testing.T) {
	for _, kind := range []string{"file symlink", "project symlink", "file hardlink", "public file", "public project", "oversize", "namespace symlink"} {
		t.Run(kind, func(t *testing.T) {
			event, data := nativeHistoryFixture(t)
			project := filepath.Dir(event.TranscriptPath)
			if err := os.Mkdir(project, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(event.TranscriptPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(t.TempDir(), "other")
			switch kind {
			case "file symlink":
				if err := os.Rename(event.TranscriptPath, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, event.TranscriptPath); err != nil {
					t.Fatal(err)
				}
			case "project symlink":
				if err := os.Rename(project, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, project); err != nil {
					t.Fatal(err)
				}
			case "file hardlink":
				if err := os.Link(event.TranscriptPath, other); err != nil {
					t.Fatal(err)
				}
			case "public file":
				if err := os.Chmod(event.TranscriptPath, 0644); err != nil {
					t.Fatal(err)
				}
			case "public project":
				if err := os.Chmod(project, 0755); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.Truncate(event.TranscriptPath, maxNativeHistoryBytes+1); err != nil {
					t.Fatal(err)
				}
			case "namespace symlink":
				if err := os.Symlink(event.Plan.Namespace, other); err != nil {
					t.Fatal(err)
				}
				event.Plan.Namespace = other
				event.TranscriptPath = filepath.Join(other, "project", event.Plan.SessionID+".jsonl")
			}
			if _, err := readClaudeRecoveryHistory(context.Background(), event); err == nil {
				t.Fatal("unsafe native history accepted")
			}
		})
	}
}

func TestClaudeRecoveryHistoryIsBoundedAndRequiresRecordedAssistantModel(t *testing.T) {
	event, data := nativeHistoryFixture(t)
	oversized := bytes.Repeat([]byte(" "), maxNativeHistoryLine+1)
	if _, err := parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(oversized), event.Plan); err == nil {
		t.Fatal("unbounded line accepted")
	}
	// Metadata without assistant model is not enough; no guessed/default model.
	missing := bytes.Replace(data, []byte("\"type\":\"assistant\""), []byte("\"type\":\"user\""), 1)
	if _, err := parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(missing), event.Plan); err == nil {
		t.Fatal("model guessed without native assistant evidence")
	}
	different := bytes.Replace(data, []byte("claude-opus-5-5"), []byte("claude-other"), 1)
	model, err := parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(append(data, different...)), event.Plan)
	if err != nil || model != "claude-other" {
		t.Fatalf("latest actual model not represented: %q %v", model, err)
	}
	synthetic := bytes.Replace(data, []byte("claude-opus-5-5"), []byte("<synthetic>"), 1)
	if _, err := parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(synthetic), event.Plan); err == nil {
		t.Fatal("native local synthetic message established model evidence")
	}
	model, err = parseClaudeRecoveryHistory(context.Background(), bytes.NewReader(append(data, synthetic...)), event.Plan)
	if err != nil || model != "claude-opus-5-5" {
		t.Fatalf("native local synthetic message replaced actual model: %q %v", model, err)
	}
}
