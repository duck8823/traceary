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
	// hookSpoolReplayBatchLimit is the minimum opportunistic drain batch when
	// remaining host budget is outside the low-headroom window.
	hookSpoolReplayBatchLimit = 5
	// hookSpoolReplayBacklogCap is the maximum opportunistic drain batch when
	// pending backlog is large. Time allowance still wins (0 / 1 in reserve).
	hookSpoolReplayBacklogCap = 32
	// hookSpoolDoctorDrainRoundLimit is one drain round inside doctor --fix.
	// Rounds repeat until the 45s wall or the queue is empty.
	hookSpoolDoctorDrainRoundLimit = 200
	// hookSpoolRetryLimit is the maximum number of delivery attempts for a
	// replayable spool record (first delivery + 2 retries). After this many
	// failed attempts the record is retained under spool/dead/ and excluded
	// from opportunistic drain so poison records cannot consume the batch.
	hookSpoolRetryLimit = 3
	// packagedHostBudget is the documented packaged host hook timeout. When
	// ctx has no deadline, remaining drain headroom is measured against this.
	packagedHostBudget = 10 * time.Second
	// hookSpoolDrainReserve is headroom kept for process exit after current
	// delivery. Opportunistic drain is skipped once remaining time is at or
	// below this reserve.
	hookSpoolDrainReserve = 2 * time.Second
	// hookSpoolDrainLowHeadroom is the remaining-time band where drain may
	// replay a single backlog record only (not a full batch).
	hookSpoolDrainLowHeadroom = 4 * time.Second
	// Packaged host hook budgets are 10 seconds. An in-flight record older than
	// one minute belongs to a process that did not finish its normal
	// success/failure transition and is safe to make replayable.
	hookSpoolInflightStaleAge = time.Minute
	// hookSpoolTmpStaleAge is how old a write-rename leftover (*.tmp) must
	// be before doctor counts it as stale_inflight. The publish window is
	// seconds; one hour is a generous bound so a live writer is never
	// counted (#2115).
	hookSpoolTmpStaleAge = time.Hour
	hookSpoolDeadDirName = "dead"
	// Dead-letter is append-only unless the operator asks doctor --fix to
	// prune files older than the retention window (#2007).
	hookSpoolDeadWarnCount    = 100
	hookSpoolDeadWarnBytes    = 10 * 1024 * 1024
	hookSpoolDeadRetention    = 14 * 24 * time.Hour
	hookSpoolDeadPruneLimit   = 200
	hookSpoolDeadRequeueLimit = 200
	// hookSpoolDeadRequeueDoctorWall bounds one whole doctor --fix apply
	// phase: dead-letter requeue, pending drain, and any later auto-fixes
	// share a single deadline. The per-batch cap stays
	// hookSpoolDeadRequeueLimit; loops continue while headroom remains,
	// matching hook-state residue / extract queue. The drain checks the wall
	// before each claim, so one in-flight replay may finish past the wall
	// (one-record slack) but a new SQLite replay never starts after it.
	hookSpoolDeadRequeueDoctorWall = 45 * time.Second
	// Claimed paths are pending/*.json.claim-<rand> so listHookSpoolRecordPaths
	// (suffix .json only) cannot pick them up while another process owns them.
	hookSpoolClaimMarker = ".claim-"
)

// hookSpoolDrainAllowance returns how many backlog records opportunistic drain
// may attempt given remaining host budget. Pure policy: no I/O.
//
//	remaining <= 2s → 0 (keep drainReserve for exit)
//	remaining <  4s → 1 (low headroom)
//	otherwise       → hookSpoolReplayBatchLimit
func hookSpoolDrainAllowance(remaining time.Duration) int {
	if remaining <= hookSpoolDrainReserve {
		return 0
	}
	if remaining < hookSpoolDrainLowHeadroom {
		return 1
	}
	return hookSpoolReplayBatchLimit
}

// hookSpoolBacklogDrainLimit scales opportunistic drain with pending count
// while preserving the 0 / 1 time-allowance bands.
func hookSpoolBacklogDrainLimit(pending int, remaining time.Duration) int {
	allowance := hookSpoolDrainAllowance(remaining)
	if allowance <= 1 {
		return allowance
	}
	if pending < 0 {
		pending = 0
	}
	scaled := pending / 100
	if scaled < hookSpoolReplayBatchLimit {
		scaled = hookSpoolReplayBatchLimit
	}
	if scaled > hookSpoolReplayBacklogCap {
		scaled = hookSpoolReplayBacklogCap
	}
	return scaled
}

// hookSpoolDrainRemaining is the wall-clock budget left for opportunistic
// drain. Prefer an explicit ctx deadline when the host provided one; otherwise
// assume the packaged 10s budget from startedAt.
func hookSpoolDrainRemaining(ctx context.Context, startedAt, now time.Time) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline.Sub(now)
	}
	return packagedHostBudget - now.Sub(startedAt)
}

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
	// AttemptCount tracks failed replay/requeue attempts. Missing field = 0
	// (never classified as a failed replay). schema_version stays 1.
	AttemptCount int `json:"attempt_count,omitempty"`
	// LastError is the most recent replay/requeue failure message. Doctor must
	// never print payload bodies; this field is for operator inspection only.
	LastError string `json:"last_error,omitempty"`
	Path      string `json:"-"`
}

// shouldDeadLetter reports whether a spool record with the given attempt count
// has exhausted hookSpoolRetryLimit and must leave the replayable queue.
func shouldDeadLetter(attempt int) bool {
	return attempt >= hookSpoolRetryLimit
}

