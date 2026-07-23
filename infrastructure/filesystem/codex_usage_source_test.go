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

func TestCodexUsageSource_LoadsFinalCumulativeTurnDeltasWithoutBodies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "019f8c18-6729-7bd1-bcfa-2e330f334de0"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cli_version":"0.145.0","private_prompt":"must not escape"}}`,
		`{"timestamp":"2026-07-23T01:00:00.5Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":80,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":110},"last_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}}`,
		`{"timestamp":"2026-07-23T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6-sol","user_instructions":"private"}}`,
		`{"timestamp":"2026-07-23T01:00:02Z","type":"response_item","payload":{"type":"message","content":"private response"}}`,
		`{"timestamp":"2026-07-23T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":130,"cached_input_tokens":100,"cache_write_input_tokens":0,"output_tokens":15,"reasoning_output_tokens":3,"total_tokens":145},"last_token_usage":{"input_tokens":30,"output_tokens":5,"total_tokens":35}}}}`,
		`{"timestamp":"2026-07-23T01:00:04Z","type":"event_msg","payload":{"type":"context_compacted","private_summary":"ignored"}}`,
		`{"timestamp":"2026-07-23T01:00:04.2Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6-sol"}}`,
		`{"timestamp":"2026-07-23T01:00:04.5Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":120,"cache_write_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":4,"total_tokens":170},"last_token_usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}}}}`,
		`{"timestamp":"2026-07-23T01:00:05Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-07-23T01:00:06Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-5.6-terra"}}`,
		`{"timestamp":"2026-07-23T01:00:07Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":170,"cached_input_tokens":130,"cache_write_input_tokens":0,"output_tokens":25,"reasoning_output_tokens":6,"total_tokens":195},"last_token_usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}}}}`,
		`{"timestamp":"2026-07-23T01:00:08Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-2","reason":"private"}}`,
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
	if rollout.SourceName != "rollout_jsonl" || rollout.SourceVersion != "0.145.0" || rollout.Model != "gpt-5.6-sol" || !rollout.ObservedAt.Equal(time.Date(2026, 7, 23, 1, 0, 5, 0, time.UTC)) || rollout.TerminalCode != types.UsageTerminalSuccess || !rollout.Available {
		t.Fatalf("rollout sample = %+v", rollout)
	}
	if value := rollout.Counters.CacheWriteInputTokens; value == nil || *value != 0 {
		t.Fatalf("cache write = %v, want known zero", value)
	}
	if value := rollout.Counters.InputTokens; value == nil || *value != 50 {
		t.Fatalf("rollout input delta = %v, want 50", value)
	}
	aborted := result.Samples[1]
	if aborted.Model != "gpt-5.6-terra" || aborted.TerminalCode != types.UsageTerminalAbortedStream || !aborted.Available {
		t.Fatalf("aborted sample = %+v", aborted)
	}
	if value := aborted.Counters.TotalTokens; value == nil || *value != 25 {
		t.Fatalf("aborted total delta = %v, want 25", value)
	}
	for _, sample := range result.Samples {
		if strings.Contains(sample.RecordID, "private") || strings.Contains(sample.Model, "private") {
			t.Fatalf("private fixture field escaped: %+v", sample)
		}
	}
}

