//go:build unix

package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
	if result.LatestTurnOrdinal != 2 || len(result.Samples) != 2 || result.SkippedNonTurnScope != 0 {
		t.Fatalf("result = %+v, want two samples, turn ordinal 2, and no skipped scopes", result)
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
	if len(result.Samples) != 1 || result.LatestTurnOrdinal != 1 || result.SkippedNonTurnScope != 0 {
		t.Fatalf("fixture result = %+v", result)
	}
}

func TestKimiUsageSource_LoadSkipsNonTurnScopeAndKeepsTurnSamples(t *testing.T) {
	root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
	wire := strings.Join([]string{
		`{"type":"turn.prompt","prompt":[{"type":"text","text":"private prompt"}]}`,
		`{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":1,"output":2},"usageScope":"turn","time":1784466740000}`,
		`{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":9,"output":8},"usageScope":"session","time":1784466740500}`,
		`{"type":"turn.prompt","prompt":[{"type":"text","text":"another private prompt"}]}`,
		`{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":3,"output":4},"usageScope":"turn","time":1784466741000}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(wire), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session")
	if err != nil {
		t.Fatal(err)
	}
	if result.LatestTurnOrdinal != 2 || len(result.Samples) != 2 || result.SkippedNonTurnScope != 1 {
		t.Fatalf("result = %+v, want two turn samples, one skipped session scope, turn ordinal 2", result)
	}
	if result.Samples[0].Counters.InputOther == nil || *result.Samples[0].Counters.InputOther != 1 ||
		result.Samples[1].Counters.InputOther == nil || *result.Samples[1].Counters.InputOther != 3 {
		t.Fatalf("turn samples = %+v", result.Samples)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") {
		t.Fatalf("result retained private wire bodies: %s", encoded)
	}
}

func TestKimiUsageSource_LoadSucceedsWhenOnlyNonTurnScopeRecordsExist(t *testing.T) {
	root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
	row := `{"type":"usage.record","model":"kimi-code/k3","usage":{"output":1},"usageScope":"session","time":1784466740000}`
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(row+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Samples) != 0 || result.SkippedNonTurnScope != 1 {
		t.Fatalf("result = %+v, want empty samples and one skipped session scope", result)
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
		"missing counters":   `{"type":"usage.record","model":"kimi-code/k3","usage":{},"usageScope":"turn","time":1784466740000}`,
		"missing time":       `{"type":"usage.record","model":"kimi-code/k3","usage":{"output":1},"usageScope":"turn"}`,
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

func TestKimiUsageSource_LoadRejectsRelativeSessionDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(kimiUsageIndexEntry{
		SessionID:  "provider-session",
		SessionDir: filepath.Join("sessions", "wd_test", "provider-session"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, kimiUsageSessionIndex), append(entry, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session"); err == nil {
		t.Fatal("expected relative session directory to fail closed")
	}
}

func TestKimiUsageSource_LoadRejectsSymlinkEscapes(t *testing.T) {
	t.Run("session index", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		outsideIndex := filepath.Join(t.TempDir(), "session_index.jsonl")
		if err := os.WriteFile(outsideIndex, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideIndex, filepath.Join(root, kimiUsageSessionIndex)); err != nil {
			t.Fatal(err)
		}
		if _, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session"); err == nil {
			t.Fatal("expected index symlink escape to fail closed")
		}
	})

	for _, target := range []string{"wire", "parent"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			sessionDir := filepath.Join(root, "sessions", "wd_test", "provider-session")
			if err := os.MkdirAll(filepath.Dir(sessionDir), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if target == "wire" {
				if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o700); err != nil {
					t.Fatal(err)
				}
				outsideWire := filepath.Join(outside, "wire.jsonl")
				if err := os.WriteFile(outsideWire, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideWire, filepath.Join(sessionDir, "agents", "main", "wire.jsonl")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Join(outside, "agents", "main"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outside, "agents", "main", "wire.jsonl"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, sessionDir); err != nil {
					t.Fatal(err)
				}
			}
			entry, err := json.Marshal(kimiUsageIndexEntry{
				SessionID:  "provider-session",
				SessionDir: sessionDir,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, kimiUsageSessionIndex), append(entry, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := newKimiUsageSourceWithRoot(root).Load(context.Background(), "provider-session"); err == nil {
				t.Fatalf("expected %s symlink escape to fail closed", target)
			}
		})
	}
}

func TestKimiUsageSource_LoadRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, target := range []string{"session index", "wire"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			if target == "session index" {
				if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(filepath.Join(root, kimiUsageSessionIndex), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				sessionDir := filepath.Join(root, "sessions", "wd_test", "provider-session")
				if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o700); err != nil {
					t.Fatal(err)
				}
				entry, err := json.Marshal(kimiUsageIndexEntry{
					SessionID:  "provider-session",
					SessionDir: sessionDir,
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, kimiUsageSessionIndex), append(entry, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := newKimiUsageSourceWithRoot(root).Load(ctx, "provider-session"); err == nil {
				t.Fatalf("expected %s FIFO to fail closed", target)
			}
			if ctx.Err() != nil {
				t.Fatalf("%s FIFO blocked until context timeout", target)
			}
		})
	}
}

func TestKimiUsageSource_LoadRejectsOversizedSourceWithoutLeakingBody(t *testing.T) {
	t.Run("wire", func(t *testing.T) {
		root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
		var body strings.Builder
		for range 64 {
			body.WriteString(`{"type":"turn.prompt","padding":"PRIVATE-SENTINEL"}` + "\n")
		}
		if err := os.WriteFile(
			filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(body.String()), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		indexInfo, err := os.Stat(filepath.Join(root, kimiUsageSessionIndex))
		if err != nil {
			t.Fatal(err)
		}
		// The line cap stays above every individual line so only the
		// open-time source size check can reject this input.
		_, err = newKimiUsageSourceForBoundTest(root, indexInfo.Size(), 1<<20).
			Load(context.Background(), "provider-session")
		if err == nil || !strings.Contains(err.Error(), "failed to open Kimi usage wire") ||
			strings.Contains(err.Error(), "PRIVATE-SENTINEL") {
			t.Fatalf("Load() error = %v, want open-time oversized wire rejection without body leak", err)
		}
	})
	t.Run("session index", func(t *testing.T) {
		root, _ := writeKimiUsageTestSession(t, "provider-session")
		var body strings.Builder
		for range 64 {
			body.WriteString(`{"sessionId":"other-session","sessionDir":"PRIVATE-SENTINEL"}` + "\n")
		}
		if err := os.WriteFile(
			filepath.Join(root, kimiUsageSessionIndex), []byte(body.String()), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		_, err := newKimiUsageSourceForBoundTest(root, 16, 1<<20).
			Load(context.Background(), "provider-session")
		if err == nil || !strings.Contains(err.Error(), "failed to open Kimi usage session index") ||
			strings.Contains(err.Error(), "PRIVATE-SENTINEL") {
			t.Fatalf("Load() error = %v, want open-time oversized index rejection without body leak", err)
		}
	})
}

func TestKimiUsageSource_LoadRejectsOversizedLine(t *testing.T) {
	root, sessionDir := writeKimiUsageTestSession(t, "provider-session")
	line := `{"type":"turn.prompt","padding":"` + strings.Repeat("x", 128*1024) + `"}`
	if err := os.WriteFile(
		filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(line+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err := newKimiUsageSourceForBoundTest(root, 1<<20, 1024).
		Load(context.Background(), "provider-session")
	if err == nil {
		t.Fatal("Load() error = nil, want oversized line rejection")
	}
}

func newKimiUsageSourceForBoundTest(root string, maxSourceSize int64, maxLineBytes int) *kimiUsageSource {
	return &kimiUsageSource{
		root:          func() (string, error) { return root, nil },
		maxSourceSize: maxSourceSize,
		maxLineBytes:  maxLineBytes,
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
