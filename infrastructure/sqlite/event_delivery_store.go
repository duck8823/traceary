package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const maxDeliveryDecisionAttempts = 3
const eventDeliveryBusyRetryDelay = 25 * time.Millisecond

var errHookDeliveryIdentityRace = errors.New("hook delivery identity race")

type hookDeliveryRow struct {
	deliveryRecordID    string
	deliveryFingerprint string
	identityStatus      string
	observedEventID     string
}

// saveEventTransaction owns the delivery decision and every write that must
// be atomic with a new event. afterInsert is called only when this attempt
// inserted a new event; session boundaries use it to persist session state in
// the same transaction. An exact redelivery may still add one supplemental
// observation, then commits without invoking afterInsert.
func saveEventTransaction(
	ctx context.Context,
	db *sql.DB,
	event *model.Event,
	audit *model.CommandAudit,
	codecMetadata bool,
	afterInsert func(context.Context, *sql.Tx) error,
) error {
	for attempt := 0; attempt < maxDeliveryDecisionAttempts; attempt++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return xerrors.Errorf("failed to begin event delivery transaction: %w", err)
		}

		inserted, persistErr := persistEventDelivery(ctx, tx, event, audit, codecMetadata)
		if persistErr == nil && inserted && afterInsert != nil {
			persistErr = afterInsert(ctx, tx)
		}
		if persistErr != nil {
			rollbackErr := tx.Rollback()
			if errors.Is(persistErr, errHookDeliveryIdentityRace) || isSQLiteBusy(persistErr) {
				if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
					return xerrors.Errorf("failed to rollback retryable event delivery: %w", rollbackErr)
				}
				if attempt == maxDeliveryDecisionAttempts-1 {
					return xerrors.Errorf("event delivery remained retryable after %d attempts: %w", maxDeliveryDecisionAttempts, persistErr)
				}
				if isSQLiteBusy(persistErr) {
					timer := time.NewTimer(eventDeliveryBusyRetryDelay)
					select {
					case <-ctx.Done():
						if !timer.Stop() {
							<-timer.C
						}
						return xerrors.Errorf("event delivery retry cancelled: %w", ctx.Err())
					case <-timer.C:
					}
				}
				continue
			}
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				return xerrors.Errorf("event delivery failed (%v) and rollback failed: %w", persistErr, rollbackErr)
			}
			return persistErr
		}
		if err := tx.Commit(); err != nil {
			return xerrors.Errorf("failed to commit event delivery transaction: %w", err)
		}
		return nil
	}
	return xerrors.Errorf("hook delivery identity remained unstable after %d attempts", maxDeliveryDecisionAttempts)
}

func persistEventDelivery(
	ctx context.Context,
	tx *sql.Tx,
	event *model.Event,
	audit *model.CommandAudit,
	codecMetadata bool,
) (bool, error) {
	if event == nil {
		return false, xerrors.Errorf("event must not be nil")
	}

	evidence, hasDelivery := event.DeliveryEvidence().Value()
	identityStatus := ""
	if hasDelivery {
		existing, found, err := findHookDeliveryByFingerprint(ctx, tx, event.SessionID(), evidence)
		if err != nil {
			return false, err
		}
		if found {
			if err := insertHookDeliveryAttempt(ctx, tx, event, existing.deliveryRecordID, "exact_redelivery"); err != nil {
				return false, err
			}
			if err := insertWorkspaceObservation(ctx, tx, event, existing.observedEventID, existing.deliveryRecordID, "supplemental", "runtime", diagnosticReason(existing.identityStatus), evidence.AttributionFingerprint()); err != nil {
				return false, err
			}
			return false, nil
		}

		_, acceptedFound, err := findAcceptedHookDelivery(ctx, tx, event.SessionID(), evidence.ReportedID())
		if err != nil {
			return false, err
		}
		identityStatus = "accepted"
		if acceptedFound {
			identityStatus = "conflict"
		}
		if err := insertHookDelivery(ctx, tx, event, evidence, identityStatus); err != nil {
			if isSQLiteUniqueOrPKConflict(err) {
				return false, errHookDeliveryIdentityRace
			}
			return false, err
		}
		if err := insertHookDeliveryAttempt(ctx, tx, event, evidence.DeliveryRecordID(), identityStatus); err != nil {
			return false, err
		}
	}

	if err := insertEventAndAudit(ctx, tx, event, audit, codecMetadata); err != nil {
		return false, err
	}
	if err := appendAttestationLink(ctx, tx, event, audit); err != nil {
		return false, err
	}

	deliveryRecordID := ""
	attributionFingerprint := model.WorkspaceAttributionFingerprint(event.Workspace(), event.RawWorkspace())
	if hasDelivery {
		deliveryRecordID = evidence.DeliveryRecordID()
		attributionFingerprint = evidence.AttributionFingerprint()
	}
	if err := insertWorkspaceObservation(ctx, tx, event, event.EventID().String(), deliveryRecordID, "primary", "runtime", diagnosticReason(identityStatus), attributionFingerprint); err != nil {
		return false, err
	}
	return true, nil
}

