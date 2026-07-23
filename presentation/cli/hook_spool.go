package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const (
	hookSpoolSchemaVersion = 1
	// hookSpoolReplayBatchLimit caps opportunistic drain work per hook
	// invocation so replay cannot exhaust the host timeout budget.
	hookSpoolReplayBatchLimit = 5
)

type hookInvocationSpec struct {
	Command string
	Client  string
	Action  string
	DBPath  string
}

type hookSpoolRecord struct {
	SchemaVersion int       `json:"schema_version"`
	Command       string    `json:"command"`
	Client        string    `json:"client"`
	Action        string    `json:"action,omitempty"`
	DBPath        string    `json:"db_path,omitempty"`
	Payload       string    `json:"payload"`
	CreatedAt     time.Time `json:"created_at"`
	Path          string    `json:"-"`
}

// explicitHookPayloadReader tells readHookPayload that the bytes came from a
// durable spool wrapper and must take precedence over TRACEARY_HOOK_INPUT.
type explicitHookPayloadReader struct{ *bytes.Reader }

func newExplicitHookPayloadReader(payload []byte) io.Reader {
	return &explicitHookPayloadReader{Reader: bytes.NewReader(payload)}
}

func (c *RootCLI) runHookDurably(
	ctx context.Context,
	name string,
	spec hookInvocationSpec,
	input io.Reader,
	run func(io.Reader) error,
) error {
	return runHookBestEffort(name, func() error {
		// Drain a bounded batch of older timeout-killed records before this
		// invocation's own work. Failures stay on disk for later retries.
		if ctx.Err() == nil {
			if replayed, failed := c.drainHookSpoolRecords(ctx, hookSpoolReplayBatchLimit); replayed > 0 || failed > 0 {
				slog.Debug("hook spool drain", "replayed", replayed, "failed", failed)
			}
		}
		payload, err := readHookPayload(input)
		if err != nil {
			return err
		}
		record := hookSpoolRecord{
			SchemaVersion: hookSpoolSchemaVersion,
			Command:       strings.TrimSpace(spec.Command),
			Client:        strings.TrimSpace(spec.Client),
			Action:        strings.TrimSpace(spec.Action),
			DBPath:        strings.TrimSpace(spec.DBPath),
			Payload:       string(payload),
			CreatedAt:     time.Now().UTC(),
		}
		path, err := persistHookSpoolRecord(record)
		if err != nil {
			// Preserve existing fail-soft behavior when the state directory is
			// unavailable. The operational error remains visible in debug logs;
			// successful persistence is the timeout-kill guarantee.
			slog.Debug("hook spool persistence failed", "command", spec.Command, "client", spec.Client, "error", err)
			return run(newExplicitHookPayloadReader(payload))
		}
		if err := ctx.Err(); err != nil {
			return xerrors.Errorf("hook context cancelled after spool persistence: %w", err)
		}
		if err := run(newExplicitHookPayloadReader(payload)); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return xerrors.Errorf("failed to clear committed hook spool record: %w", err)
		}
		return nil
	})
}

// drainHookSpoolRecords replays up to limit pending spool records (oldest
// first). A record is removed only after replay returns nil. Returns counts
// of successful replays and retained failures.
func (c *RootCLI) drainHookSpoolRecords(ctx context.Context, limit int) (replayed, failed int) {
	if limit <= 0 {
		return 0, 0
	}
	records, _, err := scanHookSpoolRecords(nil)
	if err != nil || len(records) == 0 {
		return 0, 0
	}
	// scanHookSpoolRecords returns newest first; drain oldest first.
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	for i, record := range records {
		if i >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			break
		}
		if strings.TrimSpace(record.Path) == "" {
			failed++
			continue
		}
		if err := c.replayHookSpoolRecord(ctx, record); err != nil {
			failed++
			slog.Debug("hook spool replay failed", "path", record.Path, "command", record.Command, "client", record.Client, "error", err)
			continue
		}
		if err := os.Remove(record.Path); err != nil && !os.IsNotExist(err) {
			failed++
			slog.Debug("hook spool clear failed after replay", "path", record.Path, "error", err)
			continue
		}
		replayed++
	}
	return replayed, failed
}

