package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const selectRunLineage = `
SELECT host, run_id, parent_host, parent_run_id, session_id,
       batch_id, ticket_ref, repository, pull_request_number, head_sha,
       packet_sha256, packet_bytes, tool_output_bytes
  FROM run_lineages
 WHERE host = ? AND run_id = ?`

// RunLineageDatasource persists immutable run facts with serialized replay.
type RunLineageDatasource struct{ db *Database }

// NewRunLineageDatasource creates a run lineage datasource.
func NewRunLineageDatasource(db *Database) *RunLineageDatasource {
	return &RunLineageDatasource{db: db}
}

var _ model.RunLineageRepository = (*RunLineageDatasource)(nil)

// Record inserts a fact or reports an exact already-applied replay.
func (d *RunLineageDatasource) Record(ctx context.Context, lineage *model.RunLineage) (transition model.RunLineageTransition, err error) {
	if lineage == nil {
		return "", model.ErrInvalidRunLineage
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return "", xerrors.Errorf("failed to open DB for run lineage record: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close DB after run lineage record: %w", closeErr)
		}
	}()
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", xerrors.Errorf("failed to acquire run lineage connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close run lineage connection: %w", closeErr)
		}
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", xerrors.Errorf("failed to begin run lineage transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if _, rollbackErr := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); rollbackErr != nil {
			slog.Debug("failed to roll back run lineage transaction", "error", rollbackErr)
		}
	}()

	current, err := findRunLineage(ctx, conn, lineage.Identity())
	if err != nil {
		return "", xerrors.Errorf("failed to inspect existing run lineage: %w", err)
	}
	if existing, present := current.Value(); present {
		transition, err = existing.Reconcile(lineage)
		if err != nil {
			return "", xerrors.Errorf("failed to reconcile run lineage: %w", err)
		}
	} else {
		if err := insertRunLineage(ctx, conn, lineage); err != nil {
			return "", xerrors.Errorf("failed to insert valid run lineage: %w", model.ErrInvalidRunLineage)
		}
		transition = model.RunLineageTransitionApplied
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return "", xerrors.Errorf("failed to commit run lineage transaction: %w", err)
	}
	committed = true
	return transition, nil
}

// FindByIdentity restores one namespaced run or returns None.
func (d *RunLineageDatasource) FindByIdentity(ctx context.Context, identity types.RunIdentity) (types.Optional[*model.RunLineage], error) {
	validated, err := types.RunIdentityFrom(identity.Host(), identity.RunID())
	if err != nil || validated != identity {
		return types.None[*model.RunLineage](), xerrors.Errorf("invalid run lineage lookup identity")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.RunLineage](), xerrors.Errorf("failed to open DB for run lineage lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	return findRunLineage(ctx, db, identity)
}

type runLineageQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
type runLineageExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func findRunLineage(ctx context.Context, queryer runLineageQueryer, identity types.RunIdentity) (types.Optional[*model.RunLineage], error) {
	lineage, err := scanRunLineage(queryer.QueryRowContext(ctx, selectRunLineage, identity.Host(), identity.RunID()))
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[*model.RunLineage](), nil
	}
	if err != nil {
		return types.None[*model.RunLineage](), xerrors.Errorf("failed to restore run lineage: %w", err)
	}
	return types.Some(lineage), nil
}

