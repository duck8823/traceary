package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

// PayloadRehearsalPreparation coordinates the durable handoff without exposing
// generic protocol decisions to the payload workflow.
type PayloadRehearsalPreparation struct {
	Migrations fs.FS
	Journal    func(string) application.PreparedStoreUpgradeJournal
	Service    func(string) application.PreparedStoreUpgradeUsecase
}

// Preview classifies whether the copied target requires offline migration.
func (p PayloadRehearsalPreparation) Preview(ctx context.Context, c apptypes.PayloadRehearsalConfig) (application.RehearsalPreparationPlan, error) {
	db, err := openDirectReadOnly(ctx, c.TargetPath)
	if err != nil {
		return application.RehearsalPreparationPlan{}, err
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, p.Migrations)
	if err != nil {
		return application.RehearsalPreparationPlan{}, err
	}
	return application.RehearsalPreparationPlan{Required: plan.Offline && len(plan.Pending) > 0, MigrationSetDigest: plan.Digest}, nil
}

//nolint:wrapcheck // the binding helper preserves the underlying serialization failure.
func rehearsalPreparationBinding(c apptypes.PayloadRehearsalConfig) (string, error) {
	digest, err := fileDigest(c.BackupPath)
	if err != nil {
		return "", err
	}
	value := struct {
		Target, Live, Backup, Digest string
		Batch                        int
		Stored, Decoded, WAL         int64
	}{filepath.Clean(c.TargetPath), filepath.Clean(c.LivePath), filepath.Clean(c.BackupPath), digest, c.BatchRows, c.StoredByteLimit, c.DecodedByteLimit, c.MaxWALBytes}
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// EnsurePrepared resumes or publishes the exact prepared migration candidate.
//
//nolint:wrapcheck // durable protocol errors retain their typed causes.
func (p PayloadRehearsalPreparation) EnsurePrepared(ctx context.Context, c apptypes.PayloadRehearsalConfig) (application.RehearsalPreparedTarget, error) {
	if p.Journal == nil || p.Service == nil {
		return application.RehearsalPreparedTarget{}, errors.New("payload rehearsal preparation is not configured")
	}
	journal := p.Journal(c.TargetPath)
	if journal == nil {
		return application.RehearsalPreparedTarget{}, errors.New("payload rehearsal preparation journal is not configured")
	}
	binding, err := rehearsalPreparationBinding(c)
	if err != nil {
		return application.RehearsalPreparedTarget{}, err
	}
	service := p.Service(c.TargetPath)
	run, err := journal.FindActive(ctx, domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration, c.TargetPath, binding)
	if err == nil {
		receipt, resumeErr := service.Resume(ctx, run.ID)
		return application.RehearsalPreparedTarget{Receipt: receipt}, resumeErr
	}
	if !errors.Is(err, os.ErrNotExist) {
		return application.RehearsalPreparedTarget{}, err
	}
	info, err := os.Stat(c.TargetPath)
	if err != nil {
		return application.RehearsalPreparedTarget{}, err
	}
	budget := domain.PreparedStoreUpgradeBudget{WallTimeLimit: c.WallTimeLimit, PublishLockLimit: c.LockTimeLimit, OwnedDiskByteLimit: uint64(info.Size())*2 + uint64(c.MaxWALBytes), WALByteLimit: uint64(c.MaxWALBytes), TemporaryByteLimit: uint64(c.MaxWALBytes), SafetyMarginBytes: uint64(info.Size()) / 10}
	run, err = service.Plan(ctx, application.PreparedStoreUpgradeCommand{Operation: domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration, TargetPath: c.TargetPath, ConsumerBinding: binding, Budget: budget})
	if err != nil {
		return application.RehearsalPreparedTarget{}, err
	}
	if _, err = service.Prepare(ctx, run.ID); err != nil {
		return application.RehearsalPreparedTarget{}, err
	}
	receipt, err := service.Publish(ctx, run.ID)
	return application.RehearsalPreparedTarget{Receipt: receipt}, err
}

// RollbackPrepared restores the pre-migration copied target through the journal.
//
//nolint:wrapcheck // durable protocol errors retain their typed causes.
func (p PayloadRehearsalPreparation) RollbackPrepared(ctx context.Context, c apptypes.PayloadRehearsalConfig) (application.RehearsalRollbackResult, error) {
	if p.Journal == nil || p.Service == nil {
		return application.RehearsalRollbackResult{}, errors.New("payload rehearsal preparation is not configured")
	}
	binding, err := rehearsalPreparationBinding(c)
	if err != nil {
		return application.RehearsalRollbackResult{}, err
	}
	journal := p.Journal(c.TargetPath)
	if journal == nil {
		return application.RehearsalRollbackResult{}, errors.New("payload rehearsal preparation journal is not configured")
	}
	run, err := journal.FindActive(ctx, domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration, c.TargetPath, binding)
	if err != nil {
		return application.RehearsalRollbackResult{}, err
	}
	rolled, err := p.Service(c.TargetPath).Rollback(ctx, run.ID)
	return application.RehearsalRollbackResult{RunID: run.ID, RolledBack: rolled.Phase == domain.PreparedStoreUpgradeRolledBack}, err
}

var _ application.RehearsalTargetPreparation = PayloadRehearsalPreparation{}
