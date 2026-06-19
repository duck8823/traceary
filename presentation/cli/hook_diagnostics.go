package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

const (
	hookCancellationDiagnosticSchemaVersion = 1
	hookCancellationDiagnosticStatusStarted = "started"
	hookDiagnosticsDirName                  = "diagnostics"
	// hookCancellationDiagnosticSessionHashLen is the number of hex characters
	// kept from the session hash embedded in diagnostic filenames. Twelve hex
	// characters (48 bits) make accidental collisions between distinct sessions
	// in a single diagnostics directory negligible while staying short.
	hookCancellationDiagnosticSessionHashLen = 12
)

type hookCancellationDiagnostic struct {
	SchemaVersion int       `json:"schema_version"`
	Client        string    `json:"client"`
	HostEvent     string    `json:"host_event"`
	HookCommand   string    `json:"hook_command"`
	HookPath      string    `json:"hook_path,omitempty"`
	Workspace     string    `json:"workspace,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"started_at"`

	Path string `json:"-"`
}

type hookCancellationDiagnosticScan struct {
	Records    []hookCancellationDiagnostic
	Unreadable []string
}

func inspectClaudeHookCancellationDiagnostics(ctx context.Context, projectDir string) doctorCheck {
	const checkName = "claude-hook-cancellations"
	workspace := resolveDoctorEventCoverageWorkspace(ctx, projectDir)
	scan, err := scanHookCancellationDiagnostics("claude", "SessionEnd", workspace)
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to inspect Claude hook cancellation diagnostics: %v", "Claude hook cancellation diagnostic の検査に失敗しました: %v", err),
		}
	}
	if len(scan.Records) == 0 && len(scan.Unreadable) == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"no pending Claude SessionEnd hook cancellation diagnostics found (workspace=%s)",
				"未完了の Claude SessionEnd hook cancellation diagnostic は見つかりませんでした (workspace=%s)",
				emptyAsDash(workspace.String()),
			),
		}
	}

	if len(scan.Records) == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Hint: Localize(
				"inspect the unreadable diagnostic file(s); absence of readable diagnostics is not proof that Claude never cancelled a hook before Traceary started",
				"読めない diagnostic file を確認してください。読める diagnostic が無いことは、Claude が Traceary 起動前に hook を cancel していない証明にはなりません",
			),
			Message: localizef(
				"found unreadable Claude hook cancellation diagnostic file(s): %s",
				"読めない Claude hook cancellation diagnostic file があります: %s",
				strings.Join(scan.Unreadable, ", "),
			),
		}
	}

	latest := scan.Records[0]
	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint: Localize(
			"the marker means Traceary reached Claude SessionEnd but did not complete cleanly; inspect the file and recent `traceary list --agent claude` output, then remove the marker after confirming it is stale. If Claude cancels before Traceary starts, no marker can be written.",
			"この marker は Traceary が Claude SessionEnd まで到達したものの正常完了していないことを示します。file と最近の `traceary list --agent claude` を確認し、stale と判断できたら marker を削除してください。Claude が Traceary 起動前に cancel した場合、marker は書けません。",
		),
		Message: localizef(
			"found %d pending Claude SessionEnd hook cancellation diagnostic(s); latest host_event=%s hook_command=%s hook_path=%s workspace=%s session_id=%s started_at=%s path=%s%s",
			"未完了の Claude SessionEnd hook cancellation diagnostic が %d 件あります。latest host_event=%s hook_command=%s hook_path=%s workspace=%s session_id=%s started_at=%s path=%s%s",
			len(scan.Records),
			emptyAsDash(latest.HostEvent),
			emptyAsDash(latest.HookCommand),
			emptyAsDash(latest.HookPath),
			emptyAsDash(latest.Workspace),
			emptyAsDash(latest.SessionID),
			formatHookDiagnosticTime(latest.StartedAt),
			latest.Path,
			formatUnreadableHookDiagnosticsSuffix(scan.Unreadable),
		),
	}
}

func beginHookCancellationDiagnostic(client, hostEvent, hookCommand string, sessionID types.SessionID, workspace types.Workspace) (string, error) {
	startedAt := time.Now().UTC()
	diagnosticsDir, err := hookDiagnosticsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(diagnosticsDir, 0o755); err != nil {
		return "", xerrors.Errorf("failed to create hook diagnostics directory: %w", err)
	}

	hookPath := ""
	if executablePath, err := os.Executable(); err == nil {
		hookPath = executablePath
	}
	record := hookCancellationDiagnostic{
		SchemaVersion: hookCancellationDiagnosticSchemaVersion,
		Client:        strings.TrimSpace(client),
		HostEvent:     strings.TrimSpace(hostEvent),
		HookCommand:   strings.TrimSpace(hookCommand),
		HookPath:      hookPath,
		Workspace:     workspace.String(),
		SessionID:     sessionID.String(),
		Status:        hookCancellationDiagnosticStatusStarted,
		StartedAt:     startedAt,
	}

	path := filepath.Join(diagnosticsDir, hookCancellationDiagnosticFileName(record, startedAt))
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", xerrors.Errorf("failed to encode hook cancellation diagnostic: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return "", xerrors.Errorf("failed to write hook cancellation diagnostic: %w", err)
	}

	return path, nil
}

func clearHookCancellationDiagnostic(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook cancellation diagnostic: %w", err)
	}
	return nil
}

