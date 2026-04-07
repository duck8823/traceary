package cli

import (
	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/application/usecase"
)

// RootCLI は traceary のルートコマンドを提供します。
type RootCLI struct {
	initializeStoreUsecase usecase.InitializeStoreUsecase
}

// NewRootCLI は新しい RootCLI を生成します。
func NewRootCLI(initializeStoreUsecase usecase.InitializeStoreUsecase) *RootCLI {
	return &RootCLI{initializeStoreUsecase: initializeStoreUsecase}
}

// Command は traceary のルートコマンドを返します。
func (c *RootCLI) Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          "traceary",
		Short:        "AI エージェントの作業履歴をローカルに記録する CLI",
		SilenceUsage: true,
	}
	rootCmd.AddCommand(c.newInitCommand())
	return rootCmd
}
