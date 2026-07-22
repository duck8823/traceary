package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ application.OneShotRepairStore = (*StoreManagementDatasource)(nil)

const oneShotRepairCandidateQuery = `
SELECT s.started_at,
       s.ended_at,
       s.runtime_mode,
       s.terminal_reason,
       s.client,
       s.agent,
       s.workspace,
       COALESCE((SELECT e.created_at
                   FROM events e
                  WHERE e.session_id = s.session_id
                  ORDER BY e.created_at DESC, e.id DESC
                  LIMIT 1), s.started_at) AS latest_activity_at
  FROM sessions s
 WHERE s.session_id = ?`

const oneShotRepairStatsQuery = `
SELECT COALESCE(SUM(CASE WHEN s.ended_at IS NULL THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN s.ended_at IS NULL
                         AND ts_norm(COALESCE((SELECT e.created_at
                                                 FROM events e
                                                WHERE e.session_id = s.session_id
                                                ORDER BY e.created_at DESC, e.id DESC
                                                LIMIT 1), s.started_at)) <= ts_norm(?)
                         THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN s.ended_at IS NOT NULL AND s.terminal_reason = 'success' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN s.ended_at IS NOT NULL
                         AND s.terminal_reason IN ('failure', 'timeout', 'signal', 'aborted_stream')
                         THEN 1 ELSE 0 END), 0)
  FROM sessions s`

const applyOneShotRepairQuery = `
UPDATE sessions
   SET runtime_mode = 'one_shot',
       ended_at = ?,
       terminal_reason = ?,
       summary = CASE WHEN TRIM(summary) = '' THEN ? ELSE summary END
 WHERE session_id = ?
   AND ended_at IS NULL
   AND COALESCE(terminal_reason, '') = ''
   AND ts_norm(COALESCE((SELECT e.created_at
                           FROM events e
                          WHERE e.session_id = sessions.session_id
                          ORDER BY e.created_at DESC, e.id DESC
                          LIMIT 1), started_at)) <= ts_norm(?)
   AND ts_norm(COALESCE((SELECT e.created_at
                           FROM events e
                          WHERE e.session_id = sessions.session_id
                          ORDER BY e.created_at DESC, e.id DESC
                          LIMIT 1), started_at)) <= ts_norm(?)`

type oneShotRepairRecord struct {
	candidate apptypes.OneShotRepairCandidate
	startedAt time.Time
	client    types.Client
	agent     types.Agent
	workspace types.Workspace
}

// PreviewOneShotSessions evaluates every evidence entry in a read-only transaction.
func (d *StoreManagementDatasource) PreviewOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error) {
	return d.repairOneShotSessions(ctx, params, false)
}

// ApplyOneShotSessions evaluates and commits all eligible transitions in one transaction.
func (d *StoreManagementDatasource) ApplyOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error) {
	return d.repairOneShotSessions(ctx, params, true)
}

func (d *StoreManagementDatasource) repairOneShotSessions(ctx context.Context, params apptypes.OneShotRepairParams, apply bool) (apptypes.OneShotRepairResult, error) {
	var db *sql.DB
	var err error
	if apply {
		db, err = d.db.open(ctx)
	} else {
		db, err = d.db.openReadOnly(ctx)
	}
	if err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to open DB for one-shot repair: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !apply})
	if err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to begin one-shot repair transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cutoff := params.Now.Add(-params.StaleAfter)
	before, err := readOneShotRepairStats(ctx, tx, cutoff)
	if err != nil {
		return apptypes.OneShotRepairResult{}, err
	}
	records := make([]oneShotRepairRecord, 0, len(params.Entries))
	for _, entry := range params.Entries {
		record, err := inspectOneShotRepairEntry(ctx, tx, entry, cutoff)
		if err != nil {
			return apptypes.OneShotRepairResult{}, err
		}
		records = append(records, record)
	}

	if apply {
		for index := range records {
			if !records[index].candidate.Eligible {
				continue
			}
			if err := applyOneShotRepairRecord(ctx, tx, params.EvidenceHash, cutoff, &records[index]); err != nil {
				return apptypes.OneShotRepairResult{}, err
			}
		}
	}
	after := before
	if apply {
		after, err = readOneShotRepairStats(ctx, tx, cutoff)
		if err != nil {
			return apptypes.OneShotRepairResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to commit one-shot repair: %w", err)
	}

	candidates := make([]apptypes.OneShotRepairCandidate, 0, len(records))
	for _, record := range records {
		candidates = append(candidates, record.candidate)
	}
	return apptypes.OneShotRepairResult{
		EvidenceHash: params.EvidenceHash,
		Applied:      apply,
		Before:       before,
		After:        after,
		Candidates:   candidates,
	}, nil
}

