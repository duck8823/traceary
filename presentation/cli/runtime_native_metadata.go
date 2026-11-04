package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

func (c *RootCLI) newNativeSessionMarkerCommand() *cobra.Command {
	var slot string
	cmd := &cobra.Command{
		Use: "native-session-start", Hidden: true, Args: cobra.NoArgs,
		// No stdout, permission decision, core-store initialization or failure
		// propagation: this distinct hook is optional startup metadata only.
		Run: func(cmd *cobra.Command, _ []string) {
			if c.nativeSessionMetadata == nil || slot == "" {
				return
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			_ = c.nativeSessionMetadata.Observe(ctx, slot, cmd.InOrStdin())
		},
	}
	cmd.Flags().StringVar(&slot, "slot", "", "Internal generated native observation slot")
	return cmd
}
