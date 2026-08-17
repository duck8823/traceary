//nolint:wrapcheck // Cobra boundary preserves typed compaction errors.
package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

func (c *RootCLI) newStoreCompactionCommand() *cobra.Command {
	var (
		path              string
		force             bool
		keepDays          int
		workDir           string
		archive           bool
		archiveVerify     string
		archiveRestore    string
		deleteAfterVerify bool
		target            string
		passphraseEnv     string
		dryRun            bool
		output            string
		retentionPlan     bool
		retentionApply    bool
		projectionRebuild bool
		projectionAbort   bool
		planPath          string
		confirmPlanID     string
		fileRetention     storeFileRetentionPlanInput
		projectionBudget  storeProjectionBudgetInput
	)
	projectionBudget = defaultStoreProjectionBudgetInput()
	fileRetention.archiveMaxCount = -1
	fileRetention.archiveMaxBytes = -1
	fileRetention.backupMaxCount = -1
	fileRetention.backupMaxBytes = -1
	fileRetention.expiresAfter = time.Hour

	cmd := &cobra.Command{
		Use:   "compact",
		Short: Localize("Rewrite the store, or archive / retain on-disk artifacts with absorb flags", "ストアを書き換える。absorb flag で archive / ディスク上 artifact 保持も行う"),
		Long: Localize(
			"Rewrite the store, dropping reclaimable bodies and retired indexes. Pass --archive to export GC-eligible rows (former store archive create). Pass --archive-verify / --archive-restore for an existing package. Pass --retention-plan / --retention-apply for on-disk archive and backup file retention (former store retention files). Pass --projection-rebuild / --projection-abort for search-projection lifecycle (former store search-projection start/abort).",
			"ストアを書き換え、回収できる本文と退役済み index を落とします。--archive で GC 適格行を export します（旧 store archive create）。既存 package は --archive-verify / --archive-restore。ディスク上の archive / backup 保持は --retention-plan / --retention-apply（旧 store retention files）。search-projection の世代操作は --projection-rebuild / --projection-abort（旧 store search-projection start/abort）。",
		),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fileRetention.dbPath = path
			fileRetention.outputPath = output
			return c.runStoreCompact(cmd, storeCompactInput{
				path:              path,
				force:             force,
				keepDays:          keepDays,
				workDir:           workDir,
				archive:           archive,
				archiveVerify:     archiveVerify,
				archiveRestore:    archiveRestore,
				deleteAfterVerify: deleteAfterVerify,
				target:            target,
				passphraseEnv:     passphraseEnv,
				dryRun:            dryRun,
				output:            output,
				retentionPlan:     retentionPlan,
				retentionApply:    retentionApply,
				projectionRebuild: projectionRebuild,
				projectionAbort:   projectionAbort,
				planPath:          planPath,
				confirmPlanID:     confirmPlanID,
				fileRetention:     fileRetention,
				projectionBudget:  projectionBudget,
				forceSet:          cmd.Flags().Changed("force"),
				workDirSet:        cmd.Flags().Changed("work-dir"),
				targetSet:         cmd.Flags().Changed("target"),
				deleteAfterSet:    cmd.Flags().Changed("delete-after-verify"),
				dryRunSet:         cmd.Flags().Changed("dry-run"),
			})
		},
	}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&force, "force", false, Localize("write mechanical summaries for unrefined discardable sessions and discard those bodies", "未 refine の破棄対象へ機械要約を書いて本文を捨てる"))
	cmd.Flags().IntVar(&keepDays, "keep-days", application.DefaultCompactKeepDays, Localize("retain bodies newer than this many days; also the --archive keep window", "この日数より新しい本文は保持する。--archive の保持窓でも使う"))
	cmd.Flags().StringVar(&workDir, "work-dir", "", Localize("stage the source-sized work copy on another volume when this volume cannot hold a replica", "このボリュームにレプリカを置けないとき、source サイズの work copy を別ボリュームに置く"))
	cmd.Flags().BoolVar(&archive, "archive", false, Localize("export GC-eligible rows to a versioned archive package (former store archive create)", "GC 適格行を版付き archive package に export する（旧 store archive create）"))
	cmd.Flags().StringVar(&archiveVerify, "archive-verify", "", Localize("verify an archive package (former store archive verify)", "archive package の完全性を検証する（旧 store archive verify）"))
	cmd.Flags().StringVar(&archiveRestore, "archive-restore", "", Localize("restore rows from an archive package (former store archive restore)", "archive package から行を restore する（旧 store archive restore）"))
	cmd.Flags().BoolVar(&deleteAfterVerify, "delete-after-verify", false, Localize("with --archive, after a successful verify, delete exact archived identities from the live store", "--archive 時、verify 成功後に archive 済み identity を live store から削除する"))
	cmd.Flags().StringVar(&target, "target", "all", Localize("with --archive, records to archive (events | sessions | memories | memory_edges | all)", "--archive 時の対象 (events | sessions | memories | memory_edges | all)"))
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "", Localize("with --archive/--archive-verify/--archive-restore, env var name holding an optional archive passphrase", "--archive / --archive-verify / --archive-restore 時、任意の passphrase を持つ env 名"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, Localize("with --archive or --archive-restore, plan or count only; do not write", "--archive または --archive-restore 時、plan / 件数のみ。書き込まない"))
	cmd.Flags().StringVar(&output, "output", "", Localize("with --archive or --retention-plan, output path", "--archive または --retention-plan の出力 path"))
	cmd.Flags().BoolVar(&retentionPlan, "retention-plan", false, Localize("write an immutable archive/backup capacity plan (former store retention files plan)", "変更不能な archive/backup 容量計画を書き出す（旧 store retention files plan）"))
	cmd.Flags().BoolVar(&retentionApply, "retention-apply", false, Localize("apply an exact reviewed archive/backup plan (former store retention files apply)", "review 済みの archive/backup 計画を厳密に適用する（旧 store retention files apply）"))
	cmd.Flags().StringVar(&planPath, "plan", "", Localize("with --retention-apply, reviewed plan path", "--retention-apply 時の review 済み plan path"))
	cmd.Flags().StringVar(&confirmPlanID, "confirm-plan-id", "", Localize("with --retention-apply, exact reviewed plan ID", "--retention-apply 時の review 済み plan ID の完全値"))
	cmd.Flags().StringVar(&fileRetention.archiveRoot, "archive-root", "", Localize("with --retention-plan, archive directory to inspect", "--retention-plan 時に確認する archive directory"))
	cmd.Flags().StringVar(&fileRetention.backupRoot, "backup-root", "", Localize("with --retention-plan, backup directory to inspect", "--retention-plan 時に確認する backup directory"))
	cmd.Flags().DurationVar(&fileRetention.archiveMaxAge, "archive-max-age", 0, Localize("with --retention-plan, maximum archive age (for example 720h)", "--retention-plan 時の archive 最大保持期間（例: 720h）"))
	cmd.Flags().IntVar(&fileRetention.archiveMaxCount, "archive-max-count", -1, Localize("with --retention-plan, maximum archive count", "--retention-plan 時の archive 最大件数"))
	cmd.Flags().Int64Var(&fileRetention.archiveMaxBytes, "archive-max-allocated-bytes", -1, Localize("with --retention-plan, maximum allocated archive bytes", "--retention-plan 時の archive 最大割り当て byte 数"))
	cmd.Flags().DurationVar(&fileRetention.backupMaxAge, "backup-max-age", 0, Localize("with --retention-plan, maximum backup age (for example 720h)", "--retention-plan 時の backup 最大保持期間（例: 720h）"))
	cmd.Flags().IntVar(&fileRetention.backupMaxCount, "backup-max-count", -1, Localize("with --retention-plan, maximum backup count", "--retention-plan 時の backup 最大件数"))
	cmd.Flags().Int64Var(&fileRetention.backupMaxBytes, "backup-max-allocated-bytes", -1, Localize("with --retention-plan, maximum allocated backup bytes", "--retention-plan 時の backup 最大割り当て byte 数"))
	cmd.Flags().DurationVar(&fileRetention.expiresAfter, "expires-after", time.Hour, Localize("with --retention-plan, plan validity duration", "--retention-plan 時の plan 有効期間"))
	cmd.Flags().BoolVar(&projectionRebuild, "projection-rebuild", false, Localize("start a new search-projection generation (former store search-projection start); resume if the live hash matches, otherwise replace it", "新しい search-projection 世代を開始する（旧 store search-projection start）。live hash が一致すれば resume、異なれば置き換える"))
	cmd.Flags().BoolVar(&projectionAbort, "projection-abort", false, Localize("abandon an incomplete search-projection generation (former store search-projection abort)", "未完了の search-projection 世代を破棄する（旧 store search-projection abort）"))
	cmd.Flags().IntVar(&projectionBudget.rows, "rows", projectionBudget.rows, Localize("with --projection-rebuild, maximum source rows per batch", "--projection-rebuild 時のバッチあたり最大 source 行数"))
	cmd.Flags().DurationVar(&projectionBudget.wall, "wall-time", projectionBudget.wall, Localize("with --projection-rebuild, maximum total batch duration", "--projection-rebuild 時のバッチ合計時間上限"))
	cmd.Flags().DurationVar(&projectionBudget.lock, "lock-time", projectionBudget.lock, Localize("with --projection-rebuild, maximum write-lock duration", "--projection-rebuild 時の write-lock 時間上限"))
	cmd.Flags().Int64Var(&projectionBudget.stored, "stored-bytes", projectionBudget.stored, Localize("with --projection-rebuild, maximum stored source bytes", "--projection-rebuild 時の格納 source バイト上限"))
	cmd.Flags().Int64Var(&projectionBudget.decoded, "decoded-bytes", projectionBudget.decoded, Localize("with --projection-rebuild, maximum decoded source bytes", "--projection-rebuild 時の復号 source バイト上限"))
	cmd.Flags().Int64Var(&projectionBudget.written, "write-bytes", projectionBudget.written, Localize("with --projection-rebuild, maximum logical write bytes", "--projection-rebuild 時の論理 write バイト上限"))
	cmd.Flags().DurationVar(&projectionBudget.recentAge, "recent-age", projectionBudget.recentAge, Localize("with --projection-rebuild, recent projection age", "--projection-rebuild 時の recent projection 保持期間"))
	cmd.Flags().Int64Var(&projectionBudget.indexFamilyBytes, "index-family-bytes", projectionBudget.indexFamilyBytes, "steady-state physical byte target for one completed search-index family after cleanup; not a rebuild-peak cap (two generations plus FTS delete postings can exceed it)")
	cmd.MarkFlagsMutuallyExclusive("archive", "archive-verify", "archive-restore", "retention-plan", "retention-apply", "projection-rebuild", "projection-abort")
	cmd.AddCommand(c.newStoreCompactionRollbackCommand())
	return cmd
}