func inspectOneShotRepairEntry(ctx context.Context, tx *sql.Tx, entry apptypes.OneShotRepairEvidenceEntry, cutoff time.Time) (oneShotRepairRecord, error) {
	candidate := apptypes.OneShotRepairCandidate{
		SessionID:      entry.SessionID,
		ProposedReason: entry.TerminalReason,
		CompletedAt:    entry.CompletedAt,
		EvidenceSource: entry.EvidenceSource,
		EvidenceRef:    entry.EvidenceRef,
		Decision:       "eligible",
	}
	var (
		startedAtValue      string
		endedAtValue        sql.NullString
		runtimeModeValue    string
		terminalReasonValue sql.NullString
		clientValue         string
		agentValue          string
		workspaceValue      string
		latestActivityValue string
	)
	err := tx.QueryRowContext(ctx, oneShotRepairCandidateQuery, entry.SessionID.String()).Scan(
		&startedAtValue, &endedAtValue, &runtimeModeValue, &terminalReasonValue,
		&clientValue, &agentValue, &workspaceValue, &latestActivityValue,
	)
	if err == sql.ErrNoRows {
		candidate.Decision = "missing_session"
		return oneShotRepairRecord{candidate: candidate}, nil
	}
	if err != nil {
		return oneShotRepairRecord{}, xerrors.Errorf("failed to inspect one-shot repair candidate %s: %w", entry.SessionID, err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, startedAtValue)
	if err != nil {
		return oneShotRepairRecord{}, xerrors.Errorf("failed to parse start time for one-shot repair candidate %s: %w", entry.SessionID, err)
	}
	latestActivity, err := time.Parse(time.RFC3339Nano, latestActivityValue)
	if err != nil {
		return oneShotRepairRecord{}, xerrors.Errorf("failed to parse latest activity for one-shot repair candidate %s: %w", entry.SessionID, err)
	}
	mode, err := types.RuntimeModeFrom(runtimeModeValue)
	if err != nil {
		return oneShotRepairRecord{}, xerrors.Errorf("failed to parse runtime mode for one-shot repair candidate %s: %w", entry.SessionID, err)
	}
	candidate.StoredRuntimeMode = mode
	candidate.LatestActivityAt = latestActivity
	record := oneShotRepairRecord{
		candidate: candidate,
		startedAt: startedAt,
		client:    types.Client(clientValue),
		agent:     types.Agent(agentValue),
		workspace: types.Workspace(workspaceValue),
	}
	switch {
	case (endedAtValue.Valid && strings.TrimSpace(endedAtValue.String) != "") ||
		(terminalReasonValue.Valid && strings.TrimSpace(terminalReasonValue.String) != ""):
		record.candidate.Decision = "already_terminal"
	case entry.CompletedAt.Before(startedAt):
		record.candidate.Decision = "completion_before_start"
	case entry.CompletedAt.Before(latestActivity):
		record.candidate.Decision = "completion_before_latest_activity"
	case latestActivity.After(cutoff):
		record.candidate.Decision = "recently_active"
	default:
		record.candidate.Eligible = true
	}
	return record, nil
}

func applyOneShotRepairRecord(ctx context.Context, tx *sql.Tx, evidenceHash string, cutoff time.Time, record *oneShotRepairRecord) error {
	candidate := &record.candidate
	summary := "repaired one-shot session: " + candidate.ProposedReason.String()
	result, err := tx.ExecContext(
		ctx,
		applyOneShotRepairQuery,
		formatTimestamp(candidate.CompletedAt),
		candidate.ProposedReason.String(),
		summary,
		candidate.SessionID.String(),
		formatTimestamp(candidate.CompletedAt),
		formatTimestamp(cutoff),
	)
	if err != nil {
		return xerrors.Errorf("failed to apply one-shot repair for %s: %w", candidate.SessionID, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to inspect one-shot repair update for %s: %w", candidate.SessionID, err)
	}
	if updated != 1 {
		return xerrors.Errorf("one-shot repair candidate %s changed concurrently: %w", candidate.SessionID, model.ErrInvalidSessionState)
	}
	eventDigest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s",
		evidenceHash, candidate.SessionID, candidate.ProposedReason, candidate.CompletedAt.UTC().Format(time.RFC3339Nano),
	)))
	event := model.EventOf(
		types.EventID(fmt.Sprintf("event-one-shot-repair-%x", eventDigest[:16])),
		types.EventKindSessionEnded,
		record.client,
		record.agent,
		candidate.SessionID,
		record.workspace,
		"session ended",
		candidate.CompletedAt,
	)
	event.SetSourceHook("one_shot_repair")
	if err := insertEventAndAudit(ctx, tx, event, nil); err != nil {
		return xerrors.Errorf("failed to append one-shot repair boundary for %s: %w", candidate.SessionID, err)
	}
	candidate.Applied = true
	candidate.Decision = "applied"
	return nil
}

func readOneShotRepairStats(ctx context.Context, tx *sql.Tx, cutoff time.Time) (apptypes.OneShotRepairStats, error) {
	var stats apptypes.OneShotRepairStats
	if err := tx.QueryRowContext(ctx, oneShotRepairStatsQuery, formatTimestamp(cutoff)).Scan(
		&stats.ActiveCount, &stats.StaleCount, &stats.CompletedCount, &stats.FailedCount,
	); err != nil {
		return apptypes.OneShotRepairStats{}, xerrors.Errorf("failed to read one-shot repair statistics: %w", err)
	}
	return stats, nil
}