// hookSpoolDeadLetterRequeueable reports whether a dead-letter last_error is
// safe to move back to pending. The set is conservative: store-timeout /
// lock / ping classes, plus the two load errors that #2050 and #2051 made
// replayable. Matching is case-insensitive and looks at last_error only.
func hookSpoolDeadLetterRequeueable(lastError string) bool {
	msg := strings.ToLower(strings.TrimSpace(lastError))
	if msg == "" {
		return false
	}
	for _, marker := range []string{
		"context deadline exceeded",
		"database is locked",
		"failed to ping sqlite db",
		"invalid kimi usage record metadata",
		"conflicting duplicate claude assistant usage",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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
		startedAt := time.Now()
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
		path, err := persistCurrentHookSpoolRecord(record)
		if err != nil {
			// Preserve existing fail-soft behavior when the state directory is
			// unavailable. The operational error remains visible in debug logs;
			// successful persistence is the timeout-kill guarantee.
			slog.Debug("hook spool persistence failed", "command", spec.Command, "client", spec.Client, "error", err)
			return run(newExplicitHookPayloadReader(payload))
		}
		if err := ctx.Err(); err != nil {
			if promoteErr := promoteCurrentHookSpoolRecord(path); promoteErr != nil && !os.IsNotExist(promoteErr) {
				slog.Debug("cancelled current hook spool promotion failed", "path", path, "error", promoteErr)
			}
			return xerrors.Errorf("hook context cancelled after spool persistence: %w", err)
		}
		if err := run(newExplicitHookPayloadReader(payload)); err != nil {
			if promoteErr := promoteCurrentHookSpoolRecord(path); promoteErr != nil && !os.IsNotExist(promoteErr) {
				slog.Debug("failed current hook spool promotion failed", "path", path, "error", promoteErr)
			}
			return err
		}
		if err := clearCurrentHookSpoolRecord(path); err != nil {
			return xerrors.Errorf("failed to clear committed hook spool record: %w", err)
		}
		// The current delivery is committed and its spool record is cleared
		// before backlog work can consume the remaining host timeout. Replay is
		// opportunistic and budget-capped: failures remain durable and never
		// change current delivery success. Drain is skipped entirely when the
		// remaining budget is inside the reserve window so host watchdogs do
		// not kill an already-successful hook.
		if ctx.Err() == nil {
			remaining := hookSpoolDrainRemaining(ctx, startedAt, time.Now())
			pending, pendingErr := countHookSpoolPendingPaths(time.Now().UTC())
			if pendingErr != nil {
				slog.Debug("hook spool drain pending count failed", "error", pendingErr)
				pending = 0
			}
			limit := hookSpoolBacklogDrainLimit(pending, remaining)
			if limit > 0 {
				if replayed, failed := c.drainHookSpoolRecords(ctx, limit); replayed > 0 || failed > 0 {
					slog.Debug("hook spool drain", "replayed", replayed, "failed", failed, "limit", limit, "pending", pending, "drained", replayed)
				}
			}
		}
		return nil
	})
}

type hookSpoolDrainResult struct {
	Replayed   int
	Failed     int
	Unreadable int
	Remaining  int
	Err        error
}

// drainHookSpoolRecords replays up to limit pending spool records in filename
// queue order. A record is removed only after replay returns nil. A failed
// record increments attempt_count and is either moved to the retry tail or,
// after hookSpoolRetryLimit attempts, retained under spool/dead/. Returns
// counts of successful replays and retained failures.
func (c *RootCLI) drainHookSpoolRecords(ctx context.Context, limit int) (replayed, failed int) {
	result := c.drainHookSpoolRecordsDetailed(ctx, limit, time.Time{})
	// Preserve the legacy aggregate used by opportunistic debug logging while
	// the structured result keeps replay failures and unreadable records
	// disjoint.
	return result.Replayed, result.Failed + result.Unreadable
}

// drainHookSpoolRecordsUntil drains pending records in rounds bounded by
// hookSpoolDoctorDrainRoundLimit until the queue is empty or the shared
// doctor --fix deadline is reached. The deadline is taken by the caller
// (fixHookSpoolRequeueThenDrain) so requeue and drain share one wall; a zero
// deadline disables the wall check.
func (c *RootCLI) drainHookSpoolRecordsUntil(ctx context.Context, pending int, deadline time.Time) hookSpoolDrainResult {
	aggregated := hookSpoolDrainResult{}
	remaining := pending
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			aggregated.Err = xerrors.Errorf("hook spool drain cancelled: %w", err)
			return aggregated
		}
		if !deadline.IsZero() && !hookSpoolDeadRequeueNow().Before(deadline) {
			aggregated.Remaining = remaining
			return aggregated
		}
		round := remaining
		if round > hookSpoolDoctorDrainRoundLimit {
			round = hookSpoolDoctorDrainRoundLimit
		}
		result := c.drainHookSpoolRecordsDetailed(ctx, round, deadline)
		if result.Err != nil {
			aggregated.Err = result.Err
			aggregated.Remaining = result.Remaining
			return aggregated
		}
		if result.Replayed == 0 {
			if aggregated.Replayed == 0 && aggregated.Failed == 0 && aggregated.Unreadable == 0 {
				return result
			}
			return aggregated
		}
		aggregated.Replayed += result.Replayed
		aggregated.Failed += result.Failed
		aggregated.Unreadable += result.Unreadable
		aggregated.Remaining = result.Remaining
		remaining = result.Remaining
	}
	return aggregated
}

