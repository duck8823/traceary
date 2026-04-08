package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

type hooksGuideCommandInput struct {
	client     string
	projectDir string
	outputPath string
}

func (c *RootCLI) newHooksGuideCommand() *cobra.Command {
	var (
		client     string
		projectDir string
		outputPath string
	)

	guideCmd := &cobra.Command{
		Use:   "guide",
		Short: Localize("Print guided setup steps for a supported client", "対応 client 向けの guided setup 手順を出力する"),
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksGuide(cmd.Context(), cmd.OutOrStdout(), hooksGuideCommandInput{
				client:     client,
				projectDir: projectDir,
				outputPath: outputPath,
			})
		},
	}
	guideCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	guideCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory used for project-local client configs", "project-local client config に使う project directory"))
	guideCmd.Flags().StringVar(&outputPath, "output", "", Localize("override the expected config file path", "想定 config file path を上書きする"))
	if err := guideCmd.MarkFlagRequired("client"); err != nil {
		panic(err)
	}

	return guideCmd
}

func (c *RootCLI) runHooksGuide(
	_ context.Context,
	output io.Writer,
	input hooksGuideCommandInput,
) error {
	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve project directory", "project directory の解決に失敗しました"), err)
	}
	guide, err := buildHooksGuide(input.client, resolvedProjectDir, input.outputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build hooks guide", "hooks guide の生成に失敗しました"), err)
	}
	if err := writeHooksGuide(output, guide); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hooks guide", "hooks guide の出力に失敗しました"), err)
	}

	return nil
}

type hooksGuide struct {
	client         string
	outputPath     string
	installCommand string
	doctorCommand  string
	verifyCommand  string
	notes          []string
}

func buildHooksGuide(client string, projectDir string, outputPath string) (*hooksGuide, error) {
	resolvedClient, err := normalizeHooksClient(client)
	if err != nil {
		return nil, err
	}
	resolvedOutputPath, err := resolveHooksInstallOutputPath(resolvedClient, projectDir, outputPath)
	if err != nil {
		return nil, err
	}

	quotedProjectDir := shellQuote(projectDir)
	installCommand := "traceary hooks install --client " + resolvedClient + " --project-dir " + quotedProjectDir
	doctorCommand := "traceary doctor --client " + resolvedClient + " --project-dir " + quotedProjectDir
	verifyCommand := "traceary list --limit 10"

	notes := []string{
		localizef("Expected config path: %s", "想定 config path: %s", resolvedOutputPath),
	}
	switch resolvedClient {
	case "claude":
		notes = append(notes,
			Localize("After installing, start Claude Code in the target project and run at least one Bash command.", "install 後に対象 project で Claude Code を起動し、少なくとも 1 回 Bash command を実行してください。"),
		)
	case "codex":
		notes = append(notes,
			Localize("Codex uses Stop as a best-effort session-end hook. Session start is reliable; session end depends on the installed Codex build.", "Codex は session end に Stop を best-effort で使います。session start は安定していますが、session end は installed Codex build に依存します。"),
		)
	case "gemini":
		notes = append(notes,
			Localize("Gemini requires hooksConfig.enabled=true before Traceary hooks can run.", "Gemini では Traceary hook が動く前に hooksConfig.enabled=true が必要です。"),
		)
	}

	return &hooksGuide{
		client:         resolvedClient,
		outputPath:     resolvedOutputPath,
		installCommand: installCommand,
		doctorCommand:  doctorCommand,
		verifyCommand:  verifyCommand,
		notes:          notes,
	}, nil
}

func writeHooksGuide(output io.Writer, guide *hooksGuide) error {
	if guide == nil {
		return xerrors.Errorf(Localize("hooks guide must not be nil", "hooks guide は nil にできません"))
	}

	if _, err := fmt.Fprintf(output, "TRACEARY HOOKS GUIDE (%s)\n", strings.ToUpper(guide.client)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print guide header", "guide ヘッダーの出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n  %s\n", Localize("Install:", "Install:"), guide.installCommand); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print install step", "install 手順の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n  %s\n", Localize("Check:", "Check:"), guide.doctorCommand); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print check step", "check 手順の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n  %s\n", Localize("Verify:", "Verify:"), guide.verifyCommand); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print verify step", "verify 手順の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, Localize("Notes:", "Notes:")); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print notes header", "notes ヘッダーの出力に失敗しました"), err)
	}
	for _, note := range guide.notes {
		if _, err := fmt.Fprintf(output, "- %s\n", note); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print guide note", "guide note の出力に失敗しました"), err)
		}
	}

	return nil
}
