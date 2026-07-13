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

const hookSpoolSchemaVersion = 1

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

func inspectHookSpoolDiagnostics(clients []string) doctorCheck {
	const name = "hook-spool"
	records, unreadable, err := scanHookSpoolRecords(clients)
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
		Hint: Localize("the host interrupted or Traceary failed before commit; inspect the preserved payload and retry the matching hook command before removing the record", "host が中断したか commit 前に Traceary が失敗しました。保存された payload を確認し、対応する hook command を再実行してから record を削除してください"),
	}
}