func (c *RootCLI) drainHookSpoolRecordsDetailed(ctx context.Context, limit int, deadline time.Time) hookSpoolDrainResult {
	now := time.Now().UTC()
	if err := recoverStaleCurrentHookSpoolRecords(now); err != nil {
		return hookSpoolDrainResult{Err: err}
	}
	if err := recoverStaleClaimedHookSpoolRecords(now); err != nil {
		return hookSpoolDrainResult{Err: err}
	}
	if limit <= 0 {
		remaining, err := countHookSpoolPendingPaths(now)
		return hookSpoolDrainResult{Remaining: remaining, Err: err}
	}
	paths, err := listHookSpoolRecordPaths()
	if err != nil {
		return hookSpoolDrainResult{Err: err}
	}
	result := hookSpoolDrainResult{}
	claimed := 0
	for _, path := range paths {
		if claimed >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			break
		}
		// Doctor --fix wall: stop before claiming the next record once the
		// shared deadline is reached. An in-flight replay is never cancelled
		// mid-SQLite write (one-record slack); unclaimed records stay pending
		// and are counted in Remaining below.
		if !deadline.IsZero() && !hookSpoolDeadRequeueNow().Before(deadline) {
			break
		}
		// Exclusive claim via same-directory rename (CAS). Concurrent drainers
		// that lose the race see IsNotExist and skip — no shared mutex.
		claimedPath, ok, claimErr := claimHookSpoolRecord(path)
		if claimErr != nil {
			slog.Debug("hook spool claim failed", "path", path, "error", claimErr)
			continue
		}
		if !ok {
			continue
		}
		claimed++
		record, readable, loadErr := loadClaimedHookSpoolRecord(claimedPath, readHookSpoolFile)
		if loadErr != nil || !readable {
			result.Unreadable++
			if requeueErr := requeueHookSpoolRecord(claimedPath, "unreadable hook spool record"); requeueErr != nil {
				slog.Debug("unreadable hook spool requeue failed", "path", claimedPath, "error", requeueErr)
			}
			continue
		}
		// Crash-safety: claimed records already at the retry cap still must
		// leave the replay queue without consuming a delivery attempt.
		if shouldDeadLetter(record.AttemptCount) {
			if moveErr := renameHookSpoolRecordToDead(claimedPath); moveErr != nil {
				slog.Debug("terminal hook spool claim move failed", "path", claimedPath, "error", moveErr)
			}
			continue
		}
		if err := c.replayHookSpoolRecord(ctx, record); err != nil {
			result.Failed++
			slog.Debug("hook spool replay failed", "path", claimedPath, "command", record.Command, "client", record.Client, "error", err)
			// Observed last_error classes that still hit this path and
			// eventually dead-letter after the retry cap:
			//  1. "invalid Kimi usage record metadata" — still fail-closed
			//     for broken turn records (non-turn scopes are skipped)
			//  2. "conflicting duplicate Claude assistant usage" — historical
			//     last_error; Load now keeps first-seen and continues
			if requeueErr := requeueHookSpoolRecord(claimedPath, err.Error()); requeueErr != nil {
				slog.Debug("failed hook spool requeue failed", "path", claimedPath, "error", requeueErr)
			}
			continue
		}
		if err := os.Remove(claimedPath); err != nil && !os.IsNotExist(err) {
			result.Failed++
			slog.Debug("hook spool clear failed after replay", "path", claimedPath, "error", err)
			if requeueErr := requeueHookSpoolRecord(claimedPath, err.Error()); requeueErr != nil {
				slog.Debug("committed hook spool requeue failed", "path", claimedPath, "error", requeueErr)
			}
			continue
		}
		result.Replayed++
	}
	result.Remaining, result.Err = countHookSpoolRecordPaths()
	return result
}

// Spool replay passes a nil writer, not io.Discard: wake injection (#1684) marks
// the session as injected once it has written, so a discarded write would
// consume the marker and silence the live firing. A nil writer makes injection
// a no-op.
func (c *RootCLI) replayHookSpoolRecord(ctx context.Context, record hookSpoolRecord) error {
	input := newExplicitHookPayloadReader([]byte(record.Payload))
	dbPath := record.DBPath
	client := record.Client
	action := record.Action
	switch strings.TrimSpace(record.Command) {
	case "session":
		return c.runHookSession(ctx, nil, input, client, action, dbPath)
	case "audit":
		return c.runHookAudit(ctx, input, client, dbPath)
	case "compact":
		return c.runHookCompact(ctx, nil, input, client, action, dbPath)
	case "subagent-start":
		return c.runHookSubagentStart(ctx, input, client, dbPath)
	case "subagent-stop":
		return c.runHookSubagentStop(ctx, input, client, dbPath)
	case "prompt":
		return c.runHookPrompt(ctx, input, client, dbPath)
	case "transcript":
		_, err := c.runHookTranscript(ctx, input, client, dbPath)
		return err
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
		return c.runHookAntigravityPreInvocation(ctx, nil, input, dbPath)
	case "pre-tool-use":
		return c.runHookAntigravityPreToolUse(ctx, nil, input, dbPath)
	case "post-tool-use":
		return c.runHookAntigravityPostToolUse(ctx, nil, input, dbPath)
	case "stop":
		return c.runHookAntigravityStop(ctx, nil, input, dbPath)
	default:
		return xerrors.Errorf("unsupported antigravity spool action: %s", action)
	}
}

func (c *RootCLI) replayGrokSpoolRecord(ctx context.Context, input io.Reader, action, dbPath string) error {
	switch strings.TrimSpace(action) {
	case "session-start":
		return c.runHookGrokSessionStart(ctx, nil, input, dbPath)
	case "user-prompt-submit":
		return c.runHookGrokUserPromptSubmit(ctx, nil, input, dbPath)
	case "pre-tool-use":
		return c.runHookGrokPreToolUse(ctx, nil, input, dbPath)
	case "post-tool-use":
		return c.runHookGrokPostToolUse(ctx, nil, input, dbPath)
	case "stop":
		return c.runHookGrokStop(ctx, nil, input, dbPath)
	case "pre-compact":
		return c.runHookGrokPreCompact(ctx, nil, input, dbPath)
	case "post-compact":
		return c.runHookGrokPostCompact(ctx, nil, input, dbPath)
	default:
		return xerrors.Errorf("unsupported grok spool action: %s", action)
	}
}

// nil writer for the same reason as replayGrokSpoolRecord.
func (c *RootCLI) replayKimiSpoolRecord(ctx context.Context, input io.Reader, action, dbPath string) error {
	switch strings.TrimSpace(action) {
	case "session-start":
		return c.runHookKimiSessionStart(ctx, nil, input, dbPath)
	case "session-end":
		return c.runHookKimiSessionEnd(ctx, nil, input, dbPath)
	case "user-prompt-submit":
		return c.runHookKimiUserPromptSubmit(ctx, nil, input, dbPath)
	case "pre-tool-use":
		return c.runHookKimiPreToolUse(ctx, nil, input, dbPath)
	case "post-tool-use":
		return c.runHookKimiPostToolUse(ctx, nil, input, dbPath)
	case "post-tool-use-failure":
		return c.runHookKimiPostToolUseFailure(ctx, nil, input, dbPath)
	case "stop":
		// Spool replay only needs durable side effects; consolidation is a
		// live host-facing exit and is owned by newHookKimiStopCommand.
		_, _, err := c.runHookKimiStop(ctx, input, dbPath)
		return err

	case "subagent-stop":
		return c.runHookKimiSubagentStop(ctx, nil, input, dbPath)
	case "pre-compact":
		return c.runHookKimiPreCompact(ctx, nil, input, dbPath)
	case "post-compact":
		return c.runHookKimiPostCompact(ctx, nil, input, dbPath)
	default:
		return xerrors.Errorf("unsupported kimi spool action: %s", action)
	}
}

func persistHookSpoolRecord(record hookSpoolRecord) (string, error) {
	return persistHookSpoolRecordWithSuffix(record, ".json")
}

func persistCurrentHookSpoolRecord(record hookSpoolRecord) (string, error) {
	return persistHookSpoolRecordWithSuffix(record, ".inflight")
}

