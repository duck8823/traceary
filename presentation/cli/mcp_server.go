package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

// MCPServerRunner は MCP server の起動を提供します。
type MCPServerRunner interface {
	// Run は指定 DB を参照して MCP server を起動します。
	Run(ctx context.Context, dbPath string) error
}

func (c *RootCLI) newMCPServerCommand() *cobra.Command {
	var dbPath string

	mcpServerCmd := &cobra.Command{
		Use:   "mcp-server",
		Short: Localize("Run the Traceary MCP server over stdio", "Traceary の MCP server を stdio で起動する"),
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMCPServer(cmd.Context(), cmd.OutOrStdout(), dbPath)
		},
	}
	mcpServerCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage)

	return mcpServerCmd
}

func (c *RootCLI) runMCPServer(ctx context.Context, _ io.Writer, dbPath string) error {
	if c.mcpServerRunner == nil {
		return xerrors.Errorf(Localize("MCP server runner is not configured", "MCP server ランナーが設定されていません"))
	}

	resolvedPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.mcpServerRunner.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to start MCP server", "MCP server の起動に失敗しました"), err)
	}

	return nil
}
