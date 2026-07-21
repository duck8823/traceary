package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const maxDeliveryDecisionAttempts = 3

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
	afterInsert func(context.Context, *sql.Tx) error,
) error {
	for attempt := 0; attempt < maxDeliveryDecisionAttempts; attempt++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return xerrors.Errorf("failed to begin event delivery transaction: %w", err)
		}

		inserted, persistErr := persistEventDelivery(ctx, tx, event, audit)
		if persistErr == nil && inserted && afterInsert != nil {
			persistErr = afterInsert(ctx, tx)
		}
		if persistErr != nil {
			rollbackErr := tx.Rollback()
			if errors.Is(persistErr, errHookDeliveryIdentityRace) {
				if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
					return xerrors.Errorf("failed to rollback delivery identity race: %w", rollbackErr)
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
	}

	if err := insertEventAndAudit(ctx, tx, event, audit); err != nil {
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

func insertEventAndAudit(ctx context.Context, tx *sql.Tx, event *model.Event, audit *model.CommandAudit) error {
	if _, err := tx.ExecContext(
		ctx,
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}
	if audit == nil {
		return nil
	}
	var exitCodeSQL *int
	if exitCode, ok := audit.ExitCode().Value(); ok {
		exitCodeSQL = &exitCode
	}
	if _, err := tx.ExecContext(
		ctx,
		insertCommandAuditQuery,
		audit.EventID().String(),
		audit.Command(),
		audit.Input(),
		audit.Output(),
		audit.InputTruncated(),
		audit.OutputTruncated(),
		audit.InputOriginalBytes(),
		audit.OutputOriginalBytes(),
		exitCodeSQL,
		audit.Failed(),
	); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}
	return nil
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
	if rawWorkspace == event.Workspace().String() {
		rawWorkspace = ""
	}
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