func TestCodexUsageSource_CounterRegressionBecomesUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "regression-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":5,"cache_write_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":1,"total_tokens":12}}}}`,
		`{"timestamp":"2026-07-23T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-regression","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":9,"cached_input_tokens":5,"cache_write_input_tokens":0,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":12}}}}`,
		`{"timestamp":"2026-07-23T01:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-regression"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024*1024, 1024*1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 1 || result.Samples[0].Available || !result.BoundaryObserved {
		t.Fatalf("result = %+v", result)
	}
}

func TestCodexUsageSource_RecoveredIntermediateRegressionRemainsUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "recovered-regression-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":50,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":2,"total_tokens":110}}}}`,
		`{"timestamp":"2026-07-23T01:00:01Z","type":"turn_context","payload":{"turn_id":"turn-regression","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":90,"cached_input_tokens":45,"cache_write_input_tokens":0,"output_tokens":9,"reasoning_output_tokens":2,"total_tokens":99}}}}`,
		`{"timestamp":"2026-07-23T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"cached_input_tokens":60,"cache_write_input_tokens":0,"output_tokens":12,"reasoning_output_tokens":3,"total_tokens":132}}}}`,
		`{"timestamp":"2026-07-23T01:00:04Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-regression"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024*1024, 1024*1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 1 || result.Samples[0].Available || !result.BoundaryObserved {
		t.Fatalf("result = %+v", result)
	}
}

func TestCodexUsageSource_MissingTurnInvalidatesFollowingTurnBaseline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "missing-gap-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":5,"cache_write_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":1,"total_tokens":12}}}}`,
		`{"timestamp":"2026-07-23T01:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-07-23T01:00:03Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:04Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2"}}`,
		`{"timestamp":"2026-07-23T01:00:05Z","type":"turn_context","payload":{"turn_id":"turn-3","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":15,"cache_write_input_tokens":0,"output_tokens":6,"reasoning_output_tokens":3,"total_tokens":36}}}}`,
		`{"timestamp":"2026-07-23T01:00:07Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-3"}}`,
		`{"timestamp":"2026-07-23T01:00:08Z","type":"turn_context","payload":{"turn_id":"turn-4","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:09Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":40,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":8,"reasoning_output_tokens":4,"total_tokens":48}}}}`,
		`{"timestamp":"2026-07-23T01:00:10Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-4"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024*1024, 1024*1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 4 || !result.Samples[0].Available || result.Samples[1].Available || result.Samples[2].Available || !result.Samples[3].Available {
		t.Fatalf("result = %+v", result)
	}
}

func TestCodexUsageSource_LaterTerminalWithoutSnapshotIsUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "later-missing-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"turn_context","payload":{"turn_id":"turn-known","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":5,"cache_write_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":1,"total_tokens":12}}}}`,
		`{"timestamp":"2026-07-23T01:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-known"}}`,
		`{"timestamp":"2026-07-23T01:00:03Z","type":"turn_context","payload":{"turn_id":"turn-missing","model":"gpt"}}`,
		`{"timestamp":"2026-07-23T01:00:04Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-missing"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024*1024, 1024*1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Samples) != 2 || !result.Samples[0].Available || result.Samples[1].Available || !result.BoundaryObserved {
		t.Fatalf("result = %+v", result)
	}
	if result.Samples[1].RecordID != "rollout:"+sessionID+":turn-missing" {
		t.Fatalf("missing record ID = %q", result.Samples[1].RecordID)
	}
}

func TestCodexUsageSource_RejectsMalformedAuthoritativeUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "malformed-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":"private-invalid"}}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024, 1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || !strings.Contains(err.Error(), "invalid Codex token_count usage") || strings.Contains(err.Error(), "private-invalid") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestCodexUsageSource_RejectsMalformedTimestampWithoutEchoingValue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "malformed-timestamp-session"
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "23", "rollout-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	privateValue := "private-invalid-timestamp"
	fixture := strings.Join([]string{
		`{"timestamp":"2026-07-23T01:00:00Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt"}}`,
		`{"timestamp":"` + privateValue + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":5,"cache_write_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":1,"total_tokens":12}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024*1024, 1024*1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || !strings.Contains(err.Error(), "invalid Codex token_count timestamp") || strings.Contains(err.Error(), privateValue) {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestCodexUsageSource_RejectsSymlinkedDateComponent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	sessionID := "symlink-session"
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions", "2026"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "07", "23"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "07", "23", "rollout-"+sessionID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".codex", "sessions", "2026", "07-link")); err != nil {
		t.Fatal(err)
	}
	// Match the required fixed depth through a symlinked year component.
	if err := os.Remove(filepath.Join(home, ".codex", "sessions", "2026", "07-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".codex", "sessions", "2026")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".codex", "sessions", "2026")); err != nil {
		t.Fatal(err)
	}
	_, err := filesystem.NewCodexUsageSourceForTest(func() (string, error) { return home, nil }, 1024, 1024).Load(context.Background(), application.CodexUsageLoadCriteria{SessionID: types.SessionID(sessionID)})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Load() error = %v", err)
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
