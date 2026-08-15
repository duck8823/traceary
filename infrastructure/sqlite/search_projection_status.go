package sqlite

import (
	"context"
	"database/sql"
	_ "embed"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

//go:embed sql/select_search_projection_control_status.sql
var selectSearchProjectionControlStatusSQL string

func enrichCapacityEvidenceMethod(evidence ...*apptypes.CapacityEvidence) {
	// The method is derived because persisted capacity figures only come from dbstat.
	for _, item := range evidence {
		if item.Status != "" {
			item.Method = "dbstat"
		}
	}
}

// SearchProjectionControlStatus reads only the singleton state-machine rows.
// Keeping this query separate prevents lifecycle control paths from paying for
// operator-facing measurement queries.
func (d *Database) SearchProjectionControlStatus(ctx context.Context) (s apptypes.SearchProjectionControlStatus, err error) {
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return s, err
	}
	defer func() { _ = db.Close() }()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return s, xerrors.Errorf("begin projection control status read: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); err == nil && rollbackErr != nil && rollbackErr != sql.ErrTxDone {
			err = xerrors.Errorf("rollback projection control status read: %w", rollbackErr)
		}
	}()

	err = tx.QueryRowContext(ctx, selectSearchProjectionControlStatusSQL).Scan(
		&s.GenerationID, &s.State, &s.Phase, &s.Checkpoint, &s.ConfigHash, &s.CapacitySemanticsVersion, &s.FailureClass,
		&s.CutoverIndexFamily, &s.CutoverFamilyBytesBefore, &s.CutoverFamilyBytesAfter,
		&s.CutoverBeforeEvidence.Status, &s.CutoverBeforeEvidence.Reason,
		&s.CutoverAfterEvidence.Status, &s.CutoverAfterEvidence.Reason,
		&s.Origin, &s.IndexFamilyWithinBudget, &s.CapacityRederived,
	)
	if err != nil {
		return s, xerrors.Errorf("scan projection control status: %w", err)
	}
	enrichCapacityEvidenceMethod(&s.CutoverBeforeEvidence, &s.CutoverAfterEvidence)
	if err = tx.Commit(); err != nil {
		return s, xerrors.Errorf("commit projection control status read: %w", err)
	}
	return s, nil
}
