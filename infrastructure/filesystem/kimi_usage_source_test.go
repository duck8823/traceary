package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKimiUsageSource_LoadReturnsOnlyBodyFreeUsageRecords(t *testing.T) {
	root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
	wire := strings.Join([]string{
		`{"type":"metadata","protocol_version":"1.4"}`,
		`{"type":"turn.prompt","prompt":[{"type":"text","text":"private prompt"}]}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"think","think":"private thought"}}}`,
		`{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":0,"inputCacheRead":3,"inputCacheCreation":2,"output":5},"usageScope":"turn","time":1784466740000}`,
		`{"type":"turn.prompt","prompt":[{"type":"text","text":"another private prompt"}]}`,
		`{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":7,"output":0},"usageScope":"turn","time":1784466741000}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(wire), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session")
	if err != nil {
		t.Fatal(err)
	}
	if result.LatestTurnOrdinal != 2 || len(result.Samples) != 2 {
		t.Fatalf("result = %+v, want two samples and turn ordinal 2", result)
	}
	first := result.Samples[0]
	if first.Model != "kimi-code/k3" || first.Counters.InputOther == nil || *first.Counters.InputOther != 0 ||
		first.Counters.InputCacheRead == nil || *first.Counters.InputCacheRead != 3 ||
		first.Counters.InputCacheCreation == nil || *first.Counters.InputCacheCreation != 2 ||
		first.Counters.Output == nil || *first.Counters.Output != 5 {
		t.Fatalf("first sample = %+v", first)
	}
	second := result.Samples[1]
	if second.Counters.InputCacheRead != nil || second.Counters.InputCacheCreation != nil {
		t.Fatalf("absent cache counters must remain unavailable: %+v", second.Counters)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") {
		t.Fatalf("result retained private wire bodies: %s", encoded)
	}
}

func TestKimiUsageSource_VersionedFixtureIsPrivateFreeAndReadable(t *testing.T) {
	fixturePath := filepath.Join(
		"..", "..", "presentation", "cli", "testdata", "kimi_usage", "v0.29.0", "main_wire.jsonl",
	)
	wire, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateValue := range []string{"/Users/", "/private/tmp/", "/home/", "duck8823", "@example.com"} {
		if strings.Contains(string(wire), privateValue) {
			t.Fatalf("versioned Kimi usage fixture contains private value %q", privateValue)
		}
	}
	root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), wire, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Samples) != 1 || result.LatestTurnOrdinal != 1 {
		t.Fatalf("fixture result = %+v", result)
	}
}

func TestKimiUsageSource_LoadKeepsIdentityAcrossCopiedSessionRoot(t *testing.T) {
	load := func(t *testing.T) string {
		t.Helper()
		root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
		wire := `{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":1,"output":2},"usageScope":"turn","time":1784466740000}` + "\n"
		if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(wire), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session")
		if err != nil {
			t.Fatal(err)
		}
		return result.Samples[0].RecordID
	}
	if first, second := load(t), load(t); first != second {
		t.Fatalf("copied source identities differ: %q != %q", first, second)
	}
}

func TestKimiUsageSource_LoadRejectsMalformedAuthoritativeUsage(t *testing.T) {
	for name, row := range map[string]string{
		"fractional counter": `{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":1.5},"usageScope":"turn","time":1784466740000}`,
		"negative counter":   `{"type":"usage.record","model":"kimi-code/k3","usage":{"output":-1},"usageScope":"turn","time":1784466740000}`,
		"wrong scope":        `{"type":"usage.record","model":"kimi-code/k3","usage":{"output":1},"usageScope":"session","time":1784466740000}`,
		"missing counters":   `{"type":"usage.record","model":"kimi-code/k3","usage":{},"usageScope":"turn","time":1784466740000}`,
	} {
		t.Run(name, func(t *testing.T) {
			root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
			if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(row+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session"); err == nil {
				t.Fatal("expected malformed usage to fail closed")
			}
		})
	}
}

func TestKimiUsageSource_LoadRejectsIndexPathEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "agents", "main"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "agents", "main", "wire.jsonl"), []byte(`{"type":"usage.record"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(kimiUsageIndexEntry{SessionID: "provider-session", SessionDir: outside})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, kimiUsageSessionIndex), append(entry, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session"); err == nil {
		t.Fatal("expected path escape to fail closed")
	}
}

func writeKimiUsageTestSession(t *testing.T, providerSessionID string) (string, string) {
	t.Helper()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "wd_test", providerSessionID)
	if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(kimiUsageIndexEntry{SessionID: providerSessionID, SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, kimiUsageSessionIndex), append(entry, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, sessionDir
}
