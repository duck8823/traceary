package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

// newBundleCommand is the v0.9 entry point for the local-first
// portability primitive introduced for #572. See
// docs/operations/cross-machine-handoff.md for the recommended
// transport patterns — Traceary ships the file, the operator
// carries it through AirDrop / scp / Syncthing / etc.
func (c *RootCLI) newBundleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: Localize("Export / import encrypted portability bundles", "暗号化された可搬バンドルの export / import"),
	}
	cmd.AddCommand(c.newBundleExportCommand())
	cmd.AddCommand(c.newBundleImportCommand())
	return cmd
}

func (c *RootCLI) newBundleExportCommand() *cobra.Command {
	var (
		dbPath         string
		outPath        string
		fromValue      string
		sinceValue     string
		toValue        string
		untilValue     string
		workspaceValue string
		passphraseEnv  string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: Localize("Export events to an encrypted archive for cross-machine handoff", "マシン間 handoff 用の暗号化済みアーカイブを出力する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runBundleExport(cmd.Context(), cmd.OutOrStdout(), bundleExportInput{
				dbPath:        dbPath,
				outPath:       outPath,
				from:          fromValue,
				since:         sinceValue,
				to:            toValue,
				until:         untilValue,
				workspace:     workspaceValue,
				passphraseEnv: passphraseEnv,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&outPath, "out", "", Localize("output path for the encrypted bundle (required)", "暗号化バンドルの出力先 (必須)"))
	cmd.Flags().StringVar(&fromValue, "from", "", Localize("lower bound for event created_at (YYYY-MM-DD or RFC3339; alias: --since)", "event created_at の下限 (YYYY-MM-DD または RFC3339; 別名: --since)"))
	cmd.Flags().StringVar(&sinceValue, "since", "", Localize("lower bound for event created_at (alias for --from)", "event created_at の下限 (--from の別名)"))
	cmd.Flags().StringVar(&toValue, "to", "", Localize("upper bound for event created_at (exclusive; YYYY-MM-DD or RFC3339; alias: --until)", "event created_at の上限 (排他; YYYY-MM-DD または RFC3339; 別名: --until)"))
	cmd.Flags().StringVar(&untilValue, "until", "", Localize("upper bound for event created_at (exclusive; alias for --to)", "event created_at の上限 (排他; --to の別名)"))
	cmd.Flags().StringVar(&workspaceValue, "workspace", "", Localize("restrict to a single workspace", "1 つの workspace に絞る"))
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "TRACEARY_BUNDLE_PASSPHRASE", Localize("environment variable that carries the encryption passphrase", "暗号化 passphrase を格納した環境変数名"))
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

func (c *RootCLI) newBundleImportCommand() *cobra.Command {
	var (
		dbPath             string
		inPath             string
		passphraseEnv      string
		onConflictValue    string
		missingParentValue string
		orphanEdgesValue   string
		asJSON             bool
	)
	cmd := &cobra.Command{
		Use:   "import",
		Short: Localize("Import an encrypted bundle into the local store (idempotent)", "暗号化バンドルをローカルストアに取り込む (冪等)"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runBundleImport(cmd.Context(), cmd.OutOrStdout(), bundleImportInput{
				dbPath:        dbPath,
				inPath:        inPath,
				passphraseEnv: passphraseEnv,
				onConflict:    onConflictValue,
				missingParent: missingParentValue,
				orphanEdges:   orphanEdgesValue,
				asJSON:        asJSON,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&inPath, "in", "", Localize("input path of the encrypted bundle (required)", "暗号化バンドルの入力パス (必須)"))
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "TRACEARY_BUNDLE_PASSPHRASE", Localize("environment variable that carries the decryption passphrase", "復号 passphrase を格納した環境変数名"))
	cmd.Flags().StringVar(&onConflictValue, "on-conflict", "skip", Localize("UNIQUE conflict policy: skip, replace, or error", "UNIQUE 衝突時の方針: skip, replace, error"))
	cmd.Flags().StringVar(&missingParentValue, "missing-parent", "reject", Localize("policy when an imported session's parent session is absent: reject, skip, or backfill", "import する session の親 session が無い場合の方針: reject, skip, backfill"))
	cmd.Flags().StringVar(&orphanEdgesValue, "orphan-edges", "skip", Localize("memory edge orphan endpoint policy: skip or reject", "memory edge の孤立 endpoint 方針: skip または reject"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON result", "JSON 形式で結果を出力する"))
	_ = cmd.MarkFlagRequired("in")
	return cmd
}

type bundleExportInput struct {
	dbPath        string
	outPath       string
	from          string
	since         string
	to            string
	until         string
	workspace     string
	passphraseEnv string
}

type bundleImportInput struct {
	dbPath        string
	inPath        string
	passphraseEnv string
	onConflict    string
	missingParent string
	orphanEdges   string
	asJSON        bool
}

func (c *RootCLI) runBundleExport(ctx context.Context, output io.Writer, input bundleExportInput) error {
	if c.bundle == nil {
		return xerrors.New(Localize("bundle usecase is not configured", "bundle usecase が設定されていません"))
	}
	passphrase, err := readBundlePassphrase(input.passphraseEnv)
	if err != nil {
		return err
	}
	resolved, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolved)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	fromValue, err := resolveSearchDateValue(input.from, input.since, "from", "since")
	if err != nil {
		return err
	}
	toValue, err := resolveSearchDateValue(input.to, input.until, "to", "until")
	if err != nil {
		return err
	}
	since, err := parseFlexibleTime(fromValue, false)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --from", "--from の解析に失敗しました"), err)
	}
	until, err := parseFlexibleTime(toValue, true)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --to", "--to の解析に失敗しました"), err)
	}

	if err := c.bundle.Export(ctx, usecase.BundleExportOptions{
		OutPath:    input.outPath,
		Passphrase: passphrase,
		Since:      since,
		Until:      until,
		Workspace:  types.Workspace(strings.TrimSpace(input.workspace)),
	}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to export bundle", "bundle の export に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Wrote bundle", "bundle を出力しました"), input.outPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print bundle export result", "bundle export 結果の出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) runBundleImport(ctx context.Context, output io.Writer, input bundleImportInput) error {
	if c.bundle == nil {
		return xerrors.New(Localize("bundle usecase is not configured", "bundle usecase が設定されていません"))
	}
	passphrase, err := readBundlePassphrase(input.passphraseEnv)
	if err != nil {
		return err
	}
	resolved, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolved)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	result, err := c.bundle.Import(ctx, usecase.BundleImportOptions{
		InPath:        input.inPath,
		Passphrase:    passphrase,
		OnConflict:    usecase.BundleConflictPolicy(strings.TrimSpace(input.onConflict)),
		MissingParent: usecase.BundleMissingParentPolicy(strings.TrimSpace(input.missingParent)),
		OrphanEdges:   usecase.BundleOrphanEdgesPolicy(strings.TrimSpace(input.orphanEdges)),
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to import bundle", "bundle の import に失敗しました"), err)
	}
	if input.asJSON {
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(bundleImportOutput{
			SessionsImported:      result.SessionsImported,
			SessionsSkipped:       result.SessionsSkipped,
			EventsImported:        result.EventsImported,
			EventsSkipped:         result.EventsSkipped,
			CommandAuditsImported: result.CommandAuditsImported,
			CommandAuditsSkipped:  result.CommandAuditsSkipped,
			MemoriesImported:      result.MemoriesImported,
			MemoriesSkipped:       result.MemoriesSkipped,
			MemoryEdgesImported:   result.MemoryEdgesImported,
			MemoryEdgesSkipped:    result.MemoryEdgesSkipped,
			BundleSchemaVersion:   result.BundleSchemaVersion,
		}); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print bundle import result", "bundle import 結果の出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(
		output,
		"%s: sessions_imported=%d, sessions_skipped=%d, events_imported=%d, events_skipped=%d, command_audits_imported=%d, command_audits_skipped=%d, memories_imported=%d, memories_skipped=%d, memory_edges_imported=%d, memory_edges_skipped=%d, schema=%d\n",
		Localize("Imported bundle", "bundle を取り込みました"),
		result.SessionsImported, result.SessionsSkipped,
		result.EventsImported, result.EventsSkipped,
		result.CommandAuditsImported, result.CommandAuditsSkipped,
		result.MemoriesImported, result.MemoriesSkipped,
		result.MemoryEdgesImported, result.MemoryEdgesSkipped,
		result.BundleSchemaVersion,
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print bundle import result", "bundle import 結果の出力に失敗しました"), err)
	}
	return nil
}

// readBundlePassphrase returns the passphrase bytes carried in the
// environment variable the caller specified. Traceary never accepts
// a passphrase via a CLI flag — the shell history would retain it
// and --dump-flags audit logs would leak it.
func readBundlePassphrase(envName string) ([]byte, error) {
	trimmed := strings.TrimSpace(envName)
	if trimmed == "" {
		return nil, xerrors.New(Localize(
			"--passphrase-env must not be empty",
			"--passphrase-env は空にできません",
		))
	}
	raw := os.Getenv(trimmed)
	if raw == "" {
		return nil, xerrors.New(Localizef(
			"env var %s is empty; set it before running `bundle export` / `bundle import`",
			"環境変数 %s が空です。`bundle export` / `bundle import` の前に設定してください",
			trimmed,
		))
	}
	return []byte(raw), nil
}