func (c *RootCLI) replayHookSpoolRecord(ctx context.Context, record hookSpoolRecord) error {
	input := newExplicitHookPayloadReader([]byte(record.Payload))
	dbPath := record.DBPath
	client := record.Client
	action := record.Action
	switch strings.TrimSpace(record.Command) {
	case "session":
		return c.runHookSession(ctx, io.Discard, input, client, action, dbPath)
	case "audit":
		return c.runHookAudit(ctx, input, client, dbPath)
	case "compact":
		return c.runHookCompact(ctx, io.Discard, input, client, action, dbPath)
	case "subagent-start":
		return c.runHookSubagentStart(ctx, input, client, dbPath)
	case "subagent-stop":
		return c.runHookSubagentStop(ctx, input, client, dbPath)
	case "prompt":
		return c.runHookPrompt(ctx, input, client, dbPath)
	case "transcript":
		return c.runHookTranscript(ctx, input, client, dbPath)
	case "usage":
		return c.runHookUsage(ctx, input, client, dbPath)
	case "antigravity":
		return c.replayAntigravitySpoolRecord(ctx, input, action, dbPath)
	case "grok":
		return c.replayGrokSpoolRecord(ctx, input, action, dbPath)
	case "kimi":
		return c.replayKimiSpoolRecord(ctx, input, action, dbPath)
	default:
		return xerrors.Errorf("unsupported hook spool command: %s", record.Command)
	}
}

func (c *RootCLI) replayAntigravitySpoolRecord(ctx context.Context, input io.Reader, action, dbPath string) error {
	switch strings.TrimSpace(action) {
	case "pre-invocation":
		return c.runHookAntigravityPreInvocation(ctx, io.Discard, input, dbPath)
	case "pre-tool-use":
		return c.runHookAntigravityPreToolUse(ctx, io.Discard, input, dbPath)
	case "post-tool-use":
		return c.runHookAntigravityPostToolUse(ctx, io.Discard, input, dbPath)
	case "stop":
		return c.runHookAntigravityStop(ctx, io.Discard, input, dbPath)
	default:
		return xerrors.Errorf("unsupported antigravity spool action: %s", action)
	}
}

func (c *RootCLI) replayGrokSpoolRecord(ctx context.Context, input io.Reader, action, dbPath string) error {
	switch strings.TrimSpace(action) {
	case "session-start":
		return c.runHookGrokSessionStart(ctx, input, dbPath)
	case "user-prompt-submit":
		return c.runHookGrokUserPromptSubmit(ctx, input, dbPath)
	case "pre-tool-use":
		return c.runHookGrokPreToolUse(ctx, input, dbPath)
	case "post-tool-use":
		return c.runHookGrokPostToolUse(ctx, input, dbPath)
	case "stop":
		return c.runHookGrokStop(ctx, input, dbPath)
	case "pre-compact":
		return c.runHookGrokPreCompact(ctx, input, dbPath)
	case "post-compact":
		return c.runHookGrokPostCompact(ctx, input, dbPath)
	default:
		return xerrors.Errorf("unsupported grok spool action: %s", action)
	}
}

func (c *RootCLI) replayKimiSpoolRecord(ctx context.Context, input io.Reader, action, dbPath string) error {
	switch strings.TrimSpace(action) {
	case "session-start":
		return c.runHookKimiSessionStart(ctx, input, dbPath)
	case "session-end":
		return c.runHookKimiSessionEnd(ctx, input, dbPath)
	case "user-prompt-submit":
		return c.runHookKimiUserPromptSubmit(ctx, input, dbPath)
	case "pre-tool-use":
		return c.runHookKimiPreToolUse(ctx, input, dbPath)
	case "post-tool-use":
		return c.runHookKimiPostToolUse(ctx, input, dbPath)
	case "post-tool-use-failure":
		return c.runHookKimiPostToolUseFailure(ctx, input, dbPath)
	case "stop":
		return c.runHookKimiStop(ctx, input, dbPath)
	case "subagent-stop":
		return c.runHookKimiSubagentStop(ctx, input, dbPath)
	case "pre-compact":
		return c.runHookKimiPreCompact(ctx, input, dbPath)
	case "post-compact":
		return c.runHookKimiPostCompact(ctx, input, dbPath)
	default:
		return xerrors.Errorf("unsupported kimi spool action: %s", action)
	}
}

