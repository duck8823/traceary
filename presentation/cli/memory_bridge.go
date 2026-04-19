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

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newMemoryExportCommand() *cobra.Command {
	input := memoryExportCommandInput{}
	cmd := &cobra.Command{
		Use:   "export",
		Short: Localize("Export accepted durable memories into a host instruction file", "accepted durable memory をホスト別 instruction file に書き出す"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryExport(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.target, "target", "", Localize(
		"export target host (claude / codex / gemini)",
		"書き出し先ホスト (claude / codex / gemini)",
	))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize(
		"workspace scope to export (defaults to env/detected workspace)",
		"書き出す workspace scope (未指定時は env/検出 workspace)",
	))
	cmd.Flags().StringVar(&input.outPath, "out", "", Localize(
		"output path for the generated instruction file (use - to write to stdout)",
		"書き出し先ファイル (- を指定すると stdout へ出力)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON summary", "JSON 形式で結果サマリを出力する"))
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func (c *RootCLI) newMemoryImportInstructionsCommand() *cobra.Command {
	input := memoryImportInstructionsCommandInput{}
	cmd := &cobra.Command{
		Use:   "instructions",
		Short: Localize("Import bullets from a host instruction file as durable-memory candidates", "ホスト別 instruction file の bullet を candidate durable memory として取り込む"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryImportInstructions(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.source, "source", "", Localize(
		"source host that produced the instruction file (claude / codex / gemini)",
		"instruction file を書いたホスト (claude / codex / gemini)",
	))
	cmd.Flags().StringVar(&input.inPath, "in", "", Localize("input path (CLAUDE.md / AGENTS.md / GEMINI.md)", "読み込むファイル (CLAUDE.md / AGENTS.md / GEMINI.md)"))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize(
		"workspace scope assigned to imported candidates (defaults to env/detected workspace)",
		"取り込む candidate に割り当てる workspace scope (未指定時は env/検出 workspace)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}

func (c *RootCLI) runMemoryExport(ctx context.Context, output io.Writer, warnWriter io.Writer, input memoryExportCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memoryExport == nil {
		return xerrors.Errorf(Localize("memory export usecase is not configured", "memory export ユースケースが設定されていません"))
	}
	target, ok := apptypes.MemoryBridgeTargetOf(strings.ToLower(strings.TrimSpace(input.target)))
	if !ok {
		return xerrors.Errorf(Localize("--target must be one of claude / codex / gemini", "--target は claude / codex / gemini のいずれかを指定してください"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}
	scope, err := resolveExportScope(ctx, input.workspace)
	if err != nil {
		return err
	}
	criteria := apptypes.MemoryExportCriteria{Target: target}
	if scope != nil {
		criteria.Scopes = []domtypes.MemoryScope{scope}
	}
	result, err := c.memoryExport.Export(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to export durable memories", "durable memory の書き出しに失敗しました"), err)
	}

	if err := writeMemoryExportArtifact(output, warnWriter, input.outPath, result); err != nil {
		return err
	}
	if input.asJSON {
		return writeMemoryExportJSONSummary(output, result)
	}
	return nil
}

func (c *RootCLI) runMemoryImportInstructions(ctx context.Context, output io.Writer, warnWriter io.Writer, input memoryImportInstructionsCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memoryBridgeImport == nil {
		return xerrors.Errorf(Localize("memory bridge import usecase is not configured", "memory bridge import ユースケースが設定されていません"))
	}
	target, ok := apptypes.MemoryBridgeTargetOf(strings.ToLower(strings.TrimSpace(input.source)))
	if !ok {
		return xerrors.Errorf(Localize("--source must be one of claude / codex / gemini", "--source は claude / codex / gemini のいずれかを指定してください"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}
	fallback, err := resolveImportFallbackWorkspace(ctx, input.workspace)
	if err != nil {
		return err
	}
	criteria := apptypes.MemoryBridgeImportCriteria{
		Target:            target,
		Path:              input.inPath,
		WorkspaceFallback: fallback,
	}
	result, err := c.memoryBridgeImport.ImportInstructions(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to import instructions file", "instructions ファイルの取り込みに失敗しました"), err)
	}
	// Reuse the Codex memory import renderer so both surfaces emit the
	// same imported / skipped / warnings shape.
	return writeMemoryImportResult(output, warnWriter, apptypes.MemoryImportResult(result), input.asJSON)
}

// resolveExportScope turns the --workspace flag into a WorkspaceScope or
// returns nil so the usecase falls back to the "all accepted memories"
// behaviour. The helper mirrors the inbox path so both surfaces honour
// TRACEARY_WORKSPACE / workspace auto-detection.
func resolveExportScope(ctx context.Context, raw string) (domtypes.MemoryScope, error) {
	resolved := resolveWorkspaceValue(ctx, raw)
	if strings.TrimSpace(resolved) == "" {
		return nil, nil
	}
	workspace, err := domtypes.WorkspaceOf(resolved)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve workspace", "workspace の解決に失敗しました"), err)
	}
	return domtypes.WorkspaceScopeOf(workspace), nil
}

func writeMemoryExportArtifact(output io.Writer, warnWriter io.Writer, outPath string, result apptypes.MemoryExportResult) error {
	trimmed := strings.TrimSpace(outPath)
	if trimmed == "" || trimmed == "-" {
		if _, err := fmt.Fprint(output, result.Markdown); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory export markdown", "memory export の markdown 出力に失敗しました"), err)
		}
		return nil
	}
	if err := os.WriteFile(trimmed, []byte(result.Markdown), 0o644); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write memory export file", "memory export ファイルの書き込みに失敗しました"), err)
	}
	if _, err := fmt.Fprintf(warnWriter, "wrote %d memories to %s\n", result.ExportedCount, trimmed); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory export summary", "memory export サマリの出力に失敗しました"), err)
	}
	return nil
}

func writeMemoryExportJSONSummary(output io.Writer, result apptypes.MemoryExportResult) error {
	payload := struct {
		Target        string `json:"target"`
		ExportedCount int    `json:"exported_count"`
	}{
		Target:        result.Target.String(),
		ExportedCount: result.ExportedCount,
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to encode memory export summary", "memory export サマリの JSON 出力に失敗しました"), err)
	}
	return nil
}
