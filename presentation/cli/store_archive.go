package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

type storeArchiveCreateInput struct {
	dbPath            string
	output            string
	keepDays          int
	target            string
	dryRun            bool
	deleteAfterVerify bool
	passphraseEnv     string
}

type storeArchiveVerifyInput struct {
	dbPath        string
	input         string
	passphraseEnv string
}

type storeArchiveRestoreInput struct {
	dbPath        string
	input         string
	dryRun        bool
	passphraseEnv string
}

func (c *RootCLI) newStoreArchiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: Localize("Archive cold store rows before GC (verify-before-delete)", "GC 前に cold 行を archive する (verify-before-delete)"),
	}
	cmd.AddCommand(c.newStoreArchiveCreateCommand())
	cmd.AddCommand(c.newStoreArchiveVerifyCommand())
	cmd.AddCommand(c.newStoreArchiveRestoreCommand())
	return cmd
}

func (c *RootCLI) newStoreArchiveCreateCommand() *cobra.Command {
	input := storeArchiveCreateInput{
		keepDays: defaultRetentionDays,
		target:   "all",
	}
	cmd := &cobra.Command{
		Use:   "create",
		Short: Localize("Export GC-eligible rows to a versioned archive package", "GC 適格行を版付き archive package に export する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreArchiveCreate(cmd.Context(), cmd.OutOrStdout(), input, cmd.Root().Version)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.output, "output", "", Localize("archive output path (required unless --dry-run)", "archive 出力 path (--dry-run 以外は必須)"))
	cmd.Flags().IntVar(&input.keepDays, "keep-days", defaultRetentionDays, Localize("retention window matching store gc", "store gc と同じ保持日数"))
	cmd.Flags().StringVar(&input.target, "target", "all", Localize("records to archive (events | sessions | memories | memory_edges | all)", "対象 (events | sessions | memories | memory_edges | all)"))
	cmd.Flags().BoolVar(&input.dryRun, "dry-run", false, Localize("print plan counts only; do not write a package", "plan 件数のみ表示。package を書かない"))
	cmd.Flags().BoolVar(&input.deleteAfterVerify, "delete-after-verify", false, Localize("after a successful verify, delete exact archived identities from the live store", "verify 成功後に archive 済み identity を live store から削除"))
	cmd.Flags().StringVar(&input.passphraseEnv, "passphrase-env", "", Localize("env var name holding an optional archive passphrase (never store the secret)", "任意の passphrase を持つ env 名（秘密自体は保存しない）"))
	return cmd
}

func (c *RootCLI) newStoreArchiveVerifyCommand() *cobra.Command {
	input := storeArchiveVerifyInput{}
	cmd := &cobra.Command{
		Use:   "verify",
		Short: Localize("Verify archive package integrity", "archive package の完全性を検証する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreArchiveVerify(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.input, "input", "", Localize("archive input path", "archive 入力 path"))
	cmd.Flags().StringVar(&input.passphraseEnv, "passphrase-env", "", Localize("env var name holding an optional archive passphrase", "任意の passphrase を持つ env 名"))
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func (c *RootCLI) newStoreArchiveRestoreCommand() *cobra.Command {
	input := storeArchiveRestoreInput{}
	cmd := &cobra.Command{
		Use:   "restore",
		Short: Localize("Restore rows from an archive package (idempotent)", "archive package から行を restore する (冪等)"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreArchiveRestore(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.input, "input", "", Localize("archive input path", "archive 入力 path"))
	cmd.Flags().BoolVar(&input.dryRun, "dry-run", false, Localize("report insert/skip/conflict counts without writing", "書き込まず insert/skip/conflict 件数を表示"))
	cmd.Flags().StringVar(&input.passphraseEnv, "passphrase-env", "", Localize("env var name holding an optional archive passphrase", "任意の passphrase を持つ env 名"))
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func (c *RootCLI) runStoreArchiveCreate(ctx context.Context, output io.Writer, input storeArchiveCreateInput, toolVersion string) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if input.keepDays <= 0 {
		return xerrors.New(Localize("--keep-days must be greater than or equal to 1", "keep-days は 1 以上である必要があります"))
	}
	if !input.dryRun && strings.TrimSpace(input.output) == "" {
		return xerrors.New(Localize("--output is required unless --dry-run is set", "--dry-run 以外では --output が必須です"))
	}
	if input.dryRun && input.deleteAfterVerify {
		return xerrors.New(Localize("--delete-after-verify cannot be combined with --dry-run", "--delete-after-verify は --dry-run と併用できません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	target, ok := apptypes.GarbageCollectionTargetFrom(input.target)
	if !ok {
		return xerrors.New(Localize("--target must be one of events, sessions, memories, memory_edges, all", "--target は events, sessions, memories, memory_edges, all のいずれかである必要があります"))
	}
	passphrase, err := readPassphraseEnv(input.passphraseEnv)
	if err != nil {
		return err
	}
	cutoff := gcNowFunc().AddDate(0, 0, -input.keepDays)
	result, err := c.storeManagement.CreateStoreArchive(ctx, apptypes.StoreArchiveCreateParams{
		OutputPath:        input.output,
		Before:            cutoff,
		KeepDays:          input.keepDays,
		Target:            target,
		DryRun:            input.dryRun,
		DeleteAfterVerify: input.deleteAfterVerify,
		Passphrase:        passphrase,
		ToolVersion:  toolVersion,
		SourceDBPath: resolvedDBPath,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("store archive create failed", "store archive create に失敗しました"), err)
	}
	if input.dryRun {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Archive candidates", "archive 候補"), result.TotalRows); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dry-run result", "dry-run 結果の出力に失敗しました"), err)
		}
		for _, t := range result.Tables {
			if _, err := fmt.Fprintf(output, "  %s: %d\n", t.Name, t.RowCount); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print dry-run table row", "dry-run テーブル行の出力に失敗しました"), err)
			}
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Wrote archive", "archive を書き込みました"), result.Path); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print archive path", "archive path の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Rows", "行数"), result.TotalRows); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print archive rows", "archive 行数の出力に失敗しました"), err)
	}
	if result.DeletedAfterVerify {
		if _, err := fmt.Fprintf(output, "%s: %d\n", Localize("Deleted after verify", "verify 後に削除"), result.DeletedCount); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print delete count", "削除件数の出力に失敗しました"), err)
		}
	}
	return nil
}

func (c *RootCLI) runStoreArchiveVerify(ctx context.Context, output io.Writer, input storeArchiveVerifyInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	passphrase, err := readPassphraseEnv(input.passphraseEnv)
	if err != nil {
		return err
	}
	if err := c.storeManagement.VerifyStoreArchive(ctx, input.input, passphrase); err != nil {
		return xerrors.Errorf("%s: %w", Localize("archive verify failed", "archive verify に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n", Localize("Archive OK", "Archive OK")); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print verify result", "verify 結果の出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) runStoreArchiveRestore(ctx context.Context, output io.Writer, input storeArchiveRestoreInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}
	passphrase, err := readPassphraseEnv(input.passphraseEnv)
	if err != nil {
		return err
	}
	result, err := c.storeManagement.RestoreStoreArchive(ctx, input.input, passphrase, input.dryRun)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("archive restore failed", "archive restore に失敗しました"), err)
	}
	mode := "applied"
	if result.DryRun {
		mode = "dry-run"
	}
	if _, err := fmt.Fprintf(output, "archive restore (%s): inserted=%d skipped=%d conflicts=%d total=%d\n",
		mode, result.Inserted, result.Skipped, result.Conflicts, result.TotalInArchive); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print restore result", "restore 結果の出力に失敗しました"), err)
	}
	if result.Conflicts > 0 {
		return xerrors.New(Localize("archive restore reported primary-key conflicts", "archive restore で primary-key 衝突がありました"))
	}
	return nil
}

func readPassphraseEnv(envName string) ([]byte, error) {
	name := strings.TrimSpace(envName)
	if name == "" {
		return nil, nil
	}
	val, ok := os.LookupEnv(name)
	if !ok || val == "" {
		return nil, xerrors.Errorf("passphrase env %s is not set or empty", name)
	}
	return []byte(val), nil
}

