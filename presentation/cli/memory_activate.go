package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newMemoryActivateCommand() *cobra.Command {
	input := memoryActivateCommandInput{includeGlobal: true}
	cmd := &cobra.Command{
		Use:   "activate",
		Short: Localize("Plan host-native durable-memory activation", "host-native durable-memory activation を計画する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryActivate(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.target, "target", "", Localize("activation target host (codex)", "activation 対象ホスト (codex)"))
	cmd.Flags().StringVar(&input.root, "root", "", Localize("host memory root override (Codex default: ~/.codex/memories)", "host memory root の上書き (Codex 既定: ~/.codex/memories)"))
	cmd.Flags().StringVar(&input.path, "path", "", Localize("explicit activation target file path override", "activation 対象ファイルパスを明示的に上書き"))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("workspace scope to activate (defaults to env/detected workspace)", "activation 対象の workspace scope (未指定時は env/検出 workspace)"))
	cmd.Flags().BoolVar(&input.includeGlobal, "include-global", true, Localize("include global memories alongside a workspace activation (default true)", "workspace activation に global memory も含める (default true)"))
	cmd.Flags().BoolVar(&input.noGlobal, "no-global", false, Localize("activate only the explicit workspace scope; do not include global memories", "明示した workspace scope のみを activation し、global memory は含めない"))
	cmd.Flags().BoolVar(&input.dryRun, "dry-run", false, Localize("print the activation plan without writing files", "ファイルを書き込まず activation plan を表示する"))
	cmd.Flags().BoolVar(&input.apply, "apply", false, Localize("write the activation target file", "activation target file に書き込む"))
	cmd.Flags().BoolVar(&input.diff, "diff", false, Localize("include a diff against the existing target file when present", "既存の対象ファイルがある場合に diff を含める"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func (c *RootCLI) runMemoryActivate(ctx context.Context, output io.Writer, input memoryActivateCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory activation ユースケースが設定されていません"))
	}
	if input.dryRun == input.apply {
		return xerrors.Errorf(Localize("pass exactly one of --dry-run or --apply", "--dry-run または --apply のどちらか一方を指定してください"))
	}
	if input.diff && input.apply {
		return xerrors.Errorf(Localize("--diff can only be used with --dry-run", "--diff は --dry-run と一緒にのみ使用できます"))
	}
	target, ok := apptypes.MemoryBridgeTargetOf(strings.ToLower(strings.TrimSpace(input.target)))
	if !ok || target != apptypes.MemoryBridgeTargetCodex {
		return xerrors.Errorf(Localize("--target must be codex", "--target は codex を指定してください"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}
	scope, err := resolveExportScope(ctx, input.workspace)
	if err != nil {
		return err
	}
	criteria := apptypes.MemoryActivationCriteria{
		Target: target,
		Root:   input.root,
		Path:   input.path,
		Diff:   input.diff,
	}
	if scope != nil {
		criteria.Scopes = []domtypes.MemoryScope{scope}
		criteria.IncludeGlobal = input.includeGlobal && !input.noGlobal
	}
	if input.apply {
		result, err := c.memory.Activate(ctx, criteria)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to activate memories", "memory activation に失敗しました"), err)
		}
		return writeMemoryActivationApplyResult(output, result, input.asJSON)
	}
	plan, err := c.memory.ActivatePlan(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to plan memory activation", "memory activation plan の作成に失敗しました"), err)
	}
	return writeMemoryActivationPlan(output, plan, input.asJSON)
}

func writeMemoryActivationPlan(output io.Writer, plan apptypes.MemoryActivationPlan, asJSON bool) error {
	if asJSON {
		payload := memoryActivationPlanOutput{
			Target:         plan.Target.String(),
			TargetPath:     plan.TargetPath,
			Existing:       plan.Existing,
			ActivatedCount: plan.ActivatedCount,
			Markdown:       plan.Markdown,
			Diff:           plan.Diff,
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode memory activation plan", "memory activation plan の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "target: %s\nexisting: %t\n\n", plan.TargetPath, plan.Existing); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory activation plan", "memory activation plan の出力に失敗しました"), err)
	}
	body := plan.Markdown
	if plan.Diff != "" {
		body = plan.Diff
	}
	if _, err := fmt.Fprint(output, body); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory activation content", "memory activation content の出力に失敗しました"), err)
	}
	return nil
}

func writeMemoryActivationApplyResult(output io.Writer, result apptypes.MemoryActivationApplyResult, asJSON bool) error {
	if asJSON {
		payload := memoryActivationApplyOutput{
			Target:         result.Target.String(),
			TargetPath:     result.TargetPath,
			Action:         result.Action.String(),
			Existing:       result.Existing,
			ActivatedCount: result.ActivatedCount,
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode memory activation result", "memory activation result の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(
		output,
		"target: %s\nactivated_count: %d\naction: %s\n",
		result.TargetPath,
		result.ActivatedCount,
		result.Action.String(),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory activation result", "memory activation result の出力に失敗しました"), err)
	}
	return nil
}
