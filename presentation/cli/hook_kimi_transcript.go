package cli

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
	sessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
	if sessionID == "" {
		return nil, false
	}
	sessionDir := lookupKimiSessionDir(sessionID)
	if sessionDir == "" {
		return nil, false
	}
	sessionDir = containKimiSessionDir(sessionDir)
	if sessionDir == "" {
		return nil, false
	}
	return readKimiWireTranscriptBlocks(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"))
}

// containKimiSessionDir confines the index-supplied session directory to the
// Kimi home sessions root. A tampered index could otherwise point the reader
// at an arbitrary path and have its contents recorded as a transcript.
// Symlinks are resolved on both sides before the containment check; any
// failure is a soft skip.
func containKimiSessionDir(sessionDir string) string {
	sessionsRoot := filepath.Join(kimiCodeHome(), "sessions")
	resolvedRoot, err := filepath.EvalSymlinks(sessionsRoot)
	if err != nil {
		slog.Debug("failed to resolve Kimi sessions root", "path", sessionsRoot, "error", err)
		return ""
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Clean(sessionDir))
	if err != nil {
		slog.Debug("failed to resolve Kimi session dir", "path", sessionDir, "error", err)
		return ""
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		slog.Debug("Kimi session dir escapes the sessions root", "session_dir", sessionDir, "root", resolvedRoot)
		return ""
	}
	return resolvedDir
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
// think/text blocks of the LAST turn that produced assistant content.
// Thinking blocks map to EventBodyBlockTypeThinking so downstream consumers
// can collapse reasoning, matching the Claude transcript shape.
func readKimiWireTranscriptBlocks(path string) ([]apptypes.EventBodyBlock, bool) {
	file, err := os.Open(path) // #nosec G304 -- path resolved through the Kimi session index
	if err != nil {
		slog.Debug("failed to open Kimi wire log", "path", path, "error", err)
		return nil, false
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
		return nil, false
	}

	blocks := blocksByTurn[lastTurn]
	if len(blocks) == 0 {
		return nil, false
	}
	return blocks, true
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