func persistHookSpoolRecordWithSuffix(record hookSpoolRecord, suffix string) (string, error) {
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
	base := fmt.Sprintf(
		"%s-%s-%s%s",
		record.CreatedAt.Format("20060102T150405.000000000Z"),
		sanitizeHookStateKey(record.Client),
		hex.EncodeToString(random),
		suffix,
	)
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

func replayablePathForCurrentHookSpool(path string) string {
	return strings.TrimSuffix(path, ".inflight") + ".json"
}

func promoteCurrentHookSpoolRecord(path string) error {
	if !strings.HasSuffix(path, ".inflight") {
		return xerrors.New("current hook spool path must end with .inflight")
	}
	if err := os.Rename(path, replayablePathForCurrentHookSpool(path)); err != nil {
		return xerrors.Errorf("failed to promote current hook spool record: %w", err)
	}
	return nil
}

func clearCurrentHookSpoolRecord(path string) error {
	if err := os.Remove(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return xerrors.Errorf("failed to remove in-flight hook spool record: %w", err)
	}
	// A stale-recovery race may already have promoted the record. A successful
	// current commit owns that duplicate and may safely clear it as well.
	if err := os.Remove(replayablePathForCurrentHookSpool(path)); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to remove promoted current hook spool record: %w", err)
	}
	return nil
}

func recoverStaleCurrentHookSpoolRecords(now time.Time) error {
	dir, err := hookSpoolDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".inflight") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return xerrors.Errorf("failed to inspect in-flight hook spool record: %w", err)
		}
		if now.Sub(info.ModTime()) < hookSpoolInflightStaleAge {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := promoteCurrentHookSpoolRecord(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// isHookSpoolClaimFile reports whether name is a drain claim
// (basename.json.claim-<rand>) that listHookSpoolRecordPaths must ignore.
func isHookSpoolClaimFile(name string) bool {
	idx := strings.LastIndex(name, hookSpoolClaimMarker)
	if idx < 0 {
		return false
	}
	return strings.HasSuffix(name[:idx], ".json")
}

// claimHookSpoolRecord exclusively claims a pending *.json record by renaming
// it to a non-replayable claim path in the same directory. ok=false means
// another worker already claimed or removed the path (lost CAS).
func claimHookSpoolRecord(path string) (claimedPath string, ok bool, err error) {
	if strings.TrimSpace(path) == "" {
		return "", false, xerrors.New("hook spool path is required")
	}
	if !strings.HasSuffix(path, ".json") {
		return "", false, xerrors.New("hook spool claim source must end with .json")
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", false, xerrors.Errorf("failed to generate hook spool claim ID: %w", err)
	}
	claimedPath = path + hookSpoolClaimMarker + hex.EncodeToString(random)
	if err := os.Rename(path, claimedPath); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, xerrors.Errorf("failed to claim hook spool record: %w", err)
	}
	// Rename preserves the original mtime. Bump claim times to now so a
	// long-pending record is not immediately treated as a stale claim by
	// recoverStaleClaimedHookSpoolRecords while this process still owns it.
	now := time.Now()
	if err := os.Chtimes(claimedPath, now, now); err != nil {
		slog.Debug("hook spool claim chtimes failed", "path", claimedPath, "error", err)
	}
	return claimedPath, true, nil
}

// recoverStaleClaimedHookSpoolRecords returns claim files left by a killed
// process to the replayable *.json queue after hookSpoolInflightStaleAge.
func recoverStaleClaimedHookSpoolRecords(now time.Time) error {
	dir, err := hookSpoolDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !isHookSpoolClaimFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return xerrors.Errorf("failed to inspect claimed hook spool record: %w", err)
		}
		if now.Sub(info.ModTime()) < hookSpoolInflightStaleAge {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := releaseStaleClaimedHookSpoolRecord(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func releaseStaleClaimedHookSpoolRecord(claimedPath string) error {
	base := filepath.Base(claimedPath)
	idx := strings.LastIndex(base, hookSpoolClaimMarker)
	if idx < 0 || !strings.HasSuffix(base[:idx], ".json") {
		return xerrors.New("claimed hook spool path is malformed")
	}
	restored := filepath.Join(filepath.Dir(claimedPath), base[:idx])
	if _, err := os.Lstat(restored); err == nil {
		// Basename already occupied; never rename-over another record.
		return renameHookSpoolRecordToRetryTail(claimedPath)
	} else if !os.IsNotExist(err) {
		return xerrors.Errorf("failed to inspect restored hook spool path: %w", err)
	}
	if err := os.Rename(claimedPath, restored); err != nil {
		if os.IsNotExist(err) {
			return xerrors.Errorf("claimed hook spool record disappeared during release: %w", err)
		}
		// Race: basename appeared between Lstat and Rename.
		return renameHookSpoolRecordToRetryTail(claimedPath)
	}
	return nil
}

// loadClaimedHookSpoolRecord reads a path already owned via claimHookSpoolRecord.
// readable=false means the bytes could not be parsed as a schema v1 record
// (caller should dead-letter / requeue without following symlinks).
func loadClaimedHookSpoolRecord(
	claimedPath string,
	readFile func(string) ([]byte, error),
) (hookSpoolRecord, bool, error) {
	data, err := readFile(claimedPath)
	if err != nil {
		return hookSpoolRecord{}, false, err
	}
	var record hookSpoolRecord
	if err := json.Unmarshal(data, &record); err != nil || record.SchemaVersion != hookSpoolSchemaVersion {
		return hookSpoolRecord{}, false, nil
	}
	record.Path = claimedPath
	return record, true, nil
}

func hookSpoolDir() (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "spool"), nil
}

func hookSpoolDeadDir() (string, error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hookSpoolDeadDirName), nil
}

func listHookSpoolRecordPaths() ([]string, error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		// Skip spool/dead/** and any other subdirectory; only top-level
		// replayable *.json files are candidates for drain.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func countHookSpoolRecordPaths() (int, error) {
	paths, err := listHookSpoolRecordPaths()
	return len(paths), err
}

func countHookSpoolPendingPaths(now time.Time) (int, error) {
	paths, err := listHookSpoolRecordPaths()
	if err != nil {
		return 0, err
	}
	dir, err := hookSpoolDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return len(paths), nil
	}
	if err != nil {
		return 0, xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	count := len(paths)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".inflight") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, xerrors.Errorf("failed to inspect in-flight hook spool record: %w", err)
		}
		if now.Sub(info.ModTime()) >= hookSpoolInflightStaleAge {
			count++
		}
	}
	return count, nil
}