func updateHookCancellationDiagnosticWorkspace(path string, workspace types.Workspace) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return xerrors.Errorf("failed to read hook cancellation diagnostic: %w", err)
	}
	var record hookCancellationDiagnostic
	if err := json.Unmarshal(data, &record); err != nil {
		return xerrors.Errorf("failed to decode hook cancellation diagnostic: %w", err)
	}
	record.Workspace = workspace.String()
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode hook cancellation diagnostic: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return xerrors.Errorf("failed to update hook cancellation diagnostic: %w", err)
	}
	return nil
}

func clearHookCancellationDiagnosticsForSession(client, hostEvent string, sessionID types.SessionID) error {
	sessionIDValue := strings.TrimSpace(sessionID.String())
	if sessionIDValue == "" {
		return nil
	}
	scan, err := scanHookCancellationDiagnostics(client, hostEvent, "")
	if err != nil {
		return err
	}
	for _, record := range scan.Records {
		if record.SessionID != sessionIDValue {
			continue
		}
		if err := clearHookCancellationDiagnostic(record.Path); err != nil {
			return err
		}
	}
	for _, path := range scan.Unreadable {
		if !hookCancellationDiagnosticPathMatchesSession(path, client, hostEvent, sessionID) {
			continue
		}
		if err := clearHookCancellationDiagnostic(path); err != nil {
			return err
		}
	}
	return nil
}

// hookCancellationDiagnosticPathMatchesSession reports whether an unreadable
// diagnostic file belongs to the given (client, hostEvent, session) by matching
// the stable hash segment embedded in the filename. Matching a delimited hash
// segment — rather than a hyphenated client/event/session prefix — keeps cleanup
// exact even when session IDs themselves contain hyphens, which a prefix match
// would overmatch (e.g. session "cancelled" overmatching "cancelled-session").
func hookCancellationDiagnosticPathMatchesSession(path, client, hostEvent string, sessionID types.SessionID) bool {
	fileName := filepath.Base(strings.TrimSpace(path))
	if !strings.HasSuffix(fileName, ".json") {
		return false
	}
	hash := hookCancellationDiagnosticSessionHash(client, hostEvent, sessionID.String())
	for _, segment := range strings.Split(strings.TrimSuffix(fileName, ".json"), "-") {
		if segment == hash {
			return true
		}
	}
	return false
}

// hookCancellationDiagnosticSessionHash derives the stable filename segment that
// identifies a diagnostic's (client, hostEvent, session) tuple. The inputs are
// trimmed to match the values stored on the record, so generation and cleanup
// always agree on the same hash.
func hookCancellationDiagnosticSessionHash(client, hostEvent, sessionID string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(client),
		strings.TrimSpace(hostEvent),
		strings.TrimSpace(sessionID),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "s" + hex.EncodeToString(sum[:])[:hookCancellationDiagnosticSessionHashLen]
}

func emptyAsDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func formatHookDiagnosticTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatUnreadableHookDiagnosticsSuffix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("; unreadable=%s", strings.Join(paths, ","))
}

func scanHookCancellationDiagnostics(client, hostEvent string, workspace types.Workspace) (hookCancellationDiagnosticScan, error) {
	diagnosticsDir, err := hookDiagnosticsDir()
	if err != nil {
		return hookCancellationDiagnosticScan{}, err
	}
	entries, err := os.ReadDir(diagnosticsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return hookCancellationDiagnosticScan{}, nil
		}
		return hookCancellationDiagnosticScan{}, xerrors.Errorf("failed to read hook diagnostics directory: %w", err)
	}

	client = strings.TrimSpace(client)
	hostEvent = strings.TrimSpace(hostEvent)
	workspaceValue := strings.TrimSpace(workspace.String())
	scan := hookCancellationDiagnosticScan{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(diagnosticsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			scan.Unreadable = append(scan.Unreadable, path)
			continue
		}
		var record hookCancellationDiagnostic
		if err := json.Unmarshal(data, &record); err != nil {
			scan.Unreadable = append(scan.Unreadable, path)
			continue
		}
		if record.Status != hookCancellationDiagnosticStatusStarted {
			continue
		}
		if client != "" && record.Client != client {
			continue
		}
		if hostEvent != "" && record.HostEvent != hostEvent {
			continue
		}
		// Empty-workspace records intentionally remain visible in every
		// scoped doctor run: failing closed here would hide cancellation
		// evidence from the exact cases where the host did not provide cwd
		// or Traceary was interrupted before workspace resolution.
		if workspaceValue != "" && strings.TrimSpace(record.Workspace) != "" && record.Workspace != workspaceValue {
			continue
		}
		record.Path = path
		scan.Records = append(scan.Records, record)
	}

	sort.Slice(scan.Records, func(i, j int) bool {
		left := scan.Records[i]
		right := scan.Records[j]
		if !left.StartedAt.Equal(right.StartedAt) {
			return left.StartedAt.After(right.StartedAt)
		}
		return left.Path < right.Path
	})
	sort.Strings(scan.Unreadable)
	return scan, nil
}

func hookDiagnosticsDir() (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, hookDiagnosticsDirName), nil
}

func hookCancellationDiagnosticFileName(record hookCancellationDiagnostic, startedAt time.Time) string {
	parts := []string{
		record.Client,
		record.HostEvent,
		record.SessionID,
		hookCancellationDiagnosticSessionHash(record.Client, record.HostEvent, record.SessionID),
		resolveHookStateKey(),
		startedAt.UTC().Format("20060102T150405.000000000Z"),
	}
	sanitized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = sanitizeHookStateKey(part)
		if part != "" && part != "default" {
			sanitized = append(sanitized, part)
		}
	}
	if len(sanitized) == 0 {
		return "hook-diagnostic.json"
	}
	return strings.Join(sanitized, "-") + ".json"
}
