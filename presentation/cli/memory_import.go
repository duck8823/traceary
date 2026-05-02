package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultCodexMemoryImportInterval = 30 * time.Second
	minCodexMemoryImportInterval     = time.Second
)

func (c *RootCLI) newMemoryImportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: Localize("Import memories from host-native sources as durable-memory candidates", "ホスト固有ソースから durable memory の candidate を取り込む"),
	}
	cmd.AddCommand(c.newMemoryImportCodexCommand())
	cmd.AddCommand(c.newMemoryImportInstructionsCommand())
	return cmd
}

func (c *RootCLI) newMemoryImportCodexCommand() *cobra.Command {
	input := memoryImportCodexCommandInput{
		interval: defaultCodexMemoryImportInterval,
	}
	cmd := &cobra.Command{
		Use:   "codex",
		Short: Localize("Import ~/.codex/memories/*.md as durable-memory candidates", "~/.codex/memories/*.md を durable memory の candidate として取り込む"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryImportCodex(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.root, "root", "", Localize(
		"root directory of the Codex memory layout (defaults to ~/.codex/memories)",
		"Codex memory のルートディレクトリ (既定値は ~/.codex/memories)",
	))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize(
		"workspace scope used when the source file has no applies_to hint",
		"source 側に applies_to ヒントがない場合に使う workspace scope",
	))
	cmd.Flags().BoolVar(&input.watch, "watch", false, Localize(
		"keep polling for additional imports instead of exiting after one run",
		"1回だけではなく定期的に再 import を続ける",
	))
	cmd.Flags().DurationVar(&input.interval, "interval", defaultCodexMemoryImportInterval, Localize(
		"polling interval when --watch is set (minimum 1s)",
		"--watch 指定時の polling interval (最低 1s)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) runMemoryImportCodex(ctx context.Context, output io.Writer, warnWriter io.Writer, input memoryImportCodexCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory import ユースケースが設定されていません"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	root, err := resolveCodexMemoryRoot(input.root)
	if err != nil {
		return err
	}

	fallback, err := resolveImportFallbackWorkspace(ctx, input.workspace)
	if err != nil {
		return err
	}

	if input.watch {
		interval := input.interval
		if interval < minCodexMemoryImportInterval {
			interval = minCodexMemoryImportInterval
		}
		return c.watchCodexImport(ctx, output, warnWriter, apptypes.CodexImportCriteria{Root: root, WorkspaceFallback: fallback}, interval, input.asJSON)
	}

	result, err := c.memory.ImportCodex(ctx, apptypes.CodexImportCriteria{
		Root:              root,
		WorkspaceFallback: fallback,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to import codex memories", "codex memory の取り込みに失敗しました"), err)
	}
	return writeMemoryImportResult(output, warnWriter, result, input.asJSON)
}

func (c *RootCLI) watchCodexImport(
	ctx context.Context,
	output io.Writer,
	warnWriter io.Writer,
	criteria apptypes.CodexImportCriteria,
	interval time.Duration,
	asJSON bool,
) error {
	runOnce := func() error {
		result, err := c.memory.ImportCodex(ctx, criteria)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to import codex memories", "codex memory の取り込みに失敗しました"), err)
		}
		return writeMemoryImportResult(output, warnWriter, result, asJSON)
	}
	if err := runOnce(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := runOnce(); err != nil {
				return err
			}
		}
	}
}

func resolveCodexMemoryRoot(raw string) (string, error) {
	if raw != "" {
		return raw, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve user home directory", "ホームディレクトリの解決に失敗しました"), err)
	}
	return filepath.Join(home, ".codex", "memories"), nil
}

// resolveImportFallbackWorkspace turns the --workspace flag into a fallback
// Workspace the Codex import usecase can hand to candidates that do not
// carry an `applies_to: cwd=...` hint of their own. The helper delegates to
// resolveWorkspaceValue so TRACEARY_WORKSPACE and workspace auto-detection
// behave the same way here as they do elsewhere in the memory command
// family. An unresolved workspace is not an error — it is valid to import
// with no fallback and let the source adapter drop rows that cannot be
// scoped.
func resolveImportFallbackWorkspace(ctx context.Context, raw string) (domtypes.Workspace, error) {
	resolved := resolveWorkspaceValue(ctx, raw)
	if strings.TrimSpace(resolved) == "" {
		return domtypes.Workspace(""), nil
	}
	workspace, err := domtypes.WorkspaceFrom(resolved)
	if err != nil {
		return domtypes.Workspace(""), xerrors.Errorf("%s: %w", Localize("failed to resolve workspace", "workspace の解決に失敗しました"), err)
	}
	return workspace, nil
}

// writeMemoryImportResult prints the human-friendly summary of an import run
// to stdout and replays any parser/usecase warnings to stderr so the main
// text output stays scannable. The JSON format carries the same fields so
// machine consumers do not have to parse the text surface.
func writeMemoryImportResult(output io.Writer, warnWriter io.Writer, result apptypes.MemoryImportResult, asJSON bool) error {
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintln(warnWriter, warning); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory import warning", "memory import warning の出力に失敗しました"), err)
		}
	}
	if asJSON {
		imported := make([]memoryDetailsOutput, 0, len(result.Imported))
		for _, details := range result.Imported {
			imported = append(imported, newMemoryDetailsOutput(details))
		}
		payload := memoryImportOutput{
			Imported:              imported,
			SkippedDuplicateCount: result.SkippedDuplicateCount,
			SkippedRejectedCount:  result.SkippedRejectedCount,
			Warnings:              result.Warnings,
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode memory import result", "memory import 結果の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, Localize(
		"imported=%d duplicates=%d rejected_blocked=%d\n",
		"取り込み=%d 重複=%d 拒否済みスキップ=%d\n",
	), len(result.Imported), result.SkippedDuplicateCount, result.SkippedRejectedCount); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory import summary", "memory import サマリの出力に失敗しました"), err)
	}
	for _, details := range result.Imported {
		summary := details.Summary()
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\n",
			summary.MemoryID().String(),
			summary.MemoryType().String(),
			summary.Scope().Key(),
			summary.Fact(),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print imported memory row", "imported memory 行の出力に失敗しました"), err)
		}
	}
	return nil
}