func loadHookSpoolReplayBatch(
	limit int,
	readFile func(string) ([]byte, error),
) ([]hookSpoolRecord, []string, error) {
	if limit <= 0 {
		return nil, nil, nil
	}
	paths, err := listHookSpoolRecordPaths()
	if err != nil {
		return nil, nil, err
	}
	if len(paths) > limit {
		paths = paths[:limit]
	}
	records := make([]hookSpoolRecord, 0, len(paths))
	unreadable := make([]string, 0)
	for _, path := range paths {
		data, err := readFile(path)
		if err != nil {
			unreadable = append(unreadable, path)
			continue
		}
		var record hookSpoolRecord
		if err := json.Unmarshal(data, &record); err != nil || record.SchemaVersion != hookSpoolSchemaVersion {
			unreadable = append(unreadable, path)
			continue
		}
		// Records already at the retry cap must not consume drain budget.
		// They should already live under spool/dead/; this is a crash-safety filter.
		if shouldDeadLetter(record.AttemptCount) {
			continue
		}
		record.Path = path
		records = append(records, record)
	}
	return records, unreadable, nil
}

// writeHookSpoolRecordAtomic replaces path with the encoded record via
// same-directory tmp + rename so a crash leaves either the previous or the
// new content at path, never two distinct replayable files for one update.
func writeHookSpoolRecordAtomic(path string, record hookSpoolRecord) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode hook spool record: %w", err)
	}
	encoded = append(encoded, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o600); err != nil {
		return xerrors.Errorf("failed to write hook spool record: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return xerrors.Errorf("failed to publish hook spool record: %w", err)
	}
	return nil
}

// requeueHookSpoolRecord increments attempt_count after a failed replay or
// unreadable load, then either renames the record to the retry tail or moves
// it to spool/dead/ when shouldDeadLetter reports true. lastError is stored
// for operator inspection and must never be treated as a payload body by doctor.
//
// Transition safety: update the JSON in place (tmp + rename over the original),
// then rename that single file to zz-retry-* or spool/dead/*. Never write a
// second replayable path and delete the original — a crash between those steps
// would leave two copies of the same event.
//
// Unreadable paths (including external symlinks) must not be opened with a
// link-following API. They are renamed into spool/dead/ without copying content
// so the next drain cannot replay an external target payload.
func requeueHookSpoolRecord(path string, lastError string) error {
	if strings.TrimSpace(path) == "" {
		return xerrors.New("hook spool path is required")
	}
	data, err := readHookSpoolFile(path)
	if err != nil {
		// Do not fall back to os.ReadFile: that would follow symlinks and could
		// copy an external target into a new regular retry JSON.
		return renameHookSpoolRecordToDead(path)
	}
	record, ok := parseHookSpoolRecordForRequeue(data)
	if !ok {
		// Preserve raw bytes from the nofollow regular-file read only.
		record = hookSpoolRecord{
			SchemaVersion: hookSpoolSchemaVersion,
			Payload:       string(data),
			CreatedAt:     time.Now().UTC(),
		}
	}
	record.AttemptCount++
	if trimmed := strings.TrimSpace(lastError); trimmed != "" {
		record.LastError = trimmed
	}
	record.Path = ""

	// 1) Atomically update the single original path in place.
	if err := writeHookSpoolRecordAtomic(path, record); err != nil {
		return err
	}
	// 2) Rename that one file to the retry tail or dead-letter directory.
	if shouldDeadLetter(record.AttemptCount) {
		return renameHookSpoolRecordToDead(path)
	}
	return renameHookSpoolRecordToRetryTail(path)
}

func parseHookSpoolRecordForRequeue(data []byte) (hookSpoolRecord, bool) {
	var record hookSpoolRecord
	if err := json.Unmarshal(data, &record); err != nil || record.SchemaVersion != hookSpoolSchemaVersion {
		return hookSpoolRecord{}, false
	}
	return record, true
}

func renameHookSpoolRecordToRetryTail(path string) error {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return xerrors.Errorf("failed to generate hook spool retry ID: %w", err)
	}
	name := fmt.Sprintf(
		"zz-retry-%s-%s.json",
		time.Now().UTC().Format("20060102T150405.000000000Z"),
		hex.EncodeToString(random),
	)
	newPath := filepath.Join(filepath.Dir(path), name)
	if err := os.Rename(path, newPath); err != nil {
		return xerrors.Errorf("failed to move hook spool record to retry tail: %w", err)
	}
	return nil
}

func renameHookSpoolRecordToDead(path string) error {
	deadDir, err := hookSpoolDeadDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		return xerrors.Errorf("failed to create hook spool dead-letter directory: %w", err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return xerrors.Errorf("failed to generate hook spool dead-letter ID: %w", err)
	}
	name := fmt.Sprintf(
		"dead-%s-%s.json",
		time.Now().UTC().Format("20060102T150405.000000000Z"),
		hex.EncodeToString(random),
	)
	newPath := filepath.Join(deadDir, name)
	if err := os.Rename(path, newPath); err != nil {
		return xerrors.Errorf("failed to move hook spool record to dead-letter: %w", err)
	}
	return nil
}

// hookSpoolFilesystemStats is a metadata-only view of the spool directory.
// It uses directory entry counts and byte sizes only; it never opens record
// payloads. Required for the ≥2 GiB doctor large-store early path.
type hookSpoolFilesystemStats struct {
	PendingCount       int
	PendingBytes       int64
	StaleInflightCount int
	StaleInflightBytes int64
	DeadCount          int
	DeadBytes          int64
}

