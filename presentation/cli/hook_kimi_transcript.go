package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// Kimi Code stores per-session wire logs under
// $KIMI_CODE_HOME/sessions/wd_*/<session_id>/agents/main/wire.jsonl and
// indexes them in $KIMI_CODE_HOME/session_index.jsonl. The Stop hook payload
// carries no transcript fields, so the extractor resolves the session
// directory through the index and reads the final turn's content.part
// think/text blocks as the assistant transcript.
const (
	kimiCodeHomeEnv     = "KIMI_CODE_HOME"
	kimiDefaultHomeDir  = ".kimi-code"
	kimiSessionIndex    = "session_index.jsonl"
	kimiWireMaxLineSize = 4 * 1024 * 1024
)

// kimiSessionIndexEntry is one row of Kimi Code's session_index.jsonl.
type kimiSessionIndexEntry struct {
	SessionID  string `json:"sessionId"`
	SessionDir string `json:"sessionDir"`
}

// kimiWireRow is the envelope of one wire.jsonl row. Only the
// context.append_loop_event rows wrapping content.part events carry
// assistant content. turnId arrives as a JSON string in 0.27.0 but is kept
// as RawMessage so a numeric shape cannot drop the whole row.
type kimiWireRow struct {
	Type  string `json:"type"`
	Event struct {
		Type string          `json:"type"`
		Turn json.RawMessage `json:"turnId"`
		Part struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Think string `json:"think"`
		} `json:"part"`
	} `json:"event"`
}

// kimiWireTurnID normalizes the turnId raw value (string or number) to a
// plain string for grouping.
func kimiWireTurnID(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	return strings.Trim(trimmed, `"`)
}

// extractKimiTranscript resolves the assistant turn for a Kimi Stop payload
// via the session wire log side channel (host contract:
// docs/hooks/host-contract.json). Missing index entries or wire logs are a
// soft skip — transcript capture is best-effort and must never block the
// host's Stop hook.
func extractKimiTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	blocks, _, ok := extractKimiTranscriptTurn(payload)
	return blocks, ok
}

// extractKimiTranscriptTurn resolves the same assistant turn as
// extractKimiTranscript, additionally returning the wire turn identifier the
// blocks were read from. Kimi's Stop hook fires roughly two dozen times per
// completed turn while the session's wire.jsonl is unchanged; callers use
// the turn identifier to guard against re-recording that unchanged turn
// (#1681) without re-deriving it from the raw event.Turn shape themselves.
func extractKimiTranscriptTurn(payload []byte) ([]apptypes.EventBodyBlock, string, bool) {
	sessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
	if sessionID == "" {
		return nil, "", false
	}
	sessionDir := lookupKimiSessionDir(sessionID)
	if sessionDir == "" {
		return nil, "", false
	}
	sessionDir = containKimiSessionsPath(sessionDir)
	if sessionDir == "" {
		return nil, "", false
	}
	wirePath := containKimiSessionsPath(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"))
	if wirePath == "" {
		return nil, "", false
	}
	return readKimiWireTranscriptBlocks(wirePath)
}

// containKimiSessionsPath confines an index-supplied path to the Kimi home
// sessions root. A tampered index could otherwise point the reader at an
// arbitrary path and have its contents recorded as a transcript. Symlinks
// are resolved on both the root and the candidate before the containment
// check — including the final wire.jsonl, so a symlinked agents/main entry
// cannot escape either. Any failure is a soft skip.
func containKimiSessionsPath(path string) string {
	sessionsRoot := filepath.Join(kimiCodeHome(), "sessions")
	resolvedRoot, err := filepath.EvalSymlinks(sessionsRoot)
	if err != nil {
		slog.Debug("failed to resolve Kimi sessions root", "path", sessionsRoot, "error", err)
		return ""
	}
	resolvedPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		slog.Debug("failed to resolve Kimi session path", "path", path, "error", err)
		return ""
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		slog.Debug("Kimi session path escapes the sessions root", "path", path, "root", resolvedRoot)
		return ""
	}
	return resolvedPath
}