type storeCompactInput struct {
	path              string
	force             bool
	keepDays          int
	workDir           string
	archive           bool
	archiveVerify     string
	archiveRestore    string
	deleteAfterVerify bool
	target            string
	passphraseEnv     string
	dryRun            bool
	output            string
	retentionPlan     bool
	retentionApply    bool
	projectionRebuild bool
	projectionAbort   bool
	planPath          string
	confirmPlanID     string
	fileRetention     storeFileRetentionPlanInput
	projectionBudget  storeProjectionBudgetInput
	forceSet          bool
	workDirSet        bool
	targetSet         bool
	deleteAfterSet    bool
	dryRunSet         bool
}

func (c *RootCLI) runStoreCompact(cmd *cobra.Command, input storeCompactInput) error {
	absorb := input.archive || input.archiveVerify != "" || input.archiveRestore != "" || input.retentionPlan || input.retentionApply || input.projectionRebuild || input.projectionAbort
	if absorb && (input.forceSet || input.workDirSet) {
		return xerrors.New(Localize(
			"--force/--work-dir cannot be combined with --archive/--archive-verify/--archive-restore/--retention-plan/--retention-apply/--projection-rebuild/--projection-abort",
			"--force/--work-dir は --archive / --archive-verify / --archive-restore / --retention-plan / --retention-apply / --projection-rebuild / --projection-abort と同時に使えません",
		))
	}
	if !input.archive && (input.deleteAfterSet || input.targetSet) {
		return xerrors.New(Localize(
			"--delete-after-verify/--target require --archive",
			"--delete-after-verify/--target には --archive が必要です",
		))
	}
	if input.dryRunSet && !input.archive && input.archiveRestore == "" {
		return xerrors.New(Localize(
			"--dry-run requires --archive or --archive-restore",
			"--dry-run には --archive または --archive-restore が必要です",
		))
	}
	if err := validateStoreCompactAbsorbFlags(cmd, input); err != nil {
		return err
	}
	if input.archive {
		return c.runStoreArchiveCreate(cmd.Context(), cmd.OutOrStdout(), storeArchiveCreateInput{
			dbPath:            input.path,
			output:            input.output,
			keepDays:          input.keepDays,
			target:            input.target,
			dryRun:            input.dryRun,
			deleteAfterVerify: input.deleteAfterVerify,
			passphraseEnv:     input.passphraseEnv,
		}, cmd.Root().Version)
	}
	if input.archiveVerify != "" {
		return c.runStoreArchiveVerify(cmd.Context(), cmd.OutOrStdout(), storeArchiveVerifyInput{
			dbPath:        input.path,
			input:         input.archiveVerify,
			passphraseEnv: input.passphraseEnv,
		})
	}
	if input.archiveRestore != "" {
		return c.runStoreArchiveRestore(cmd.Context(), cmd.OutOrStdout(), storeArchiveRestoreInput{
			dbPath:        input.path,
			input:         input.archiveRestore,
			dryRun:        input.dryRun,
			passphraseEnv: input.passphraseEnv,
		})
	}
	if input.retentionPlan {
		return c.runStoreFileRetentionPlan(cmd.Context(), cmd.OutOrStdout(), input.fileRetention)
	}
	if input.retentionApply {
		if strings.TrimSpace(input.planPath) == "" || strings.TrimSpace(input.confirmPlanID) == "" {
			return xerrors.New(Localize(
				"--retention-apply requires --plan and --confirm-plan-id",
				"--retention-apply には --plan と --confirm-plan-id が必要です",
			))
		}
		return c.runStoreFileRetentionApply(cmd.Context(), cmd.OutOrStdout(), storeFileRetentionApplyInput{
			planPath:        input.planPath,
			confirmedPlanID: input.confirmPlanID,
		})
	}
	if input.projectionRebuild || input.projectionAbort {
		if cmd.Flags().Changed("db-path") {
			return xerrors.New(Localize(
				"--db-path cannot be combined with --projection-rebuild/--projection-abort; those flags use the process default store",
				"--db-path は --projection-rebuild / --projection-abort と同時に使えません。これらの flag はプロセス既定ストアを対象にします",
			))
		}
	}
	if input.projectionRebuild {
		return c.runStoreSearchProjectionRebuild(cmd.Context(), cmd.OutOrStdout(), input.projectionBudget.budget())
	}
	if input.projectionAbort {
		return c.runStoreSearchProjectionAbort(cmd.Context(), cmd.OutOrStdout())
	}

	resolved, service, err := c.compactionFor(input.path)
	if err != nil {
		return err
	}
	result, err := service.Compact(cmd.Context(), application.CompactInput{
		Source:   resolved,
		Force:    input.force,
		KeepDays: input.keepDays,
		WorkDir:  input.workDir,
	})
	if err != nil {
		var unrefined application.UnrefinedMaterialError
		if errors.As(err, &unrefined) {
			return xerrors.Errorf("%s", unrefined.Error())
		}
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"run_id":                      result.Run.ID,
		"phase":                       result.Run.Phase,
		"bytes_before":                result.BytesBefore,
		"bytes_after":                 result.BytesAfter,
		"unrefined_remaining":         result.UnrefinedRemaining,
		"unrefined_bytes":             result.UnrefinedBytes,
		"mechanical_summaries":        result.MechanicalSummaries,
		"released_command_body_rows":  result.ReleasedCommandBodyRows,
		"released_command_body_bytes": result.ReleasedCommandBodyBytes,
		"estimated_reclaimable_bytes": result.EstimatedReclaimableBytes,
		"compact_strategy":            result.CompactStrategy,
		"rollback_path":               result.Run.RollbackPath,
		// Apply-time VerifyPair is not in-use proof. The operator
		// deletes this file when they accept the rewrite (#1827).
		"rollback_retained": result.CompactStrategy != application.CompactStrategyInPlace,
	})
}

