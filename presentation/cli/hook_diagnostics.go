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

	hookCancellationDiagnosticPhaseWorkspaceResolved = "workspace_resolved"
	hookCancellationDiagnosticPhaseStoreInitialized  = "store_initialized"

	// hookCancellationDiagnosticRetentionWindow bounds how long a marker for a
	// given (client, host_event) stays eligible for begin-time GC and how old
	// an Unknown-store marker must be before `doctor --fix` removes it.
	hookCancellationDiagnosticRetentionWindow = 7 * 24 * time.Hour
	// hookCancellationDiagnosticRetentionCap bounds how many markers for a
	// given (client, host_event) survive begin-time GC regardless of age, so
	// the diagnostics directory cannot grow unbounded even under a tight
	// begin/kill loop within the retention window.
	hookCancellationDiagnosticRetentionCap = 20
)

type hookCancellationDiagnostic struct {
	SchemaVersion int       `json:"schema_version"`
	Client        string    `json:"client"`
	HostEvent     string    `json:"host_event"`
	HookCommand   string    `json:"hook_command"`
	HookPath      string    `json:"hook_path,omitempty"`
	Workspace     string    `json:"workspace,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	DBPath        string    `json:"db_path,omitempty"`
	Phase         string    `json:"phase,omitempty"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"started_at"`

	Path string `json:"-"`
}

type hookCancellationDiagnosticScan struct {
	Records    []hookCancellationDiagnostic
	Unreadable []string
}

type hookDiagnosticSessionLookup interface {
	FindEndedSessionIDs(context.Context, []types.SessionID) (map[types.SessionID]struct{}, error)
}

// hookCancellationDiagnosticClassification splits scanned markers three ways.
// Resolved: the session ended in the store currently being inspected.
// Actionable: the session is known-active in that same store.
// Unknown: the marker references a different store (or predates db_path
// recording), so its session state cannot be determined here. Unknown is
// never surfaced as Actionable — a scratch/other --db-path store must not
// mark every historical marker as needing attention.
type hookCancellationDiagnosticClassification struct {
	Actionable []hookCancellationDiagnostic
	Resolved   []hookCancellationDiagnostic
	Unknown    []hookCancellationDiagnostic
}

func (c *RootCLI) inspectClaudeHookCancellationDiagnostics(ctx context.Context, dbPath, projectDir string) doctorCheck {
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
	classification, err := classifyHookCancellationDiagnostics(ctx, scan.Records, c.session, dbPath)
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve Claude hook cancellation diagnostics against session state: %v", "Claude hook cancellation diagnostic と session state の照合に失敗しました: %v", err),
		}
	}

	if len(classification.Actionable) == 0 && len(classification.Resolved) == 0 && len(classification.Unknown) == 0 {
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

	agedUnknown := agedHookCancellationDiagnostics(classification.Unknown, time.Now().UTC().Add(-hookCancellationDiagnosticRetentionWindow))
	fixRecords := append(append([]hookCancellationDiagnostic{}, classification.Resolved...), agedUnknown...)
	fixCommand := ""
	var fix doctorFixFunc
	if len(fixRecords) > 0 {
		fixCommand = fmt.Sprintf("traceary doctor --client claude --project-dir %s --fix --dry-run", shellQuote(projectDir))
		fix = resolvedHookCancellationDiagnosticFix(fixRecords)
	}

	if len(classification.Actionable) == 0 {
		if len(fixRecords) > 0 {
			return doctorCheck{
				Name:   checkName,
				Status: doctorStatusWarn,
				Hint: Localize(
					"the referenced sessions have ended, or the markers reference another store and have aged past the retention window; preview the safe marker cleanup with the fix command",
					"参照先 session は終了済みか、marker が別ストアを参照したまま retention window を超えています。fix command で安全な marker cleanup を preview してください",
				),
				Message: localizef(
					"found %d resolved and %d aged unknown-store Claude SessionEnd hook cancellation diagnostic(s) eligible for cleanup%s",
					"cleanup 可能な解決済み Claude SessionEnd hook cancellation diagnostic が %d 件、別ストア参照で retention window を超えた marker が %d 件あります%s",
					len(classification.Resolved),
					len(agedUnknown),
					formatUnreadableHookDiagnosticsSuffix(scan.Unreadable),
				),
				FixCommand:       fixCommand,
				AutoFixAvailable: true,
				FixFunc:          fix,
			}
		}
		if len(classification.Unknown) > 0 && len(scan.Unreadable) == 0 {
			return doctorCheck{
				Name:   checkName,
				Status: doctorStatusWarn,
				Hint: Localize(
					"these markers reference a different store than the one currently inspected, or predate db_path recording; they are not evidence of an actionable cancellation in this store",
					"これらの marker は現在検査中のストアとは別のストアを参照しているか、db_path 記録より前のものです。このストアで対応が必要な cancellation の証拠にはなりません",
				),
				Message: localizef(
					"found %d Claude SessionEnd hook cancellation diagnostic(s) of unknown store affinity",
					"ストアの対応関係が不明な Claude SessionEnd hook cancellation diagnostic が %d 件あります",
					len(classification.Unknown),
				),
			}
		}
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Hint: Localize(
				"inspect the unreadable diagnostic file(s); absence of readable diagnostics is not proof that Claude never cancelled a hook before Traceary started",
				"読めない diagnostic file を確認してください。読める diagnostic が無いことは、Claude が Traceary 起動前に hook を cancel していない証明にはなりません",
			),
			Message: localizef(
				"found %d unknown-store Claude SessionEnd hook cancellation diagnostic(s) and unreadable diagnostic file(s): %s",
				"ストアの対応関係が不明な Claude SessionEnd hook cancellation diagnostic が %d 件、読めない diagnostic file があります: %s",
				len(classification.Unknown),
				strings.Join(scan.Unreadable, ", "),
			),
		}
	}

	latest := classification.Actionable[0]
	check := doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint: Localize(
			"the marker means Traceary reached Claude SessionEnd but did not complete cleanly; inspect the file and recent `traceary list --agent claude` output, then remove the marker after confirming it is stale. If Claude cancels before Traceary starts, no marker can be written.",
			"この marker は Traceary が Claude SessionEnd まで到達したものの正常完了していないことを示します。file と最近の `traceary list --agent claude` を確認し、stale と判断できたら marker を削除してください。Claude が Traceary 起動前に cancel した場合、marker は書けません。",
		),
		Message: localizef(
			"found %d actionable Claude SessionEnd hook cancellation diagnostic(s), %d resolved, %d unknown-store; latest phase=%s host_event=%s hook_command=%s hook_path=%s workspace=%s session_id=%s started_at=%s path=%s%s",
			"対応が必要な Claude SessionEnd hook cancellation diagnostic が %d 件、解決済みが %d 件、ストア不明が %d 件あります。latest phase=%s host_event=%s hook_command=%s hook_path=%s workspace=%s session_id=%s started_at=%s path=%s%s",
			len(classification.Actionable),
			len(classification.Resolved),
			len(classification.Unknown),
			emptyAsDash(latest.Phase),
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
	if len(fixRecords) > 0 {
		check.FixCommand = fixCommand
		check.AutoFixAvailable = true
		check.FixFunc = fix
	}
	return check
}

func classifyHookCancellationDiagnostics(
	ctx context.Context,
	records []hookCancellationDiagnostic,
	sessions hookDiagnosticSessionLookup,
	currentDBPath string,
) (hookCancellationDiagnosticClassification, error) {
	classification := hookCancellationDiagnosticClassification{}
	currentDBPath = strings.TrimSpace(currentDBPath)

	// A marker only speaks to the store it was written against. Comparing it
	// to any other store — including "we don't know which store" — is not
	// evidence either way, so it is never Actionable and never Resolved.
	sameStore := make([]hookCancellationDiagnostic, 0, len(records))
	for _, record := range records {
		recordDBPath := strings.TrimSpace(record.DBPath)
		if currentDBPath == "" || recordDBPath == "" || recordDBPath != currentDBPath {
			classification.Unknown = append(classification.Unknown, record)
			continue
		}
		sameStore = append(sameStore, record)
	}
	if len(sameStore) == 0 {
		return classification, nil
	}

	if sessions == nil {
		classification.Actionable = append(classification.Actionable, sameStore...)
		return classification, nil
	}
	ids := make([]types.SessionID, 0, len(sameStore))
	for _, record := range sameStore {
		if strings.TrimSpace(record.SessionID) != "" {
			ids = append(ids, types.SessionID(record.SessionID))
		}
	}
	endedIDs, err := sessions.FindEndedSessionIDs(ctx, ids)
	if err != nil {
		return hookCancellationDiagnosticClassification{}, xerrors.Errorf("failed to inspect ended sessions: %w", err)
	}
	for _, record := range sameStore {
		if strings.TrimSpace(record.SessionID) == "" {
			classification.Actionable = append(classification.Actionable, record)
			continue
		}
		if _, ended := endedIDs[types.SessionID(record.SessionID)]; ended {
			classification.Resolved = append(classification.Resolved, record)
			continue
		}
		classification.Actionable = append(classification.Actionable, record)
	}
	return classification, nil
}

// agedHookCancellationDiagnostics returns the subset of records started
// before cutoff, preserving order.
func agedHookCancellationDiagnostics(records []hookCancellationDiagnostic, cutoff time.Time) []hookCancellationDiagnostic {
	aged := make([]hookCancellationDiagnostic, 0, len(records))
	for _, record := range records {
		if record.StartedAt.Before(cutoff) {
			aged = append(aged, record)
		}
	}
	return aged
}

func resolvedHookCancellationDiagnosticFix(records []hookCancellationDiagnostic) doctorFixFunc {
	paths := make([]string, 0, len(records))
	for _, record := range records {
		paths = append(paths, record.Path)
	}
	return func(_ context.Context, dryRun bool) (string, error) {
		if dryRun {
			return fmt.Sprintf("would remove %d resolved Claude SessionEnd hook cancellation diagnostic(s)", len(paths)), nil
		}
		for _, path := range paths {
			if err := clearHookCancellationDiagnostic(path); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("removed %d resolved Claude SessionEnd hook cancellation diagnostic(s)", len(paths)), nil
	}
}

func beginHookCancellationDiagnostic(client, hostEvent, hookCommand string, sessionID types.SessionID, workspace types.Workspace, dbPath string) (string, error) {
	startedAt := time.Now().UTC()
	diagnosticsDir, err := hookDiagnosticsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(diagnosticsDir, 0o755); err != nil {
		return "", xerrors.Errorf("failed to create hook diagnostics directory: %w", err)
	}
	gcHookCancellationDiagnostics(client, hostEvent, startedAt)

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
		DBPath:        strings.TrimSpace(dbPath),
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

// gcHookCancellationDiagnostics removes stale markers for the same
// (client, host_event) before a new one is written, so a host that keeps
// killing SessionEnd before it completes cannot grow the diagnostics
// directory without bound. Markers within the retention window and cap are
// left alone; scan failures are treated as "nothing to GC" since begin must
// still write the new marker.
func gcHookCancellationDiagnostics(client, hostEvent string, now time.Time) {
	scan, err := scanHookCancellationDiagnostics(client, hostEvent, "")
	if err != nil {
		return
	}
	cutoff := now.Add(-hookCancellationDiagnosticRetentionWindow)
	// Reserve one cap slot for the marker this call is about to write, so a
	// steady stream of begin calls converges on the cap instead of settling
	// one marker over it.
	keepWithinCap := hookCancellationDiagnosticRetentionCap - 1
	for i, record := range scan.Records {
		if i < keepWithinCap && !record.StartedAt.Before(cutoff) {
			continue
		}
		_ = clearHookCancellationDiagnostic(record.Path)
	}
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
	return mutateHookCancellationDiagnostic(path, func(record *hookCancellationDiagnostic) {
		record.Workspace = workspace.String()
	})
}

// updateHookCancellationDiagnosticPhase records a cheap breadcrumb of how far
// SessionEnd progressed before a possible kill: workspace resolution and
// store initialize are the two points most likely to hang against a large
// store, so those are the only phases recorded (#1972).
func updateHookCancellationDiagnosticPhase(path, phase string) error {
	return mutateHookCancellationDiagnostic(path, func(record *hookCancellationDiagnostic) {
		record.Phase = phase
	})
}

func mutateHookCancellationDiagnostic(path string, mutate func(*hookCancellationDiagnostic)) error {
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
	mutate(&record)
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
