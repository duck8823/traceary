package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

const retiredDropTableName = "run_lineages"

func inspectBoundDrop(ctx context.Context, db *sql.DB) (int64, string, error) {
	exists, err := tableExists(ctx, db, retiredDropTableName)
	if err != nil {
		return 0, "", err
	}
	if !exists {
		return 0, hex.EncodeToString(sha256.New().Sum(nil)), nil
	}
	rows, err := db.QueryContext(ctx, `SELECT host, run_id FROM run_lineages ORDER BY host, run_id`)
	if err != nil {
		return 0, "", xerrors.Errorf("inspect run_lineages identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hasher := sha256.New()
	var count int64
	for rows.Next() {
		var host, runID string
		if err := rows.Scan(&host, &runID); err != nil {
			return 0, "", xerrors.Errorf("scan run_lineages identity: %w", err)
		}
		_, _ = hasher.Write([]byte(host))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(runID))
		_, _ = hasher.Write([]byte{'\n'})
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", xerrors.Errorf("iterate run_lineages identities: %w", err)
	}
	return count, hex.EncodeToString(hasher.Sum(nil)), nil
}

func boundDropApprovalRequired(count int64, digest string) error {
	return &apptypes.BoundDropApprovalRequiredError{RowCount: count, Digest: digest}
}

func (d *Database) inspectBoundDropAt(ctx context.Context, snapshot string) (apptypes.BoundDropInspection, error) {
	if snapshot == "" {
		return apptypes.BoundDropInspection{Digest: hex.EncodeToString(sha256.New().Sum(nil))}, nil
	}
	db, err := openDirectReadOnly(ctx, snapshot)
	if err != nil {
		return apptypes.BoundDropInspection{}, xerrors.Errorf("open store for bound drop inspect: %w", err)
	}
	defer func() { _ = db.Close() }()
	count, digest, err := inspectBoundDrop(ctx, db)
	if err != nil {
		return apptypes.BoundDropInspection{}, err
	}
	return apptypes.BoundDropInspection{RowCount: count, Digest: digest}, nil
}

// VerifyBoundDrop re-reads the source identity set immediately before Build.
func (r *PreparedUpgradeMigrationRecipe) VerifyBoundDrop(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source for bound drop preflight: %w", err)
	}
	defer func() { _ = db.Close() }()
	count, digest, err := inspectBoundDrop(ctx, db)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if request.Run.BoundDropApproval == nil {
		return boundDropApprovalRequired(count, digest)
	}
	if !request.Run.BoundDropApproval.Matches(count, digest) {
		return boundDropApprovalRequired(count, digest)
	}
	return nil
}

func verifyDropRetiredTable(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	exists, err := tableExists(ctx, candidateDB, retiredDropTableName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("candidate still has table %s", retiredDropTableName)
	}
	sourceHasRuns, err := tableExists(ctx, sourceDB, "usage_observation_runs")
	if err != nil {
		return err
	}
	candidateHasRuns, err := tableExists(ctx, candidateDB, "usage_observation_runs")
	if err != nil {
		return err
	}
	if sourceHasRuns && candidateHasRuns {
		if err := verifyTableCountAndDigest(ctx, sourceDB, candidateDB, "usage_observation_runs"); err != nil {
			return err
		}
	}
	fkRows, err := candidateDB.QueryContext(ctx, `PRAGMA foreign_key_list('usage_observation_runs')`)
	if err != nil {
		return fmt.Errorf("inspect usage_observation_runs foreign keys: %w", err)
	}
	defer func() { _ = fkRows.Close() }()
	for fkRows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return fmt.Errorf("scan usage_observation_runs foreign key: %w", err)
		}
		if table == retiredDropTableName {
			return fmt.Errorf("candidate usage_observation_runs still references %s", retiredDropTableName)
		}
	}
	if err := fkRows.Err(); err != nil {
		return fmt.Errorf("iterate usage_observation_runs foreign keys: %w", err)
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if minimumReader != 35 {
		return fmt.Errorf("candidate minimum_reader_version = %d, want 35", minimumReader)
	}
	return nil
}

var _ application.BoundDropVerifier = (*PreparedUpgradeMigrationRecipe)(nil)
