package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestClaudeUsageSource_DeduplicatesRequestAndMessageIdentityWithoutPrivateBodies(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-claude-1"
	path := writeClaudeUsageFixture(t, home, sessionID, strings.Join([]string{
		`{"type":"user","sessionId":"session-claude-1","message":{"content":"PRIVATE-PROMPT"}}`,
		`{"type":"assistant","sessionId":"session-claude-1","requestId":"req-1","uuid":"row-a","timestamp":"2026-07-23T01:00:00Z","message":{"id":"msg-1","model":"claude-opus-4-1","content":[{"type":"text","text":"PRIVATE-RESPONSE"}],"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":8,"output_tokens":3}}}`,
		`{"type":"assistant","sessionId":"session-claude-1","requestId":"req-1","uuid":"row-b","timestamp":"2026-07-23T01:00:00Z","message":{"id":"msg-1","model":"claude-opus-4-1","content":[{"type":"text","text":"PRIVATE-DUPLICATE"}],"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":8,"output_tokens":3}}}`,
		`{"type":"assistant","sessionId":"session-claude-1","requestId":"req-2","timestamp":"2026-07-23T01:00:01Z","message":{"id":"msg-1","model":"claude-opus-4-1","usage":{"input_tokens":4,"cache_read_input_tokens":0,"output_tokens":1}}}`,
	}, "\n")+"\n")
	source := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	)
	result, err := source.Load(
		context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)},
	)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", filepath.Base(path), err)
	}
	if result.Mode != application.ClaudeUsageModeTranscriptCalls ||
		!result.BoundaryObserved || len(result.Samples) != 2 {
		t.Fatalf("result = %+v", result)
	}
	first := result.Samples[0]
	if first.Scope != types.UsageScopeCall || !first.Available ||
		first.Model != "claude-opus-4-1" || first.Counters.CacheWriteInputTokens == nil ||
		*first.Counters.CacheWriteInputTokens != 0 {
		t.Fatalf("first sample = %+v", first)
	}
	if first.RecordID == result.Samples[1].RecordID {
		t.Fatal("distinct request IDs collapsed into one call identity")
	}
	for _, sample := range result.Samples {
		if strings.Contains(sample.RecordID, "req-") || strings.Contains(sample.RecordID, "msg-") {
			t.Fatalf("source identity was not opaque: %q", sample.RecordID)
		}
	}
}

func TestClaudeUsageSource_LegacyResultWinsWhileRetainingCallEvidence(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-claude-legacy"
	writeClaudeUsageFixture(t, home, sessionID, strings.Join([]string{
		`{"type":"assistant","session_id":"session-claude-legacy","requestId":"req-1","timestamp":"2026-07-23T01:00:00Z","message":{"id":"msg-1","model":"claude-sonnet-4","usage":{"input_tokens":10,"output_tokens":2}}}`,
		`{"type":"result","session_id":"session-claude-legacy","subtype":"success","timestamp":"2026-07-23T01:00:01Z","usage":{"input_tokens":20,"cache_creation_input_tokens":3,"cache_read_input_tokens":4,"output_tokens":5},"modelUsage":{"claude-sonnet-4":{"costUSD":0.01}},"result":"PRIVATE-RESULT"}`,
	}, "\n")+"\n")
	result, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if result.Mode != application.ClaudeUsageModeOneShotStream ||
		!result.BoundaryObserved || len(result.Samples) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Samples[0].Scope != types.UsageScopeRun ||
		result.Samples[0].Model != "claude-sonnet-4" ||
		result.Samples[1].Scope != types.UsageScopeCall {
		t.Fatalf("samples = %+v", result.Samples)
	}
}

func TestClaudeUsageSource_MissingSelectedUsageIsExplicitlyUnavailable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-claude-aborted"
	writeClaudeUsageFixture(t, home, sessionID,
		`{"type":"result","session_id":"session-claude-aborted","subtype":"error","is_error":true,"timestamp":"2026-07-23T01:00:01Z","error":"PRIVATE-ERROR"}`+"\n",
	)
	result, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 1 || result.Samples[0].Available ||
		result.Samples[0].TerminalCode != types.UsageTerminalFailure {
		t.Fatalf("result = %+v", result)
	}
}