func validateStoreCompactAbsorbFlags(cmd *cobra.Command, input storeCompactInput) error {
	changed := func(names ...string) bool {
		for _, name := range names {
			if cmd.Flags().Changed(name) {
				return true
			}
		}
		return false
	}
	if changed("output") && !input.archive && !input.retentionPlan {
		return xerrors.New(Localize(
			"--output requires --archive or --retention-plan",
			"--output には --archive または --retention-plan が必要です",
		))
	}
	if changed("passphrase-env") && !input.archive && input.archiveVerify == "" && input.archiveRestore == "" {
		return xerrors.New(Localize(
			"--passphrase-env requires --archive/--archive-verify/--archive-restore",
			"--passphrase-env には --archive / --archive-verify / --archive-restore が必要です",
		))
	}
	if changed("plan", "confirm-plan-id") && !input.retentionApply {
		return xerrors.New(Localize(
			"--plan/--confirm-plan-id require --retention-apply",
			"--plan/--confirm-plan-id には --retention-apply が必要です",
		))
	}
	if changed(
		"archive-root", "backup-root",
		"archive-max-age", "archive-max-count", "archive-max-allocated-bytes",
		"backup-max-age", "backup-max-count", "backup-max-allocated-bytes",
		"expires-after",
	) && !input.retentionPlan {
		return xerrors.New(Localize(
			"file-retention ceiling flags require --retention-plan",
			"file-retention の上限 flag には --retention-plan が必要です",
		))
	}
	if changed(
		"rows", "wall-time", "lock-time",
		"stored-bytes", "decoded-bytes", "write-bytes",
		"recent-age", "index-family-bytes",
	) && !input.projectionRebuild {
		return xerrors.New(Localize(
			"search-projection budget flags require --projection-rebuild",
			"search-projection の budget flag には --projection-rebuild が必要です",
		))
	}
	return nil
}

func (c *RootCLI) newStoreCompactionRollbackCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "rollback RUN_ID",
		Short: Localize("Restore the pre-compact store from the rollback inode", "rollback inode から compact 前のストアを戻す"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := c.compactionFor(path)
			if err != nil {
				return err
			}
			value, err := service.Rollback(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
		},
	}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) compactionFor(path string) (string, application.StoreCompactionUsecase, error) {
	if c.storeCompactionFactory == nil {
		return "", nil, xerrors.New("store compaction is not configured")
	}
	resolved, err := resolveDBPath(path)
	if err != nil {
		return "", nil, err
	}
	return resolved, c.storeCompactionFactory(resolved), nil
}