// lookupKimiSessionDir resolves a session_id to its on-disk session
// directory via Kimi Code's session index. The index is append-only, so the
// last matching row wins.
func lookupKimiSessionDir(sessionID string) string {
	indexPath := filepath.Join(kimiCodeHome(), kimiSessionIndex)
	file, err := os.Open(indexPath) // #nosec G304 -- fixed name under the Kimi home
	if err != nil {
		slog.Debug("failed to open Kimi session index", "path", indexPath, "error", err)
		return ""
	}
	defer func() { _ = file.Close() }()

	sessionDir := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), kimiWireMaxLineSize)
	for scanner.Scan() {
		var entry kimiSessionIndexEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.SessionID == sessionID && entry.SessionDir != "" {
			sessionDir = entry.SessionDir
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("failed while scanning Kimi session index", "path", indexPath, "error", err)
		return ""
	}
	return sessionDir
}

// readKimiWireTranscriptBlocks reads the wire log and returns the ordered
// think/text blocks of the LAST turn that produced assistant content, along
// with that turn's identifier. Thinking blocks map to
// EventBodyBlockTypeThinking so downstream consumers can collapse
// reasoning, matching the Claude transcript shape.
func readKimiWireTranscriptBlocks(path string) ([]apptypes.EventBodyBlock, string, bool) {
	file, err := os.Open(path) // #nosec G304 -- path resolved through the Kimi session index
	if err != nil {
		slog.Debug("failed to open Kimi wire log", "path", path, "error", err)
		return nil, "", false
	}
	defer func() { _ = file.Close() }()

	lastTurn := ""
	blocksByTurn := map[string][]apptypes.EventBodyBlock{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), kimiWireMaxLineSize)
	for scanner.Scan() {
		var row kimiWireRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if row.Type != "context.append_loop_event" || row.Event.Type != "content.part" {
			continue
		}
		var block apptypes.EventBodyBlock
		switch row.Event.Part.Type {
		case "think":
			block = apptypes.EventBodyBlock{Type: apptypes.EventBodyBlockTypeThinking, Text: row.Event.Part.Think}
		case "text":
			block = apptypes.EventBodyBlock{Type: apptypes.EventBodyBlockTypeText, Text: row.Event.Part.Text}
		default:
			continue
		}
		turn := kimiWireTurnID(row.Event.Turn)
		blocksByTurn[turn] = append(blocksByTurn[turn], block)
		lastTurn = turn
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("failed while scanning Kimi wire log", "path", path, "error", err)
		return nil, "", false
	}

	blocks := blocksByTurn[lastTurn]
	if len(blocks) == 0 {
		return nil, "", false
	}
	return blocks, lastTurn, true
}

// kimiCodeHome resolves the Kimi Code data home: $KIMI_CODE_HOME when set,
// otherwise ~/.kimi-code.
func kimiCodeHome() string {
	if home := strings.TrimSpace(os.Getenv(kimiCodeHomeEnv)); home != "" {
		return home
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return ""
	}
	return filepath.Join(home, kimiDefaultHomeDir)
}

// kimiTranscriptTurnsStateDir is the hook-state subdirectory holding, per
// session, a marker file naming the wire turn whose transcript was most
// recently recorded. It follows the same fixed-name marker-file idiom as
// the session-end markers in hook_state.go rather than a SQL table, because
// the wire turn identifier is host side-channel state (read from
// wire.jsonl), not a field carried on any hook payload.
const kimiTranscriptTurnsStateDir = "kimi-transcript-turns"

// kimiTranscriptTurnLockTimeout bounds how long a Stop firing waits to
// acquire the per-session turn-marker lock. The Kimi Stop hook's host timeout
// is 5s (integrations/kimi-plugin/kimi.plugin.json) and the critical section
// runs a DB migration check plus an event insert against a store that can be
// tens of GB, so this budget leaves several seconds of headroom inside the
// host deadline for that work to finish even after a fully contended wait.
//
// The wait can genuinely be exhausted: the insert inside the critical section
// is itself bounded by SQLite's busy_timeout, which is also 1000ms
// (infrastructure/sqlite/database.go), so a store under write contention
// holds this lock for about as long as a contending firing is willing to wait
// for it. That is why exhausting the wait is handled as
// errKimiTranscriptTurnLockBusy rather than as a fall-open case.
const kimiTranscriptTurnLockTimeout = 1 * time.Second

