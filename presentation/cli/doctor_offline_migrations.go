package cli

import (
	"context"
	"os"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"golang.org/x/xerrors"
)

func skippedOfflineMigrationsCheck() doctorCheck {
	return doctorCheck{
		Name:   "offline-migrations",
		Status: doctorStatusSkip,
		Message: Localize(
			"default doctor is filesystem-metadata-only for stores at or above 2 GiB; run doctor --fix to apply authorized data-dependent migrations (can take minutes)",
			"2 GiB 以上の store では default doctor は filesystem metadata のみです。データ依存 migration の適用は doctor --fix です（数分かかることがあります）",
		),
		FixCommand: "traceary doctor --fix",
	}
}

func (c *RootCLI) inspectOfflineMigrations(ctx context.Context) doctorCheck {
	const name = "offline-migrations"
	if c.storeManagement == nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("store management is not configured", "store management が設定されていません"),
		}
	}
	pending, err := c.storeManagement.PreviewOfflineMigrations(ctx)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect pending data-dependent migrations: %v", "保留中のデータ依存マイグレーションの確認に失敗しました: %v", err),
		}
	}
	if len(pending) == 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("no pending data-dependent migrations", "保留中のデータ依存マイグレーションはありません"),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"pending data-dependent migrations: %s (not applied implicitly; can take minutes)",
			"保留中のデータ依存マイグレーション: %s（暗黙には適用しません。数分かかることがあります）",
			formatMigrationVersions(pending),
		),
		Hint: Localize(
			"apply with doctor --fix after reviewing a backup",
			"backup を確認したあと doctor --fix で適用してください",
		),
		FixCommand: "traceary doctor --fix",
	}
}

func (c *RootCLI) applyAuthorizedStoreInit(ctx context.Context, input doctorCommandInput) (doctorFixLog, bool) {
	log := doctorFixLog{Name: "offline-migrations", Before: "unknown"}
	if c.storeManagement == nil {
		log.Error = Localize("store management is not configured", "store management が設定されていません")
		return log, true
	}
	resolved, err := resolveDBPath(input.dbPath)
	if err != nil {
		log.Error = xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err).Error()
		return log, true
	}
	c.applyDatabasePath(resolved)
	pending, err := c.storeManagement.PreviewOfflineMigrations(ctx)
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	if len(pending) == 0 {
		return log, false
	}
	versions := formatMigrationVersions(pending)
	approval, err := boundDropApprovalForFix(ctx, c.storeManagement, input.approveDrop)
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	unavailableApproval, err := unavailableRetentionApprovalForFix(ctx, c.storeManagement, input.approveUnavailableRetention)
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	if input.dryRun {
		log.Action = "dry-run: would apply data-dependent migrations " + versions
		return log, true
	}
	if c.preparedStoreUpgradeFactory == nil {
		log.Error = Localize("offline upgrade driver is not configured", "offline upgrade driver が設定されていません")
		return log, true
	}
	info, statErr := os.Stat(resolved)
	size := uint64(1 << 20)
	if statErr == nil && info.Size() > 0 {
		size = uint64(info.Size())
	}
	svc := c.preparedStoreUpgradeFactory(resolved)
	receipt, err := svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:                    domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:                   resolved,
		ConsumerBinding:              "traceary-doctor-offline-upgrade",
		BoundDropApproval:            approval,
		UnavailableRetentionApproval: unavailableApproval,
		Budget: domain.PreparedStoreUpgradeBudget{
			WallTimeLimit:      time.Hour,
			PublishLockLimit:   time.Hour,
			OwnedDiskByteLimit: size*8 + 1<<30,
			WALByteLimit:       size*2 + 1<<30,
			TemporaryByteLimit: size*4 + 1<<30,
			SafetyMarginBytes:  64 << 20,
		},
	})
	if err != nil {
		log.Error = err.Error()
		return log, true
	}
	log.Action = "applied data-dependent migrations: " + versions +
		"\nrollback copy retained as forensic backup (not an interchangeable rollback target): " + receipt.RollbackPath
	return log, true
}

func boundDropApprovalForFix(ctx context.Context, store usecase.StoreManagementUsecase, token string) (*domain.BoundDropApproval, error) {
	inspection, err := store.InspectBoundDrop(ctx)
	if err != nil {
		return nil, xerrors.Errorf("inspect bound drop: %w", err)
	}
	if inspection.RowCount == 0 {
		return nil, nil
	}
	if token == "" {
		return nil, &apptypes.BoundDropApprovalRequiredError{RowCount: inspection.RowCount, Digest: inspection.Digest}
	}
	count, digest, err := apptypes.ParseBoundDropApprovalToken(token)
	if err != nil {
		return nil, xerrors.Errorf("parse bound drop approval: %w", err)
	}
	approval, err := domain.NewBoundDropApproval(count, digest)
	if err != nil {
		return nil, xerrors.Errorf("bound drop approval: %w", err)
	}
	if !approval.Matches(inspection.RowCount, inspection.Digest) {
		return nil, &apptypes.BoundDropApprovalRequiredError{RowCount: inspection.RowCount, Digest: inspection.Digest}
	}
	return &approval, nil
}

func unavailableRetentionApprovalForFix(ctx context.Context, store usecase.StoreManagementUsecase, token string) (*domain.UnavailableRetentionApproval, error) {
	inspection, err := store.InspectUnavailableRetention(ctx)
	if err != nil {
		return nil, xerrors.Errorf("inspect unavailable retention: %w", err)
	}
	if inspection.RowCount == 0 {
		return nil, nil
	}
	required := &apptypes.UnavailableRetentionApprovalRequiredError{
		RowCount:      inspection.RowCount,
		Digest:        inspection.Digest,
		Sample:        inspection.Sample,
		SchemaVersion: inspection.SchemaVersion,
	}
	if token == "" {
		return nil, required
	}
	count, digest, err := apptypes.ParseUnavailableRetentionApprovalToken(token)
	if err != nil {
		return nil, xerrors.Errorf("parse unavailable retention approval: %w", err)
	}
	if count != inspection.RowCount || digest != inspection.Digest {
		return nil, required
	}
	approval, err := domain.NewUnavailableRetentionApproval(inspection.RowCount, inspection.Digest, inspection.SchemaVersion)
	if err != nil {
		return nil, xerrors.Errorf("unavailable retention approval: %w", err)
	}
	return &approval, nil
}
