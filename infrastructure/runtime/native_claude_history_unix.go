//go:build darwin || linux

package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

const claudeRecoveryVersion = "2.1.318"
const maxNativeHistoryBytes int64 = 16 * 1024 * 1024
const maxNativeHistoryLine = 4 * 1024 * 1024

type nativeHistoryMetadata struct {
	NamespaceWitness string
	Model            string
}

// nativeHistoryFields borrows vendor JSON but retains only named metadata.
// Critical aliases/duplicates reject, while opaque tool/context objects are
// neither interpreted as metadata nor subject to our private journal schema.
func nativeHistoryFields(data []byte, names ...string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) || !json.Valid(data) {
		return nil, domain.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, domain.ErrInvalid
	}
	fields := make(map[string]json.RawMessage, len(names))
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, domain.ErrInvalid
		}
		key, ok := token.(string)
		if !ok {
			return nil, domain.ErrInvalid
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, domain.ErrInvalid
		}
		exact := false
		for _, name := range names {
			if key != name {
				continue
			}
			if fields[name] != nil {
				return nil, domain.ErrInvalid
			}
			fields[name] = raw
			exact = true
			break
		}
		if exact {
			continue
		}
		for _, name := range names {
			if !strings.EqualFold(strings.ReplaceAll(key, "_", ""), strings.ReplaceAll(name, "_", "")) {
				continue
			}
			return nil, domain.ErrInvalid
		}
	}
	return fields, nil
}

func nativeHistoryString(fields map[string]json.RawMessage, key string) (string, error) {
	var value string
	if json.Unmarshal(fields[key], &value) != nil || value == "" {
		return "", domain.ErrInvalid
	}
	return value, nil
}

// parseClaudeRecoveryHistory validates the whole bounded JSONL stream. It
// returns only a recorded model, never content, tool state, readiness or exit.
func parseClaudeRecoveryHistory(ctx context.Context, reader io.Reader, plan nativeStartPlan) (string, error) {
	if !plan.valid() || reader == nil {
		return "", domain.ErrInvalid
	}
	limited := &io.LimitedReader{R: reader, N: maxNativeHistoryBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), maxNativeHistoryLine)
	model := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", xerrors.Errorf("inspect native history budget: %w", err)
		}
		// This pinned native version can emit both spellings on the same row.
		// sessionId remains required authority; session_id, when present, must
		// agree. It cannot replace canonical identity or silently override it.
		fields, err := nativeHistoryFields(scanner.Bytes(), "type", "sessionId", "session_id", "cwd", "version", "message")
		if err != nil {
			return "", err
		}
		kind, err := nativeHistoryString(fields, "type")
		if err != nil {
			return "", err
		}
		required := kind == "user" || kind == "assistant" || kind == "system"
		if fields["session_id"] != nil {
			alias, err := nativeHistoryString(fields, "session_id")
			if err != nil || alias != plan.SessionID {
				return "", domain.ErrChanged
			}
		}
		for key, want := range map[string]string{"sessionId": plan.SessionID, "cwd": plan.CWD, "version": claudeRecoveryVersion} {
			if fields[key] == nil && !required {
				continue
			}
			actual, err := nativeHistoryString(fields, key)
			if err != nil {
				return "", err
			}
			if actual != want {
				return "", domain.ErrChanged
			}
		}
		if kind != "assistant" {
			continue
		}
		message, err := nativeHistoryFields(fields["message"], "model")
		if err != nil {
			return "", err
		}
		value, err := nativeHistoryString(message, "model")
		if err != nil {
			return "", err
		}
		// The pinned native format also records local synthetic assistant
		// messages. They are not model-call evidence and cannot establish or
		// replace the last actual model; a synthetic-only file stays unknown.
		if value == "<synthetic>" {
			continue
		}
		if !strings.HasPrefix(value, "claude-") || len(value) > 256 {
			return "", domain.ErrUnsupported
		}
		for _, c := range value {
			if c < '!' || c > '~' {
				return "", domain.ErrInvalid
			}
		}
		model = value
	}
	if scanner.Err() != nil || limited.N == 0 {
		return "", domain.ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return "", xerrors.Errorf("inspect native history budget: %w", err)
	}
	if model == "" {
		return "", domain.ErrNotFound
	}
	return model, nil
}

// readClaudeRecoveryHistory opens exactly the callback's planned native file.
// No project-directory scan, guessed slug, symlink traversal or file mutation.
func readClaudeRecoveryHistory(ctx context.Context, event nativeStartEvent) (nativeHistoryMetadata, error) {
	p := event.Plan
	encoded, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": p.SessionID, "cwd": p.CWD, "source": p.Source, "transcript_path": event.TranscriptPath})
	if _, err := decodeNativeStart(encoded, p, nativePlanHints(p)); err != nil {
		return nativeHistoryMetadata{}, err
	}
	root, err := privateRuntimeDir(p.Namespace, false)
	if err != nil {
		return nativeHistoryMetadata{}, err
	}
	defer func() { _ = unix.Close(root) }()
	witness, err := directoryWitness(root)
	if err != nil {
		return nativeHistoryMetadata{}, err
	}
	rel, err := filepath.Rel(p.Namespace, event.TranscriptPath)
	if err != nil {
		return nativeHistoryMetadata{}, domain.ErrInvalid
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 {
		return nativeHistoryMetadata{}, domain.ErrInvalid
	}
	project, err := unix.Openat(root, parts[0], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nativeHistoryMetadata{}, domain.ErrUnsafe
	}
	defer func() { _ = unix.Close(project) }()
	projectWitness, err := directoryWitness(project)
	if err != nil {
		return nativeHistoryMetadata{}, err
	}
	fd, err := unix.Openat(project, parts[1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nativeHistoryMetadata{}, domain.ErrUnsafe
	}
	file := os.NewFile(uintptr(fd), "owned native recovery history")
	defer func() { _ = file.Close() }()
	var before, after, named unix.Stat_t
	if unix.Fstat(fd, &before) != nil || !privateRegular(before) {
		return nativeHistoryMetadata{}, domain.ErrUnsafe
	}
	if before.Size <= 0 || before.Size > maxNativeHistoryBytes {
		return nativeHistoryMetadata{}, domain.ErrLimit
	}
	info, err := file.Stat()
	if err != nil {
		return nativeHistoryMetadata{}, domain.ErrUnsafe
	}
	model, err := parseClaudeRecoveryHistory(ctx, file, p)
	if err != nil {
		return nativeHistoryMetadata{}, err
	}
	fresh, err := file.Stat()
	if err != nil || unix.Fstat(fd, &after) != nil || !privateRegular(after) || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || !info.ModTime().Equal(fresh.ModTime()) {
		return nativeHistoryMetadata{}, domain.ErrChanged
	}
	if unix.Fstatat(project, parts[1], &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateRegular(named) || named.Dev != after.Dev || named.Ino != after.Ino {
		return nativeHistoryMetadata{}, domain.ErrChanged
	}
	current, err := unix.Openat(root, parts[0], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nativeHistoryMetadata{}, domain.ErrChanged
	}
	currentWitness, err := directoryWitness(current)
	_ = unix.Close(current)
	if err != nil || currentWitness != projectWitness {
		return nativeHistoryMetadata{}, domain.ErrChanged
	}
	if err := verifyRuntimeRoot(p.Namespace, witness, root); err != nil {
		return nativeHistoryMetadata{}, err
	}
	digest := sha256.Sum256([]byte(witness))
	return nativeHistoryMetadata{NamespaceWitness: hex.EncodeToString(digest[:]), Model: model}, nil
}