// errKimiTranscriptTurnLockBusy reports that another process held the turn
// lock for the whole acquisition budget — meaning it is recording this very
// turn right now. This is distinct from the marker infrastructure being
// unusable (an unresolvable state directory, a failed mkdir), which leaves no
// process able to record and must fall open.
//
// The distinction decides whether a firing records unguarded or skips.
// Skipping is safe here and falling open is not: Kimi redelivers Stop for the
// same turn roughly two dozen times, so a firing that yields is retried
// ~0.14s later by the next redelivery, whereas recording unguarded under
// exactly the contention that produced the timeout reproduces the duplicate
// storm this guard exists to stop (#1681).
var errKimiTranscriptTurnLockBusy = errors.New("kimi transcript turn state lock is held by another firing")

// kimiTranscriptTurnLockRetryDelay is the poll interval TryLockContext uses
// while waiting up to kimiTranscriptTurnLockTimeout for the lock.
const kimiTranscriptTurnLockRetryDelay = 20 * time.Millisecond

// kimiTranscriptBlocksFingerprint computes a stable content fingerprint over
// an assistant turn's extracted blocks. Kimi's Stop hook can re-fire while a
// turn is still streaming: a firing whose wire turn ID matches the marker
// but whose fingerprint differs means the turn grew since the marker was
// written, not that it redelivered, and must still be recorded (#1681).
// json.Marshal over a fixed struct slice shape is deterministic within one
// process/Go version, which is all a same-host comparison needs — this
// fingerprint is never compared across hosts or persisted long-term. On
// error the returned string is always empty; callers must check err rather
// than rely on that as a sentinel.
func kimiTranscriptBlocksFingerprint(blocks []apptypes.EventBodyBlock) (string, error) {
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return "", xerrors.Errorf("failed to encode Kimi transcript blocks for fingerprinting: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// kimiTranscriptTurnMarkerSeparator joins the turn ID and content
// fingerprint fields inside the marker file. Neither field can contain a
// tab (the turn ID is a Kimi-issued opaque identifier normalized to a
// trimmed string; the fingerprint is hex), so a single split is
// unambiguous.
const kimiTranscriptTurnMarkerSeparator = "\t"

// encodeKimiTranscriptTurnMarker renders the marker file content for a
// recorded (turn ID, content fingerprint) pair.
func encodeKimiTranscriptTurnMarker(turnID, fingerprint string) []byte {
	return []byte(turnID + kimiTranscriptTurnMarkerSeparator + fingerprint)
}

// decodeKimiTranscriptTurnMarker parses a marker file's content into its
// (turn ID, fingerprint) pair. It returns ok=false for anything that is not
// exactly two nonempty tab-separated fields — including a marker written by
// the pre-fingerprint format (turn ID only, no separator), which must never
// be mistaken for a match against a real fingerprint.
func decodeKimiTranscriptTurnMarker(data []byte) (turnID, fingerprint string, ok bool) {
	trimmed := strings.TrimSpace(string(data))
	parts := strings.SplitN(trimmed, kimiTranscriptTurnMarkerSeparator, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// kimiTranscriptTurnStatePath returns the marker file path recording the
// last (wire turn ID, content fingerprint) pair whose transcript was
// recorded for a Kimi session.
func kimiTranscriptTurnStatePath(sessionID string) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, kimiTranscriptTurnsStateDir, sanitizeHookStateKey(sessionID)), nil
}

// withKimiTranscriptTurnStateLock runs fn while holding an exclusive
// github.com/gofrs/flock lock scoped to sessionID's turn marker, matching
// the idiom hook_memory_extract_queue.go and hook_archive_auto.go already
// use for hook-state locks. Kimi redelivers Stop for the same turn with
// observed gaps as small as ~0.14s, including effectively concurrent
// firings, so the check ("already recorded?") and the record-then-mark
// sequence inside fn must be one atomic critical section — otherwise two
// racing firings can both observe "not yet recorded" and each write a row
// (#1681).
//
// flock (unlike the prior mkdir-based lock) is released by the kernel when
// the holding process dies for any reason, including SIGKILL — so a host
// kill mid-critical-section (a real risk here: the section runs a DB
// migration check plus an insert against a multi-GB store, inside a 5s host
// hook timeout) cannot leave a stale lock behind. No PID check, TTL, or
// cleanup is needed. Acquisition is bounded by kimiTranscriptTurnLockTimeout
// so a firing never spins past its host budget; exhausting that budget
// returns errKimiTranscriptTurnLockBusy, which the caller treats as "another
// firing owns this turn" and skips. Every other failure means no process can
// take the lock at all, and falls open in the caller (records unguarded).
func withKimiTranscriptTurnStateLock(sessionID string, fn func() error) error {
	path, err := kimiTranscriptTurnStatePath(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf("failed to create Kimi transcript turn state directory: %w", err)
	}
	lock := flock.New(path + ".lock")
	lockCtx, cancel := context.WithTimeout(context.Background(), kimiTranscriptTurnLockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, kimiTranscriptTurnLockRetryDelay)
	if err != nil {
		// TryLockContext surfaces the expired acquisition budget as the
		// context's own error, so that case is the busy one, not a
		// broken-lock one.
		if errors.Is(err, context.DeadlineExceeded) {
			return errKimiTranscriptTurnLockBusy
		}
		return xerrors.Errorf("failed to lock Kimi transcript turn state: %w", err)
	}
	if !locked {
		return errKimiTranscriptTurnLockBusy
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			slog.Debug("failed to release Kimi transcript turn state lock", "session_id", sessionID, "error", unlockErr)
		}
	}()
	return fn()
}

// kimiTranscriptTurnAlreadyRecorded reports whether the (turnID,
// fingerprint) pair was already recorded as sessionID's last transcript
// turn. Kimi's Stop hook fires roughly two dozen times per completed turn,
// and can also re-fire while a turn is still streaming (#1681); this is the
// read half of the guard that collapses unchanged redeliveries to a single
// recorded transcript event while still recording a turn that grew content
// between firings (turnID matches, fingerprint does not). The fingerprint
// keys the exact blocks runHookKimiTranscript passes to the recorder (not a
// second, independent re-extraction), so a "recorded" answer here always
// corresponds to a row that was actually persisted with that content.
// Callers must hold withKimiTranscriptTurnStateLock for correctness under
// concurrent firings.
//
// Any inability to read or parse the marker (including a missing state
// directory, or a marker written by the pre-fingerprint single-field
// format) fails open — returns false, treating the turn as not yet
// recorded. Traceary prefers an occasional duplicate over silently dropping
// a genuinely new or grown turn because of transient marker-file trouble or
// a marker-format change.
func kimiTranscriptTurnAlreadyRecorded(sessionID, turnID, fingerprint string) bool {
	path, err := kimiTranscriptTurnStatePath(sessionID)
	if err != nil {
		slog.Debug("failed to resolve Kimi transcript turn state path", "session_id", sessionID, "error", err)
		return false
	}
	data, err := os.ReadFile(path) // #nosec G304 -- fixed name under the hook state directory
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("failed to read Kimi transcript turn state", "path", path, "error", err)
		}
		return false
	}
	recordedTurnID, recordedFingerprint, ok := decodeKimiTranscriptTurnMarker(data)
	if !ok {
		return false
	}
	return recordedTurnID == turnID && recordedFingerprint == fingerprint
}

