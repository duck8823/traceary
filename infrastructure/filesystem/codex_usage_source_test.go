package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestCodexUsageSource_LoadsVerifiedRolloutAndExecUsageWithoutBodies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "019f8c18-6729-7bd1-bcfa-2e330f334de0"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cli_version":"0.145.0","private_prompt":"must not escape"}}`,
		`{"timestamp":"2026-07-23T01:00:01Z","type":"turn_context","payload":{"model":"gpt-5.6-sol","user_instructions":"private"}}`,
		`{"timestamp":"2026-07-23T01:00:02Z","type":"response_item","payload":{"type":"message","content":"private response"}}`,
		`{"timestamp":"2026-07-23T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":120,"cached_input_tokens":80,"cache_write_input_tokens":0,"output_tokens":7,"reasoning_output_tokens":3,"total_tokens":127}}}}`,
		`{"timestamp":"2026-07-23T01:00:04Z","type":"event_msg","payload":{"type":"context_compacted","private_summary":"ignored"}}`,
		`{"timestamp":"2026-07-23T01:00:04.5Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":0,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":24476}}}}`,
		`{"type":"turn.completed","model":"gpt-5.6-terra","usage":{"input_tokens":21,"cached_input_tokens":0,"output_tokens":5,"total_tokens":26},"private_response":"ignored"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	fallback := time.Date(2026, 7, 23, 1, 0, 5, 0, time.UTC)
	if err := os.Chtimes(path, fallback, fallback); err != nil {
		t.Fatal(err)
	}
	source := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024*1024, 1024*1024)
	result, err := source.Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 2 {
		t.Fatalf("len(Samples) = %d, want 2", len(result.Samples))
	}
	rollout := result.Samples[0]
	if rollout.SourceName != "rollout_jsonl" || rollout.SourceVersion != "0.145.0" || rollout.Model != "gpt-5.6-sol" || !rollout.ObservedAt.Equal(time.Date(2026, 7, 23, 1, 0, 3, 0, time.UTC)) {
		t.Fatalf("rollout sample = %+v", rollout)
	}
	if value := rollout.Counters.CacheWriteInputTokens; value == nil || *value != 0 {
		t.Fatalf("cache write = %v, want known zero", value)
	}
	direct := result.Samples[1]
	if direct.SourceName != "exec_jsonl" || direct.Model != "gpt-5.6-terra" || !direct.ObservedAt.Equal(fallback) {
		t.Fatalf("direct sample = %+v", direct)
	}
	if direct.Counters.ReasoningOutputTokens != nil || direct.Counters.CacheWriteInputTokens != nil {
		t.Fatalf("direct absent fields became values: %+v", direct.Counters)
	}
	for _, sample := range result.Samples {
		if strings.Contains(sample.RecordID, "private") || strings.Contains(sample.Model, "private") {
			t.Fatalf("private fixture field escaped: %+v", sample)
		}
	}
}

func TestCodexUsageSource_MissingSessionIsSupportedEmptyResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	source := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024, 1024)
	result, err := source.Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID("missing-session")})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 0 {
		t.Fatalf("Samples = %+v", result.Samples)
	}
}

func TestCodexUsageSource_RejectsOversizedMatchedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "oversized-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 65)), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 64, 1024)
	_, err := source.Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load() error = %v", err)
	}
}
