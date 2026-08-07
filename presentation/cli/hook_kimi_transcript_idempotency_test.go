package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gofrs/flock"

	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_HookKimiStop_TranscriptTurnIdempotency reproduces and fixes
// #1681: Kimi Code fires Stop roughly two dozen times per completed turn
// while the session's wire.jsonl is unchanged. Extraction itself was always
// correct (it resolves only the final turn's blocks), but nothing guarded
// against re-recording that unchanged turn on every redelivery, measured at
// 23.6x duplication on the live store. These cases pin the fix: a stable
// (session, wire turn) pair collapses to one recorded transcript event, and
// an advancing turn still produces one distinct event per turn.
func TestRootCLI_HookKimiStop_TranscriptTurnIdempotency(t *testing.T) {
	const sessionID = "session_00000000-0000-4000-8000-000000000001"

	t.Run("repeated Stop against an unadvanced wire log records exactly one transcript event", func(t *testing.T) {
		t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-unadvanced")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"first reply"}}}`,
		})

		eventStub := &eventUsecaseStub{}
		sessionStub := &sessionUsecaseStub{}
		payload := readKimiFixture(t, "stop.json")

		for fire := 0; fire < 3; fire++ {
			stdout, _, _ := runKimiHook(t, "stop", payload, eventStub, sessionStub)
			if stdout != "" {
				t.Fatalf("Stop fire %d output = %q, want empty passive-hook output", fire, stdout)
			}
		}

		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log calls = %d, want 1 (unadvanced turn must collapse to one event)", got)
		}
		if got, want := eventStub.logCalls[0].kind, types.EventKindTranscript; got != want {
			t.Fatalf("kind = %q, want %q", got, want)
		}
		if !strings.Contains(eventStub.logCalls[0].message, "first reply") {
			t.Fatalf("transcript body = %q, want it to contain the wire log text", eventStub.logCalls[0].message)
		}
	})

	t.Run("an advancing turn between firings records one event per turn with distinct bodies", func(t *testing.T) {
		t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-advancing")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"first reply"}}}`,
		})

		eventStub := &eventUsecaseStub{}
		sessionStub := &sessionUsecaseStub{}
		payload := readKimiFixture(t, "stop.json")

		// Re-fire against the still-unadvanced turn once first, mirroring the
		// live redelivery pattern, then advance the wire log to a new turn.
		runKimiHook(t, "stop", payload, eventStub, sessionStub)
		runKimiHook(t, "stop", payload, eventStub, sessionStub)
		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log calls after two same-turn fires = %d, want 1", got)
		}

		appendKimiWireRow(t, homeDir, sessionID,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"1","part":{"type":"text","text":"second reply"}}}`,
		)
		runKimiHook(t, "stop", payload, eventStub, sessionStub)

		if got := len(eventStub.logCalls); got != 2 {
			t.Fatalf("transcript log calls after the turn advanced = %d, want 2", got)
		}
		if !strings.Contains(eventStub.logCalls[0].message, "first reply") {
			t.Fatalf("first transcript body = %q, want it to contain the first turn's text", eventStub.logCalls[0].message)
		}
		if !strings.Contains(eventStub.logCalls[1].message, "second reply") {
			t.Fatalf("second transcript body = %q, want it to contain the second turn's text", eventStub.logCalls[1].message)
		}
		if eventStub.logCalls[0].message == eventStub.logCalls[1].message {
			t.Fatalf("distinct turns recorded identical bodies: %q", eventStub.logCalls[0].message)
		}
	})

	t.Run("a turn that grows content between firings records a second event containing the new content", func(t *testing.T) {
		// Kimi's Stop hook is not a completed-turn boundary: it can re-fire
		// while a turn is still streaming, so the SAME wire turn ID can gain
		// blocks between firings (measured live: 2,053 of 52,475 consecutive
		// same-session pairs grew, 153 of those gained the turn's only
		// "text" block). Every other fixture in this suite seeds a turn
		// that is already complete before the first Stop firing, which
		// bakes the wrong "turn ID alone means unchanged" premise into the
		// old guard. This reproduces the growth case directly: the first
		// firing sees only a thinking block, then a text block is appended
		// under the SAME turnId before the second firing.
		t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-growing")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"think","think":"reasoning about the reply"}}}`,
		})

		eventStub := &eventUsecaseStub{}
		sessionStub := &sessionUsecaseStub{}
		payload := readKimiFixture(t, "stop.json")

		runKimiHook(t, "stop", payload, eventStub, sessionStub)
		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log calls after the first firing = %d, want 1", got)
		}
		if strings.Contains(eventStub.logCalls[0].message, "final answer text") {
			t.Fatalf("first transcript body = %q, want it to NOT yet contain the not-yet-appended text block", eventStub.logCalls[0].message)
		}

		// Same turnId as above: the wire turn ID does not advance, only its
		// content does.
		appendKimiWireRow(t, homeDir, sessionID,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"final answer text"}}}`,
		)
		runKimiHook(t, "stop", payload, eventStub, sessionStub)

		if got := len(eventStub.logCalls); got != 2 {
			t.Fatalf("transcript log calls after the turn grew = %d, want 2 (turn ID unchanged but content grew must still record)", got)
		}
		if !strings.Contains(eventStub.logCalls[1].message, "final answer text") {
			t.Fatalf("second transcript body = %q, want it to contain the newly appended text block", eventStub.logCalls[1].message)
		}
	})

	t.Run("concurrent Stop firings for the same turn record exactly one transcript event", func(t *testing.T) {
		// Kimi's live redeliveries land as little as ~0.14s apart, including
		// effectively concurrent firings (#1681). Fire the Stop hook from
		// several goroutines at once against the same (session, turn) pair
		// to prove withKimiTranscriptTurnStateLock serializes the
		// check-then-record-then-mark sequence instead of letting two
		// racing firings both observe "not yet recorded" and both write a
		// row.
		t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-concurrent")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"racing reply"}}}`,
		})

		eventStub := &eventUsecaseStub{}
		payload := readKimiFixture(t, "stop.json")

		const invocations = 8
		start := make(chan struct{})
		errs := make(chan error, invocations)
		var wg sync.WaitGroup
		for i := 0; i < invocations; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				cmd := newTestRootCLI(
					cli.WithStoreManagement(&storeManagementUsecaseStub{}),
					cli.WithEvent(eventStub),
					cli.WithSession(&sessionUsecaseStub{}),
				).Command()
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})
				cmd.SetIn(strings.NewReader(payload))
				cmd.SetArgs([]string{"hook", "kimi", "stop"})
				errs <- cmd.Execute()
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent Stop firing error = %v", err)
			}
		}

		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log calls = %d, want 1 (concurrent firings for the same turn must collapse to one event)", got)
		}
		if !strings.Contains(eventStub.logCalls[0].message, "racing reply") {
			t.Fatalf("transcript body = %q, want it to contain the wire log text", eventStub.logCalls[0].message)
		}
	})

	t.Run("idempotency state unavailable falls open and still records without failing the hook", func(t *testing.T) {
		// Pre-create a plain file at the exact path
		// withKimiTranscriptTurnStateLock needs as a directory
		// (<state-dir>/kimi-transcript-turns), so only the turn-marker
		// guard's own os.MkdirAll fails (ENOTDIR). The rest of the state
		// dir stays a real, writable directory so sibling hook-state
		// subsystems (e.g. active-subagent-state, consulted while
		// resolving the session ID) are unaffected. The guard must fail
		// open (treat the turn as unrecorded) rather than block the
		// transcript, and the command must still exit clean.
		stateDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(stateDir, "kimi-transcript-turns"), []byte("blocking file"), 0o600); err != nil {
			t.Fatalf("seed blocking state path: %v", err)
		}
		t.Setenv("TRACEARY_HOOK_STATE_DIR", stateDir)
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-state-unavailable")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"state unavailable reply"}}}`,
		})

		eventStub := &eventUsecaseStub{}
		sessionStub := &sessionUsecaseStub{}

		// runKimiHook itself asserts Execute() returns nil, i.e. the hook
		// command exits 0 despite the marker state being unreachable.
		stdout, _, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), eventStub, sessionStub)
		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log calls = %d, want 1 (fail-open must still record)", got)
		}
		if !strings.Contains(eventStub.logCalls[0].message, "state unavailable reply") {
			t.Fatalf("transcript body = %q, want it to contain the wire log text", eventStub.logCalls[0].message)
		}
	})

	t.Run("event store error exits 0 without crashing the hook, and a later healthy firing still records", func(t *testing.T) {
		t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-store-error")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"db error reply"}}}`,
		})

		eventStub := &eventUsecaseStub{logErr: errors.New("simulated event store failure")}
		sessionStub := &sessionUsecaseStub{}
		payload := readKimiFixture(t, "stop.json")

		// A hook must never fail the host's turn. runKimiHook asserts
		// Execute() returns nil; the outer hook runtime swallows the
		// propagated event-store error and exits 0.
		stdout, _, _ := runKimiHook(t, "stop", payload, eventStub, sessionStub)
		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log attempts = %d, want 1", got)
		}

		// This pins the invariant that the guard only marks the turn as
		// recorded AFTER a successful record: a failing firing must leave
		// the marker unwritten so the SAME turn is retried on the next
		// firing, rather than being dropped forever because a refactor
		// moved the mark ahead of the error check. Clear the simulated
		// failure and fire again for the identical (session, turn) pair.
		eventStub.logErr = nil
		stdout, _, _ = runKimiHook(t, "stop", payload, eventStub, sessionStub)
		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if got := len(eventStub.logCalls); got != 2 {
			t.Fatalf("transcript log attempts after the retry = %d, want 2 (a failed record must leave the turn unmarked and retried)", got)
		}
		if !strings.Contains(eventStub.logCalls[1].message, "db error reply") {
			t.Fatalf("retried transcript body = %q, want it to contain the wire log text", eventStub.logCalls[1].message)
		}
	})

	t.Run("lock acquisition timeout falls open and still records without failing the hook", func(t *testing.T) {
		// The turn-marker lock now uses github.com/gofrs/flock (crash-safe
		// release on process death, unlike the former mkdir lock) instead
		// of a directory-mkdir spin. Forcing a genuine *lock-acquisition
		// timeout* — as opposed to the state-dir-unavailable path covered
		// above — requires contending on the exact lock file the hook will
		// try to acquire. flock(2) locks are scoped to the open file
		// description, not the process, so a second, independent
		// *flock.Flock opened on the same path from this same test process
		// blocks the hook's own TryLockContext exactly as a second process
		// would; gofrs/flock's own test suite relies on the same property.
		// That lock file's path is derived deterministically from
		// TRACEARY_HOOK_STATE_DIR plus the fixed "kimi-transcript-turns"
		// subdirectory plus the session ID (already unadorned enough that
		// sanitizeHookStateKey leaves it unchanged), mirroring the
		// state-dir-unavailable test above.
		stateDir := t.TempDir()
		t.Setenv("TRACEARY_HOOK_STATE_DIR", stateDir)
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-turn-idempotency-lock-timeout")
		t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
		homeDir := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		seedKimiSession(t, homeDir, sessionID, []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"lock contention reply"}}}`,
		})

		markerDir := filepath.Join(stateDir, "kimi-transcript-turns")
		if err := os.MkdirAll(markerDir, 0o755); err != nil {
			t.Fatalf("mkdir marker dir: %v", err)
		}
		externalLock := flock.New(filepath.Join(markerDir, sessionID+".lock"))
		locked, err := externalLock.TryLock()
		if err != nil || !locked {
			t.Fatalf("acquire external contending lock: locked=%v err=%v", locked, err)
		}
		t.Cleanup(func() { _ = externalLock.Unlock() })

		eventStub := &eventUsecaseStub{}
		sessionStub := &sessionUsecaseStub{}

		stdout, _, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), eventStub, sessionStub)
		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if got := len(eventStub.logCalls); got != 1 {
			t.Fatalf("transcript log calls = %d, want 1 (lock-acquisition timeout must still fall open and record)", got)
		}
		if !strings.Contains(eventStub.logCalls[0].message, "lock contention reply") {
			t.Fatalf("transcript body = %q, want it to contain the wire log text", eventStub.logCalls[0].message)
		}
	})
}

// appendKimiWireRow appends one more row to an already-seeded session's
// wire.jsonl, simulating the host's turn advancing between Stop firings.
func appendKimiWireRow(t *testing.T, homeDir, sessionID, row string) {
	t.Helper()
	wirePath := filepath.Join(homeDir, ".kimi-code", "sessions", "wd_probe_000000000000", sessionID, "agents", "main", "wire.jsonl")
	file, err := os.OpenFile(wirePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open wire log for append: %v", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(row + "\n"); err != nil {
		t.Fatalf("append wire log row: %v", err)
	}
}