// markKimiTranscriptTurnRecorded persists (turnID, fingerprint) as the last
// recorded transcript turn for sessionID. Callers MUST call this only after
// confirming a row was actually persisted (runHookTranscriptWithBlocks
// returned recorded=true) — never merely because the recorder returned a
// nil error, since it fails soft (nil error, nothing written) on several
// unrelated conditions. Marking on a skip would silently drop every later
// redelivery of a turn that was never actually stored (#1681 CRITICAL
// finding). Failures to write the marker file itself are logged and
// swallowed: losing the marker only risks one extra duplicate on the next
// Stop firing, never a lost turn, and a hook must never fail the host's
// turn over housekeeping state. Callers must hold
// withKimiTranscriptTurnStateLock for correctness under concurrent
// firings.
func markKimiTranscriptTurnRecorded(sessionID, turnID, fingerprint string) {
	path, err := kimiTranscriptTurnStatePath(sessionID)
	if err != nil {
		slog.Debug("failed to resolve Kimi transcript turn state path", "session_id", sessionID, "error", err)
		return
	}
	if err := os.WriteFile(path, encodeKimiTranscriptTurnMarker(turnID, fingerprint), 0o600); err != nil {
		slog.Debug("failed to write Kimi transcript turn state", "path", path, "error", err)
	}
}