func inspectHookSpoolFilesystemStats(now time.Time) (hookSpoolFilesystemStats, error) {
	var stats hookSpoolFilesystemStats
	dir, err := hookSpoolDir()
	if err != nil {
		return stats, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return stats, nil
	}
	if err != nil {
		return stats, xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	for _, entry := range entries {
		if addErr := addHookSpoolFilesystemEntry(&stats, dir, entry, now); addErr != nil {
			return stats, addErr
		}
	}
	return stats, nil
}

func addHookSpoolFilesystemEntry(stats *hookSpoolFilesystemStats, dir string, entry os.DirEntry, now time.Time) error {
	name := entry.Name()
	path := filepath.Join(dir, name)
	if entry.IsDir() {
		if name != hookSpoolDeadDirName {
			return nil
		}
		deadCount, deadBytes, deadErr := countDirJSONEntries(path)
		if deadErr != nil {
			return deadErr
		}
		stats.DeadCount = deadCount
		stats.DeadBytes = deadBytes
		return nil
	}
	info, infoErr := entry.Info()
	if infoErr != nil {
		// A write-rename can remove *.tmp between ReadDir and Info.
		if os.IsNotExist(infoErr) && strings.HasSuffix(name, ".tmp") {
			return nil
		}
		return xerrors.Errorf("failed to inspect hook spool entry: %w", infoErr)
	}
	switch {
	case strings.HasSuffix(name, ".json"):
		stats.PendingCount++
		stats.PendingBytes += info.Size()
	case strings.HasSuffix(name, ".inflight"):
		if now.Sub(info.ModTime()) >= hookSpoolInflightStaleAge {
			stats.StaleInflightCount++
			stats.StaleInflightBytes += info.Size()
		}
	case strings.HasSuffix(name, ".tmp"):
		if now.Sub(info.ModTime()) >= hookSpoolTmpStaleAge {
			stats.StaleInflightCount++
			stats.StaleInflightBytes += info.Size()
		}
	}
	return nil
}

func countDirJSONEntries(dir string) (int, int64, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, xerrors.Errorf("failed to read directory: %w", err)
	}
	count := 0
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return 0, 0, xerrors.Errorf("failed to inspect directory entry: %w", infoErr)
		}
		count++
		total += info.Size()
	}
	return count, total, nil
}

func hookSpoolDeadOverThreshold(stats hookSpoolFilesystemStats) bool {
	return stats.DeadCount >= hookSpoolDeadWarnCount || stats.DeadBytes >= hookSpoolDeadWarnBytes
}

// hookSpoolDeadRequeueClock is the wall clock used by doctor --fix requeue
// loops. Tests replace it; production leaves it nil so time.Now.UTC is used.
var hookSpoolDeadRequeueClock func() time.Time

func hookSpoolDeadRequeueNow() time.Time {
	if hookSpoolDeadRequeueClock != nil {
		return hookSpoolDeadRequeueClock()
	}
	return time.Now().UTC()
}

func requeueHookSpoolDeadLetters(ctx context.Context, now time.Time, dryRun bool) (requeued, skipped, remaining int, err error) {
	return requeueHookSpoolDeadLettersLimited(ctx, now, dryRun, hookSpoolDeadRequeueLimit)
}

// requeueHookSpoolDeadLettersUntil loops bounded requeue batches until the
// dead-letter directory has no remaining transients or the shared doctor --fix
// deadline is reached. The deadline is taken by the caller
// (fixHookSpoolRequeueThenDrain) so requeue and drain share one wall; a zero
// deadline disables the wall check. Dry-run counts every requeueable record
// in one pass (no cap) so the preview is the full planned drain, not the
// first batch.
func requeueHookSpoolDeadLettersUntil(ctx context.Context, now time.Time, dryRun bool, deadline time.Time) (requeued, skipped, remaining int, err error) {
	if dryRun {
		return requeueHookSpoolDeadLettersLimited(ctx, now, true, 0)
	}
	for {
		if err := ctx.Err(); err != nil {
			return requeued, skipped, remaining, xerrors.Errorf("hook spool dead-letter requeue cancelled: %w", err)
		}
		if !deadline.IsZero() && !hookSpoolDeadRequeueNow().Before(deadline) {
			// Dry-run unlimited counts leftover transients as requeued, not
			// remaining. Fold them back so remaining is files still in dead/.
			wouldRequeue, leftoverSkipped, leftoverRemaining, inspectErr := requeueHookSpoolDeadLettersLimited(ctx, now, true, 0)
			return requeued, leftoverSkipped, leftoverRemaining + wouldRequeue, inspectErr
		}
		batchRequeued, batchSkipped, batchRemaining, batchErr := requeueHookSpoolDeadLettersLimited(ctx, now, false, hookSpoolDeadRequeueLimit)
		requeued += batchRequeued
		skipped = batchSkipped
		remaining = batchRemaining
		if batchErr != nil {
			return requeued, skipped, remaining, batchErr
		}
		if batchRequeued == 0 {
			return requeued, skipped, remaining, nil
		}
	}
}

func requeueHookSpoolDeadLettersLimited(ctx context.Context, now time.Time, dryRun bool, limit int) (requeued, skipped, remaining int, err error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return 0, 0, 0, err
	}
	deadDir := filepath.Join(dir, hookSpoolDeadDirName)
	entries, err := os.ReadDir(deadDir)
	if os.IsNotExist(err) {
		return 0, 0, 0, nil
	}
	if err != nil {
		return 0, 0, 0, xerrors.Errorf("failed to read hook spool dead-letter directory: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return requeued, skipped, remaining, xerrors.Errorf("hook spool dead-letter requeue cancelled: %w", err)
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(deadDir, entry.Name())
		data, readErr := readHookSpoolFile(path)
		if readErr != nil {
			skipped++
			remaining++
			continue
		}
		record, ok := parseHookSpoolRecordForRequeue(data)
		if !ok || !hookSpoolDeadLetterRequeueable(record.LastError) {
			skipped++
			remaining++
			continue
		}
		if limit > 0 && requeued >= limit {
			remaining++
			continue
		}
		if dryRun {
			requeued++
			continue
		}
		// Rename first so a crash cannot leave a dead file with last_error
		// cleared (that would make the record unreplayable until prune).
		random := make([]byte, 8)
		if _, randErr := rand.Read(random); randErr != nil {
			return requeued, skipped, remaining, xerrors.Errorf("failed to generate hook spool requeue ID: %w", randErr)
		}
		name := fmt.Sprintf(
			"zz-retry-%s-%s.json",
			now.UTC().Format("20060102T150405.000000000Z"),
			hex.EncodeToString(random),
		)
		dest := filepath.Join(dir, name)
		if renameErr := os.Rename(path, dest); renameErr != nil {
			return requeued, skipped, remaining, xerrors.Errorf("failed to requeue hook spool dead-letter: %w", renameErr)
		}
		record.AttemptCount = 0
		record.LastError = ""
		record.Path = ""
		if writeErr := writeHookSpoolRecordAtomic(dest, record); writeErr != nil {
			return requeued, skipped, remaining, writeErr
		}
		requeued++
	}
	return requeued, skipped, remaining, nil
}