func scanRunLineage(row usageRowScanner) (*model.RunLineage, error) {
	var host, runID string
	var parentHost, parentRunID, sessionID, batchID, ticketRef, repository sql.NullString
	var pullRequestNumber sql.NullInt64
	var headSHA, packetSHA sql.NullString
	var packetBytes, toolOutputBytes sql.NullInt64
	if err := row.Scan(&host, &runID, &parentHost, &parentRunID, &sessionID, &batchID, &ticketRef, &repository, &pullRequestNumber, &headSHA, &packetSHA, &packetBytes, &toolOutputBytes); err != nil {
		return nil, xerrors.Errorf("failed to scan run lineage row: %w", err)
	}
	identity, err := types.RunIdentityFrom(host, runID)
	if err != nil {
		return nil, xerrors.Errorf("invalid stored run identity: %w", err)
	}
	parent := types.None[types.RunIdentity]()
	if parentHost.Valid != parentRunID.Valid {
		return nil, xerrors.Errorf("stored parent run identity is incomplete")
	}
	if parentHost.Valid {
		value, err := types.RunIdentityFrom(parentHost.String, parentRunID.String)
		if err != nil {
			return nil, xerrors.Errorf("invalid stored parent run identity: %w", err)
		}
		parent = types.Some(value)
	}
	session := types.None[types.SessionID]()
	if sessionID.Valid {
		value, err := types.SessionIDFrom(sessionID.String)
		if err != nil {
			return nil, xerrors.Errorf("invalid stored run session identity: %w", err)
		}
		session = types.Some(value)
	}
	work, err := types.RunWorkAttributionFrom(optionalString(batchID), optionalString(ticketRef), optionalString(repository), optionalInt64(pullRequestNumber), optionalString(headSHA))
	if err != nil {
		return nil, xerrors.Errorf("invalid stored work attribution: %w", err)
	}
	packet := types.None[types.PacketIdentity]()
	if packetSHA.Valid != packetBytes.Valid {
		return nil, xerrors.Errorf("stored packet identity is incomplete")
	}
	if packetSHA.Valid {
		value, err := types.PacketIdentityFrom(packetSHA.String, packetBytes.Int64)
		if err != nil {
			return nil, xerrors.Errorf("invalid stored packet identity: %w", err)
		}
		packet = types.Some(value)
	}
	lineage, err := model.RunLineageOf(identity, parent, session, work, packet, optionalInt64(toolOutputBytes))
	if err != nil {
		return nil, xerrors.Errorf("invalid stored run lineage: %w", err)
	}
	return lineage, nil
}

func insertRunLineage(ctx context.Context, exec runLineageExecer, lineage *model.RunLineage) error {
	const query = `
INSERT INTO run_lineages (
    host, run_id, parent_host, parent_run_id, session_id,
    batch_id, ticket_ref, repository, pull_request_number, head_sha,
    packet_sha256, packet_bytes, tool_output_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	identity := lineage.Identity()
	args := []any{identity.Host(), identity.RunID()}
	if parent, present := lineage.Parent().Value(); present {
		args = append(args, parent.Host(), parent.RunID())
	} else {
		args = append(args, nil, nil)
	}
	args = append(args, nullableSessionID(lineage.SessionID()))
	work := lineage.Work()
	args = append(args, nullableOptionalString(work.BatchID()), nullableOptionalString(work.TicketRef()), nullableOptionalString(work.Repository()), nullableOptionalInt64(work.PullRequestNumber()), nullableOptionalString(work.HeadSHA()))
	if packet, present := lineage.Packet().Value(); present {
		args = append(args, packet.SHA256(), packet.Bytes())
	} else {
		args = append(args, nil, nil)
	}
	args = append(args, nullableOptionalInt64(lineage.ToolOutputBytes()))
	if _, err := exec.ExecContext(ctx, query, args...); err != nil {
		return xerrors.Errorf("failed to execute run lineage insert: %w", err)
	}
	return nil
}

func optionalString(value sql.NullString) types.Optional[string] {
	if value.Valid {
		return types.Some(value.String)
	}
	return types.None[string]()
}
func nullableOptionalString(value types.Optional[string]) any {
	if actual, present := value.Value(); present {
		return actual
	}
	return nil
}
func nullableOptionalInt64(value types.Optional[int64]) any {
	if actual, present := value.Value(); present {
		return actual
	}
	return nil
}
func nullableSessionID(value types.Optional[types.SessionID]) any {
	if actual, present := value.Value(); present {
		return actual.String()
	}
	return nil
}
