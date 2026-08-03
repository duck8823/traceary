package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/duck8823/traceary/domain"
)

// PreparedMigrationVerifier independently verifies a pre-migration source and
// a current candidate without opening either file writable.
type PreparedMigrationVerifier struct{ Migrations fs.FS }

// VerifyPair proves compatibility, integrity, expected schema/ledger, and
// canonical event/audit equivalence. Schema equality is intentionally not
// required because the candidate is expected to be newer.
func (v PreparedMigrationVerifier) VerifyPair(ctx context.Context, source, candidate, planDigest string) (domain.PreparedCandidateEvidence, error) {
	sourceDB, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, errors.New("open source for prepared verification")
	}
	defer func() { _ = sourceDB.Close() }()
	candidateDB, err := openDirectReadOnly(ctx, candidate)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, errors.New("open candidate for prepared verification")
	}
	defer func() { _ = candidateDB.Close() }()
	if err = verifyPreparedMigrationStore(ctx, sourceDB); err != nil {
		return domain.PreparedCandidateEvidence{}, fmt.Errorf("source verification: %w", err)
	}
	if err = verifyPreparedMigrationStore(ctx, candidateDB); err != nil {
		return domain.PreparedCandidateEvidence{}, fmt.Errorf("candidate verification: %w", err)
	}
	sourcePlan, err := BuildPreparedMigrationPlan(ctx, sourceDB, v.Migrations)
	if err != nil || sourcePlan.Digest != planDigest || !sourcePlan.Offline || len(sourcePlan.Pending) == 0 {
		return domain.PreparedCandidateEvidence{}, errors.New("source migration plan does not match prepared evidence")
	}
	candidatePlan, err := BuildPreparedMigrationPlan(ctx, candidateDB, v.Migrations)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	if candidatePlan.Current != candidatePlan.Latest || len(candidatePlan.Pending) != 0 {
		return domain.PreparedCandidateEvidence{}, errors.New("candidate migration ledger is not current")
	}
	sourceCanonical, err := CanonicalEventAuditDigest(ctx, sourceDB)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	candidateCanonical, err := CanonicalEventAuditDigest(ctx, candidateDB)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	if sourceCanonical != candidateCanonical {
		return domain.PreparedCandidateEvidence{}, errors.New("candidate canonical event/audit evidence mismatch")
	}
	schema, err := schemaDigest(ctx, candidateDB)
	if err != nil {
		return domain.PreparedCandidateEvidence{}, err
	}
	return domain.PreparedCandidateEvidence{MigrationSetDigest: sourcePlan.Digest, SchemaDigest: schema, Canonical: candidateCanonical}, nil
}

func verifyPreparedMigrationStore(ctx context.Context, db *sql.DB) error {
	if err := VerifyStoreCompatibility(ctx, db); err != nil {
		return err
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("SQLite integrity check failed")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("run SQLite foreign key check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return errors.New("SQLite foreign key check failed")
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite foreign key check: %w", err)
	}
	return scrubPayloadCodecs(ctx, db)
}