func insertHookDeliveryAttempt(ctx context.Context, tx *sql.Tx, event *model.Event, deliveryRecordID, outcome string) error {
	enabled, err := tableExistsInTransaction(ctx, tx, "hook_delivery_attempts")
	if err != nil {
		return err
	}
	if !enabled {
		// Focused historical-schema tests may omit migration 23. Runtime stores
		// always initialize before hook writes.
		return nil
	}
	// A hook callback receives a fresh Traceary event ID. OR IGNORE collapses
	// only a repository retry of that same event object after a transaction
	// race; it does not collapse a later callback carrying a new event ID.
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO hook_delivery_attempts (
			delivery_record_id, attempted_event_id, outcome, attempt_origin, observed_at
		) VALUES (?, ?, ?, 'runtime', ?)`,
		deliveryRecordID,
		event.EventID().String(),
		outcome,
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("failed to insert hook delivery attempt: %w", err)
	}
	return nil
}

func insertEventAndAudit(ctx context.Context, tx *sql.Tx, event *model.Event, audit *model.CommandAudit, codecMetadata bool) error {
	eventPayload, err := encodeCanonicalPayload([]byte(event.Body()), codecMetadata)
	if err != nil {
		return xerrors.Errorf("encode event payload: %w", err)
	}
	eventHasCodec := codecMetadata
	eventQuery := insertEventQuery
	eventArgs := []any{
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		storedBodyArg(eventPayload),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook())}
	if eventHasCodec {
		eventQuery = `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		eventArgs = append(eventArgs, eventPayload.Codec, eventPayload.FormatVersion, eventPayload.PlaintextBytes, eventPayload.StoredBytes, eventPayload.SHA256)
	}
	if _, err := tx.ExecContext(ctx, eventQuery, eventArgs...); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}
	if audit == nil {
		return nil
	}
	commandPayload, err := encodeCanonicalPayload([]byte(audit.Command()), codecMetadata)
	if err != nil {
		return xerrors.Errorf("encode command payload: %w", err)
	}
	inputPayload, err := encodeCanonicalPayload([]byte(audit.Input()), codecMetadata)
	if err != nil {
		return xerrors.Errorf("encode command input payload: %w", err)
	}
	outputPayload, err := encodeCanonicalPayload([]byte(audit.Output()), codecMetadata)
	if err != nil {
		return xerrors.Errorf("encode command output payload: %w", err)
	}
	var exitCodeSQL *int
	if exitCode, ok := audit.ExitCode().Value(); ok {
		exitCodeSQL = &exitCode
	}
	var wrapper string
	if value, ok := audit.CommandIdentity().Wrapper().Value(); ok {
		wrapper = value.String()
	}
	auditHasCodec := codecMetadata
	auditQuery := insertCommandAuditQuery
	auditArgs := []any{
		audit.EventID().String(),
		storedBodyArg(commandPayload),
		wrapper,
		audit.CommandIdentity().Command().String(),
		storedBodyArg(inputPayload),
		storedBodyArg(outputPayload),
		audit.InputTruncated(),
		audit.OutputTruncated(),
		audit.InputOriginalBytes(),
		audit.OutputOriginalBytes(),
		exitCodeSQL,
		audit.Failed(),
		audit.FailureReason().String()}
	if auditHasCodec {
		auditQuery = `INSERT INTO command_audits(event_id, command_text, command_wrapper, command_name, input_text, output_text,
input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed, failure_reason,
command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256,
input_codec, input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256,
output_codec, output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		auditArgs = append(auditArgs,
			commandPayload.Codec, commandPayload.FormatVersion, commandPayload.PlaintextBytes, commandPayload.StoredBytes, commandPayload.SHA256,
			inputPayload.Codec, inputPayload.FormatVersion, inputPayload.PlaintextBytes, inputPayload.StoredBytes, inputPayload.SHA256,
			outputPayload.Codec, outputPayload.FormatVersion, outputPayload.PlaintextBytes, outputPayload.StoredBytes, outputPayload.SHA256)
	}
	if _, err := tx.ExecContext(ctx, auditQuery, auditArgs...); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}
	return nil
}

func transactionColumnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, xerrors.Errorf("inspect payload metadata column: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

func findHookDeliveryByFingerprint(
	ctx context.Context,
	tx *sql.Tx,
	sessionID types.SessionID,
	evidence model.HookDeliveryEvidence,
) (hookDeliveryRow, bool, error) {
	return scanHookDelivery(tx.QueryRowContext(
		ctx,
		`SELECT delivery_record_id, delivery_fingerprint, identity_status, observed_event_id
		   FROM hook_deliveries
		  WHERE session_id = ? AND reported_delivery_id = ? AND delivery_fingerprint = ?`,
		sessionID.String(), evidence.ReportedID(), evidence.DeliveryFingerprint(),
	))
}

func findAcceptedHookDelivery(
	ctx context.Context,
	tx *sql.Tx,
	sessionID types.SessionID,
	reportedID string,
) (hookDeliveryRow, bool, error) {
	return scanHookDelivery(tx.QueryRowContext(
		ctx,
		`SELECT delivery_record_id, delivery_fingerprint, identity_status, observed_event_id
		   FROM hook_deliveries
		  WHERE session_id = ? AND reported_delivery_id = ? AND identity_status = 'accepted'`,
		sessionID.String(), reportedID,
	))
}

func scanHookDelivery(row *sql.Row) (hookDeliveryRow, bool, error) {
	var result hookDeliveryRow
	if err := row.Scan(&result.deliveryRecordID, &result.deliveryFingerprint, &result.identityStatus, &result.observedEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return hookDeliveryRow{}, false, nil
		}
		return hookDeliveryRow{}, false, xerrors.Errorf("failed to read hook delivery ledger: %w", err)
	}
	return result, true, nil
}

func insertHookDelivery(
	ctx context.Context,
	tx *sql.Tx,
	event *model.Event,
	evidence model.HookDeliveryEvidence,
	identityStatus string,
) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO hook_deliveries (
			delivery_record_id, session_id, reported_delivery_id, delivery_fingerprint,
			identity_status, observed_event_id, accepted_at, source_client, source_hook
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evidence.DeliveryRecordID(),
		event.SessionID().String(),
		evidence.ReportedID(),
		evidence.DeliveryFingerprint(),
		identityStatus,
		event.EventID().String(),
		formatTimestamp(event.CreatedAt()),
		rootSourceClient(event.Agent().String()),
		event.SourceHook(),
	); err != nil {
		return xerrors.Errorf("failed to insert hook delivery ledger row: %w", err)
	}
	return nil
}

func insertWorkspaceObservation(
	ctx context.Context,
	tx *sql.Tx,
	event *model.Event,
	observedEventID, deliveryRecordID, kind, origin, reason, attributionFingerprint string,
) error {
	enabled, err := tableExistsInTransaction(ctx, tx, "session_workspace_observations")
	if err != nil {
		return err
	}
	if !enabled {
		// A few datasource-level tests deliberately install a minimal historical
		// schema. Runtime databases always reach migration 22 before writes, while
		// the compatibility path keeps those focused fixtures useful.
		return nil
	}
	relationship, err := workspaceRelationshipForEvent(ctx, tx, event)
	if err != nil {
		return err
	}
	rawWorkspace := event.RawWorkspace()
	observationID := "event:" + observedEventID
	if evidence, ok := event.DeliveryEvidence().Value(); ok {
		observationID = evidence.ObservationID()
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO session_workspace_observations (
			observation_id, session_id, workspace, raw_workspace, observation_kind,
			observation_origin, observed_relationship, observed_event_id,
			delivery_record_id, attribution_fingerprint, diagnostic_reason,
			observed_at, source_client, source_hook
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		observationID,
		event.SessionID().String(),
		event.Workspace().String(),
		nullableString(rawWorkspace),
		kind,
		origin,
		string(relationship),
		observedEventID,
		nullableString(deliveryRecordID),
		attributionFingerprint,
		reason,
		formatTimestamp(event.CreatedAt()),
		rootSourceClient(event.Agent().String()),
		event.SourceHook(),
	)
	if err != nil {
		if deliveryRecordID != "" && isSQLiteUniqueOrPKConflict(err) {
			// Primary and unchanged-retry observations intentionally mint the
			// same delivery+attribution ID. The collision is the database-backed
			// idempotent no-op; changed attribution has a different ID and inserts.
			return nil
		}
		return xerrors.Errorf("failed to insert workspace observation: %w", err)
	}
	return nil
}

func workspaceRelationshipForEvent(ctx context.Context, tx *sql.Tx, event *model.Event) (model.WorkspaceRelationship, error) {
	canonical, err := canonicalWorkspaceForEvent(ctx, tx, event)
	if err != nil {
		return model.WorkspaceRelationshipUnknown, err
	}
	relationship := model.ClassifyWorkspaceRelationship(canonical, event.Workspace())
	if relationship != model.WorkspaceRelationshipConflict {
		return relationship, nil
	}

	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM session_workspace_aliases
		 WHERE session_id = ? AND alias_workspace = ?`,
		event.SessionID().String(), event.Workspace().String(),
	).Scan(&count); err != nil {
		return model.WorkspaceRelationshipUnknown, xerrors.Errorf("failed to inspect reviewed workspace alias: %w", err)
	}
	if count > 0 {
		return model.WorkspaceRelationshipExplicitAlias, nil
	}
	return relationship, nil
}

func tableExistsInTransaction(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&count); err != nil {
		return false, xerrors.Errorf("failed to inspect SQLite schema for %s: %w", table, err)
	}
	return count > 0, nil
}

func canonicalWorkspaceForEvent(ctx context.Context, tx *sql.Tx, event *model.Event) (types.Workspace, error) {
	var canonical string
	err := tx.QueryRowContext(ctx, `SELECT workspace FROM sessions WHERE session_id = ?`, event.SessionID().String()).Scan(&canonical)
	if err == nil {
		return types.Workspace(canonical), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", xerrors.Errorf("failed to read canonical session workspace: %w", err)
	}
	if event.Kind() == types.EventKindSessionStarted {
		return event.Workspace(), nil
	}
	return "", nil
}

func diagnosticReason(identityStatus string) string {
	if identityStatus == "conflict" {
		return "delivery_identity_conflict"
	}
	return ""
}

func rootSourceClient(agent string) string {
	root, _, _ := strings.Cut(strings.TrimSpace(agent), "/")
	return strings.TrimSpace(root)
}
