package cli

import (
	"context"

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
	if input.dryRun {
		log.Action = "dry-run: would apply data-dependent migrations " + versions
		return log, true
	}
	if err := c.storeManagement.InitializeAuthorized(ctx); err != nil {
		log.Error = err.Error()
		return log, true
	}
	log.Action = "applied data-dependent migrations: " + versions
	return log, true
}