func persistHookSpoolRecord(record hookSpoolRecord) (string, error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", xerrors.Errorf("failed to create hook spool directory: %w", err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", xerrors.Errorf("failed to generate hook spool ID: %w", err)
	}
	base := fmt.Sprintf("%s-%s-%s.json", record.CreatedAt.Format("20060102T150405.000000000Z"), sanitizeHookStateKey(record.Client), hex.EncodeToString(random))
	path := filepath.Join(dir, base)
	tmpPath := path + ".tmp"
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", xerrors.Errorf("failed to encode hook spool record: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(tmpPath, encoded, 0o600); err != nil {
		return "", xerrors.Errorf("failed to write hook spool record: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", xerrors.Errorf("failed to publish hook spool record: %w", err)
	}
	return path, nil
}

func hookSpoolDir() (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "spool"), nil
}

func scanHookSpoolRecords(clients []string) ([]hookSpoolRecord, []string, error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	allowed := make(map[string]struct{}, len(clients))
	for _, client := range clients {
		allowed[strings.TrimSpace(client)] = struct{}{}
	}
	records := []hookSpoolRecord{}
	unreadable := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			unreadable = append(unreadable, path)
			continue
		}
		var record hookSpoolRecord
		if err := json.Unmarshal(data, &record); err != nil || record.SchemaVersion != hookSpoolSchemaVersion {
			unreadable = append(unreadable, path)
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[record.Client]; !ok {
				continue
			}
		}
		record.Path = path
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	sort.Strings(unreadable)
	return records, unreadable, nil
}

func (c *RootCLI) inspectHookSpoolDiagnostics(clients []string) doctorCheck {
	records, unreadable, err := scanHookSpoolRecords(clients)
	return c.inspectHookSpoolDiagnosticsFromScan(records, unreadable, err)
}

func (c *RootCLI) inspectHookSpoolDiagnosticsFromScan(
	records []hookSpoolRecord,
	unreadable []string,
	err error,
) doctorCheck {
	const name = "hook-spool"
	if err != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect hook spool: %v", "hook spool の検査に失敗しました: %v", err)}
	}
	if len(records) == 0 && len(unreadable) == 0 {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("no pending hook spool records found", "未処理の hook spool record はありません")}
	}
	latest := "-"
	if len(records) > 0 {
		latest = fmt.Sprintf("client=%s command=%s action=%s created_at=%s path=%s", emptyAsDash(records[0].Client), emptyAsDash(records[0].Command), emptyAsDash(records[0].Action), records[0].CreatedAt.Format(time.RFC3339Nano), records[0].Path)
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"found %d pending hook spool record(s) and %d unreadable record(s); latest %s",
			"未処理の hook spool record が %d 件、読めない record が %d 件あります。latest %s",
			len(records), len(unreadable), latest,
		),
		Hint: Localize(
			"records are drained automatically on later hook invocations (bounded batch). Run `traceary doctor --fix` to force a larger drain, or inspect payloads under the hook spool directory before manual removal.",
			"record は後続 hook 呼び出し時に bounded batch で自動 drain されます。`traceary doctor --fix` で大きめに drain するか、手動削除前に spool ディレクトリの payload を確認してください。",
		),
		FixCommand:       "traceary doctor --fix",
		AutoFixAvailable: true,
		FixFunc: func(ctx context.Context, dryRun bool) (string, error) {
			pending, _, err := scanHookSpoolRecords(nil)
			if err != nil {
				return "", err
			}
			if dryRun {
				return localizef("would drain up to %d pending hook spool record(s)", "未処理 hook spool record 最大 %d 件を drain します", min(len(pending), 200)), nil
			}
			// Force path: allow a larger batch than the per-hook opportunistic drain.
			limit := len(pending)
			if limit > 200 {
				limit = 200
			}
			replayed, failed := c.drainHookSpoolRecords(ctx, limit)
			remaining := len(pending) - replayed
			if remaining < 0 {
				remaining = 0
			}
			return localizef("drained hook spool: replayed=%d failed=%d remaining_estimate=%d", "hook spool を drain しました: replayed=%d failed=%d remaining_estimate=%d", replayed, failed, remaining), nil
		},
	}
}
