package cli

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// kimiTranscriptTurnLockRetries and kimiTranscriptTurnLockDelay bound the
// mkdir-lock spin below, matching the retry budget
// withHookActiveSubagentStateLock (hook_state.go) already uses for the same
// kind of tiny, short-held hook-state lock.
const (
	kimiTranscriptTurnLockRetries = 100
	kimiTranscriptTurnLockDelay   = 10 * time.Millisecond
)

// kimiTranscriptTurnStatePath returns the marker file path recording the
// last wire turn ID whose transcript was recorded for a Kimi session.
func kimiTranscriptTurnStatePath(sessionID string) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, kimiTranscriptTurnsStateDir, sanitizeHookStateKey(sessionID)), nil
}

// withKimiTranscriptTurnStateLock runs fn while holding an exclusive,
// directory-mkdir-based lock scoped to sessionID's turn marker. Kimi
// redelivers Stop for the same completed turn with observed gaps as small
// as ~0.14s, including effectively concurrent firings, so the check
// ("already recorded?") and the record-then-mark sequence inside fn must be
// one atomic critical section — otherwise two racing firings can both
// observe "not yet recorded" and each write a row (#1681).
func withKimiTranscriptTurnStateLock(sessionID string, fn func() error) error {
	path, err := kimiTranscriptTurnStatePath(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf("failed to create Kimi transcript turn state directory: %w", err)
	}
	lockPath := path + ".lock"
	for attempt := 0; ; attempt++ {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			defer func() { _ = os.Remove(lockPath) }()
			return fn()
		}
		if !os.IsExist(err) {
			return xerrors.Errorf("failed to lock Kimi transcript turn state: %w", err)
		}
		if attempt >= kimiTranscriptTurnLockRetries {
			return xerrors.Errorf("failed to lock Kimi transcript turn state: timed out")
		}
		time.Sleep(kimiTranscriptTurnLockDelay)
	}
}

// kimiTranscriptTurnAlreadyRecorded reports whether turnID was already
// recorded as sessionID's last transcript turn. Kimi's Stop hook fires
// roughly two dozen times per completed turn while wire.jsonl is
// unchanged (#1681); this is the read half of the guard that collapses
// those redeliveries to a single recorded transcript event. Callers must
// hold withKimiTranscriptTurnStateLock for correctness under concurrent
// firings.
//
// Any inability to read the marker (including a missing state directory)
// fails open — returns false, treating the turn as not yet recorded.
// Traceary prefers an occasional duplicate over silently dropping a
// genuinely new turn because of transient marker-file trouble.
func kimiTranscriptTurnAlreadyRecorded(sessionID, turnID string) bool {
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
	return strings.TrimSpace(string(data)) == turnID
}

// markKimiTranscriptTurnRecorded persists turnID as the last recorded
// transcript turn for sessionID. Failures are logged and swallowed:
// losing the marker only risks one extra duplicate on the next Stop
// firing, never a lost turn, and a hook must never fail the host's turn
// over housekeeping state. Callers must hold
// withKimiTranscriptTurnStateLock for correctness under concurrent
// firings.
func markKimiTranscriptTurnRecorded(sessionID, turnID string) {
	path, err := kimiTranscriptTurnStatePath(sessionID)
	if err != nil {
		slog.Debug("failed to resolve Kimi transcript turn state path", "session_id", sessionID, "error", err)
		return
	}
	if err := os.WriteFile(path, []byte(turnID), 0o600); err != nil {
		slog.Debug("failed to write Kimi transcript turn state", "path", path, "error", err)
	}
}