func TestClaudeUsageSource_ErrorResultRetainsReportedUsageWithoutInferringSuccess(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-claude-error-usage"
	writeClaudeUsageFixture(t, home, sessionID,
		`{"type":"result","session_id":"session-claude-error-usage","subtype":"error","is_error":true,"timestamp":"2026-07-23T01:00:01Z","usage":{"input_tokens":3,"output_tokens":1},"error":"PRIVATE-ERROR"}`+"\n",
	)
	result, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 1 || !result.Samples[0].Available ||
		result.Samples[0].TerminalCode != types.UsageTerminalFailure {
		t.Fatalf("result = %+v", result)
	}
}

func TestClaudeUsageSource_RejectsConflictingDuplicateAssistantUsage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-conflict"
	writeClaudeUsageFixture(t, home, sessionID, strings.Join([]string{
		`{"type":"assistant","sessionId":"session-conflict","requestId":"req-1","timestamp":"2026-07-23T01:00:00Z","message":{"id":"msg-1","usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"assistant","sessionId":"session-conflict","requestId":"req-1","timestamp":"2026-07-23T01:00:00Z","message":{"id":"msg-1","usage":{"input_tokens":2,"output_tokens":1},"content":"PRIVATE-CONFLICT"}}`,
	}, "\n")+"\n")
	_, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || strings.Contains(err.Error(), "PRIVATE-CONFLICT") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestClaudeUsageSource_RejectsAmbiguousExactSessionFiles(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-ambiguous"
	for _, project := range []string{"project-a", "project-b"} {
		path := filepath.Join(home, ".claude", "projects", project, sessionID+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"type":"result"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestClaudeUsageSource_UsesExactSessionNameInsteadOfGlobPattern(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session[1]"
	writeClaudeUsageFixture(t, home, sessionID,
		`{"type":"result","session_id":"session[1]","subtype":"success","usage":{"input_tokens":1,"output_tokens":1}}`+"\n",
	)
	result, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestClaudeUsageSource_RejectsMalformedJSONWithoutLeakingBody(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	sessionID := "session-malformed"
	writeClaudeUsageFixture(t, home, sessionID, `{"type":"assistant","private":"PRIVATE-MALFORMED"`+"\n")
	_, err := filesystem.NewClaudeUsageSourceForTest(
		func() (string, error) { return home, nil }, 1024*1024, 1024*1024,
	).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || strings.Contains(err.Error(), "PRIVATE-MALFORMED") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestClaudeUsageSource_RejectsUnsafeOrOversizedSourceWithoutLeakingBody(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Run("oversized", func(t *testing.T) {
		home := t.TempDir()
		sessionID := "session-oversized"
		writeClaudeUsageFixture(t, home, sessionID, strings.Repeat("PRIVATE-SENTINEL", 8))
		_, err := filesystem.NewClaudeUsageSourceForTest(
			func() (string, error) { return home, nil }, 16, 1024,
		).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
		if err == nil || strings.Contains(err.Error(), "PRIVATE-SENTINEL") {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		home := t.TempDir()
		project := filepath.Join(home, ".claude", "projects", "project")
		if err := os.MkdirAll(project, 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "outside.jsonl")
		if err := os.WriteFile(target, []byte(`{"type":"result"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		sessionID := "session-symlink"
		if err := os.Symlink(target, filepath.Join(project, sessionID+".jsonl")); err != nil {
			t.Fatal(err)
		}
		_, err := filesystem.NewClaudeUsageSourceForTest(
			func() (string, error) { return home, nil }, 1024, 1024,
		).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
		if err == nil {
			t.Fatal("Load() error = nil, want symlink rejection")
		}
	})
	t.Run("symlinked project directory", func(t *testing.T) {
		home := t.TempDir()
		projects := filepath.Join(home, ".claude", "projects")
		if err := os.MkdirAll(projects, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "project")
		if err := os.MkdirAll(outside, 0o700); err != nil {
			t.Fatal(err)
		}
		sessionID := "session-parent-symlink"
		if err := os.WriteFile(
			filepath.Join(outside, sessionID+".jsonl"), []byte(`{"type":"result"}`), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(projects, "linked-project")); err != nil {
			t.Fatal(err)
		}
		_, err := filesystem.NewClaudeUsageSourceForTest(
			func() (string, error) { return home, nil }, 1024, 1024,
		).Load(context.Background(), application.ClaudeUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
		if err == nil {
			t.Fatal("Load() error = nil, want symlinked parent rejection")
		}
	})
}

func writeClaudeUsageFixture(t *testing.T, home, sessionID, body string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", "project", sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