func pruneHookSpoolDeadLetters(now time.Time, dryRun bool) (pruned, remaining int, err error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return 0, 0, err
	}
	deadDir := filepath.Join(dir, hookSpoolDeadDirName)
	entries, err := os.ReadDir(deadDir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, xerrors.Errorf("failed to read hook spool dead-letter directory: %w", err)
	}
	cutoff := now.Add(-hookSpoolDeadRetention)
	remaining = 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return pruned, remaining, xerrors.Errorf("failed to inspect dead-letter file: %w", infoErr)
		}
		if !info.ModTime().Before(cutoff) {
			remaining++
			continue
		}
		if pruned >= hookSpoolDeadPruneLimit {
			remaining++
			continue
		}
		if dryRun {
			pruned++
			continue
		}
		path := filepath.Join(deadDir, entry.Name())
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return pruned, remaining, xerrors.Errorf("failed to prune dead-letter file: %w", removeErr)
		}
		pruned++
	}
	return pruned, remaining, nil
}

func pruneHookSpoolOrphanTmpFiles(now time.Time, dryRun bool) (pruned, remaining int, err error) {
	dir, err := hookSpoolDir()
	if err != nil {
		return 0, 0, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, xerrors.Errorf("failed to read hook spool directory: %w", err)
	}
	cutoff := now.Add(-hookSpoolDeadRetention)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				continue
			}
			return pruned, remaining, xerrors.Errorf("failed to inspect hook spool tmp file: %w", infoErr)
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if pruned >= hookSpoolDeadPruneLimit {
			remaining++
			continue
		}
		if dryRun {
			pruned++
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return pruned, remaining, xerrors.Errorf("failed to prune orphan hook spool tmp file: %w", removeErr)
		}
		pruned++
	}
	return pruned, remaining, nil
}

// inspectHookSpoolFilesystemMetadata builds a doctor check from directory
// metadata only (entry counts + byte sizes). Safe on the large-store early
// path: inspect opens no SQLite. --fix requeues transient dead letters by
// reading last_error envelope fields only, then drains pending records via
// the shared fixer; that spool replay may open SQLite (never dbstat or
// capacity), while --fix --dry-run performs no file moves and opens nothing.
func (c *RootCLI) inspectHookSpoolFilesystemMetadata() doctorCheck {
	const name = "hook-spool"
	stats, err := inspectHookSpoolFilesystemStats(time.Now().UTC())
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusFail,
			Message: localizef("failed to inspect hook spool metadata: %v", "hook spool メタデータの検査に失敗しました: %v", err),
		}
	}
	pendingTotal := stats.PendingCount + stats.StaleInflightCount
	if pendingTotal == 0 && stats.DeadCount == 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("no pending or terminal hook spool files (metadata-only, store-independent)", "未処理および terminal の hook spool file はありません (metadata-only, store-independent)"),
		}
	}
	status := doctorStatusPass
	if pendingTotal > 0 || hookSpoolDeadOverThreshold(stats) {
		status = doctorStatusWarn
	}
	check := doctorCheck{
		Name:   name,
		Status: status,
		Message: localizef(
			"hook spool files (metadata-only, store-independent): pending=%d (%s), stale_inflight=%d (%s), dead=%d (%s)",
			"hook spool files (metadata-only, store-independent): pending=%d (%s), stale_inflight=%d (%s), dead=%d (%s)",
			stats.PendingCount,
			formatByteSize(stats.PendingBytes),
			stats.StaleInflightCount,
			formatByteSize(stats.StaleInflightBytes),
			stats.DeadCount,
			formatByteSize(stats.DeadBytes),
		),
		Hint: Localize(
			"metadata-only counts from the hook spool directory (no payload bodies). Pending records drain on later hook invocations. `traceary doctor --fix` requeues transient dead letters, drains pending records (spool replay may open SQLite; `--fix --dry-run` does not), and prunes dead-letter files and orphan *.tmp leftovers older than 14 days.",
			"hook spool ディレクトリのメタデータのみの件数です（payload body は読みません）。未処理 record は後続 hook で drain されます。`traceary doctor --fix` は transient dead-letter を再キューし、未処理 record を drain し（spool replay は SQLite を開くことがあります。`--fix --dry-run` は開きません）、14 日より古い dead-letter と orphan *.tmp を prune します。",
		),
	}
	if status == doctorStatusWarn {
		check.FixCommand = "traceary doctor --fix"
		check.AutoFixAvailable = true
		check.StructuredFixFunc = func(ctx context.Context, dryRun bool) (doctorFixResult, error) {
			return c.fixHookSpoolRequeueThenDrain(ctx, time.Now().UTC(), dryRun)
		}
		check.FixFunc = func(ctx context.Context, dryRun bool) (string, error) {
			result, err := c.fixHookSpoolRequeueThenDrain(ctx, time.Now().UTC(), dryRun)
			return result.Action, err
		}
	}
	return check
}

