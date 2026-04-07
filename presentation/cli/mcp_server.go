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
		Short: "Traceary の MCP server を stdio で起動する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMCPServer(cmd.Context(), cmd.OutOrStdout(), dbPath)
		},
	}
	mcpServerCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")

	return mcpServerCmd
}

func (c *RootCLI) runMCPServer(ctx context.Context, _ io.Writer, dbPath string) error {
	if c.mcpServerRunner == nil {
		return xerrors.Errorf("MCP server ランナーが設定されていません")
	}

	resolvedPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.mcpServerRunner.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("MCP server の起動に失敗しました: %w", err)
	}

	return nil
}