// fixHookSpoolRequeueThenDrain is the shared doctor --fix body for both the
// full-scan and the large-store metadata-only hook-spool checks: requeue
// transient dead letters, drain pending records through the existing spool
// replay, then prune aged dead-letter and orphan tmp files. Dry-run performs
// no file moves and does not open SQLite. Apply may open SQLite only for
// spool replay via drainHookSpoolRecordsUntil / replayHookSpoolRecord — never
// dbstat or store capacity. Apply takes one 45s deadline up front and shares
// it between requeue and drain, so the documented wall covers the whole
// apply including SQLite replay; the drain checks the wall before each claim
// and lets one in-flight replay finish (one-record slack). The Action always
// reports remaining= so a wall hit never hides leftover pending records.
func (c *RootCLI) fixHookSpoolRequeueThenDrain(ctx context.Context, now time.Time, dryRun bool) (doctorFixResult, error) {
	pending, err := countHookSpoolPendingPaths(now)
	if err != nil {
		return doctorFixResult{}, err
	}
	deadline := time.Time{}
	if !dryRun {
		deadline = hookSpoolDeadRequeueNow().Add(hookSpoolDeadRequeueDoctorWall)
	}
	requeued, skippedNontransient, _, err := requeueHookSpoolDeadLettersUntil(ctx, now, dryRun, deadline)
	if err != nil {
		return doctorFixResult{}, err
	}
	pruned, deadRemaining, err := pruneHookSpoolDeadLetters(now, dryRun)
	if err != nil {
		return doctorFixResult{}, err
	}
	prunedTmp, _, err := pruneHookSpoolOrphanTmpFiles(now, dryRun)
	if err != nil {
		return doctorFixResult{}, err
	}
	if dryRun {
		return doctorFixResult{Action: localizef(
			"would requeue %d transient dead-letter(s), skip %d non-transient, drain up to %d pending hook spool record(s), prune %d aged dead-letter file(s), and prune %d orphan tmp file(s)",
			"transient dead-letter %d 件を再キューし、非 transient %d 件をスキップし、未処理 hook spool record 最大 %d 件を drain し、古い dead-letter %d 件と orphan tmp %d 件を prune します",
			requeued,
			skippedNontransient,
			pending+requeued,
			pruned,
			prunedTmp,
		)}, nil
	}
	result := c.drainHookSpoolRecordsUntil(ctx, pending+requeued, deadline)
	fixed := doctorFixResult{
		Action: localizef(
			"drained hook spool: requeued=%d skipped_nontransient=%d replayed=%d failed=%d unreadable=%d remaining=%d; pruned_dead=%d dead_remaining=%d pruned_tmp=%d",
			"hook spool を drain しました: requeued=%d skipped_nontransient=%d replayed=%d failed=%d unreadable=%d remaining=%d; pruned_dead=%d dead_remaining=%d pruned_tmp=%d",
			requeued,
			skippedNontransient,
			result.Replayed,
			result.Failed,
			result.Unreadable,
			result.Remaining,
			pruned,
			deadRemaining,
			prunedTmp,
		),
		Metrics: map[string]int{
			"requeued":             requeued,
			"skipped_nontransient": skippedNontransient,
			"replayed":             result.Replayed,
			"failed":               result.Failed,
			"remaining":            result.Remaining,
			"unreadable":           result.Unreadable,
			"pruned_dead":          pruned,
			"dead_remaining":       deadRemaining,
			"pruned_tmp":           prunedTmp,
		},
	}
	// Even on a drain error the populated result is returned so the operator
	// sees the counts (including remaining=) alongside the error.
	return fixed, result.Err
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
		// spool/dead/** is terminal retention and is reported via metadata, not
		// as pending replay candidates.
		if entry.IsDir() {
			continue
		}
		isReplayable := strings.HasSuffix(entry.Name(), ".json")
		if !isReplayable && strings.HasSuffix(entry.Name(), ".inflight") {
			info, infoErr := entry.Info()
			if infoErr != nil {
				unreadable = append(unreadable, filepath.Join(dir, entry.Name()))
				continue
			}
			isReplayable = time.Since(info.ModTime()) >= hookSpoolInflightStaleAge
		}
		if !isReplayable {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := readHookSpoolFile(path)
		if err != nil {
			unreadable = append(unreadable, path)
			continue
		}
		var record hookSpoolRecord
		if err := json.Unmarshal(data, &record); err != nil || record.SchemaVersion != hookSpoolSchemaVersion {
			unreadable = append(unreadable, path)
			continue
		}
		if shouldDeadLetter(record.AttemptCount) {
			// Terminal records left in the pending directory must not appear as
			// drain candidates; dead/ is the retained location after requeue.
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
	stats, statsErr := inspectHookSpoolFilesystemStats(time.Now().UTC())
	if statsErr != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect hook spool metadata: %v", "hook spool メタデータの検査に失敗しました: %v", statsErr)}
	}
	if len(records) == 0 && len(unreadable) == 0 && stats.DeadCount == 0 && stats.StaleInflightCount == 0 {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusPass,
			Message: localizef(
				"no decoded pending hook spool records for the selected doctor client(s); filesystem pending files (store-independent)=%d",
				"選択中の doctor client 向け decoded pending hook spool record はありません。filesystem pending files (store-independent)=%d",
				stats.PendingCount,
			),
		}
	}
	structuredFix := func(ctx context.Context, dryRun bool) (doctorFixResult, error) {
		return c.fixHookSpoolRequeueThenDrain(ctx, time.Now().UTC(), dryRun)
	}
	latest := "-"
	if len(records) > 0 {
		latest = fmt.Sprintf("client=%s command=%s action=%s created_at=%s path=%s", emptyAsDash(records[0].Client), emptyAsDash(records[0].Command), emptyAsDash(records[0].Action), records[0].CreatedAt.Format(time.RFC3339Nano), records[0].Path)
	}
	status := doctorStatusPass
	if len(records) > 0 || len(unreadable) > 0 || hookSpoolDeadOverThreshold(stats) || stats.StaleInflightCount > 0 {
		status = doctorStatusWarn
	}
	message := localizef(
		"found %d decoded pending hook spool record(s) for the selected doctor client(s), %d terminal dead-letter record(s), %d unreadable record(s), and %d stale inflight file(s); filesystem pending files (store-independent)=%d; latest %s",
		"選択中の doctor client 向け decoded pending hook spool record が %d 件、terminal dead-letter が %d 件、読めない record が %d 件、stale inflight が %d 件あります。filesystem pending files (store-independent)=%d。latest %s",
		len(records), stats.DeadCount, len(unreadable), stats.StaleInflightCount, stats.PendingCount, latest,
	)
	check := doctorCheck{
		Name:    name,
		Status:  status,
		Message: message,
		Hint: Localize(
			"records are drained automatically on later hook invocations (bounded batch). After 3 failed attempts a record is retained under spool/dead/. Run `traceary doctor --fix` to requeue transient dead letters, drain pending records, and prune dead-letter files and orphan *.tmp leftovers older than 14 days. Doctor never deletes those files without --fix.",
			"record は後続 hook 呼び出し時に bounded batch で自動 drain されます。3 回失敗すると spool/dead/ に保持されます。`traceary doctor --fix` で transient dead-letter を再キューし、未処理を drain し、14 日より古い dead-letter と orphan *.tmp を prune します。--fix なしではそれらを削除しません。",
		),
	}
	if status == doctorStatusWarn {
		check.FixCommand = "traceary doctor --fix"
		check.AutoFixAvailable = true
		check.FixFunc = func(ctx context.Context, dryRun bool) (string, error) {
			result, err := structuredFix(ctx, dryRun)
			return result.Action, err
		}
		check.StructuredFixFunc = structuredFix
	}
	return check
}
