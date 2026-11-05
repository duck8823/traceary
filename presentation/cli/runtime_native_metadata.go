package cli

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	"time"

	"github.com/spf13/cobra"
)

// WithAntigravityStatuslineMetadata injects a distinct optional native observer.
// It does not enable recovery or replace the original recording hooks.
func WithAntigravityStatuslineMetadata(sink app.NativeSessionMetadataSink) RootCLIOption {
	return func(c *RootCLI) { c.agyStatuslineMetadata = sink }
}
func (c *RootCLI) newNativeSessionMarkerCommand() *cobra.Command {
	return newNativeMetadataPlumbingCommand("native-session-start", c.nativeSessionMetadata)
}
func (c *RootCLI) newAntigravityStatuslineCommand() *cobra.Command {
	return newNativeMetadataPlumbingCommand("native-antigravity-statusline", c.agyStatuslineMetadata)
}
func newNativeMetadataPlumbingCommand(name string, sink app.NativeSessionMetadataSink) *cobra.Command {
	var slot string
	cmd := &cobra.Command{
		Use: name, Hidden: true, Args: cobra.NoArgs,
		// No stdout, permission decision, core-store initialization or failure
		// propagation: this distinct hook is optional startup metadata only.
		Run: func(cmd *cobra.Command, _ []string) {
			if sink == nil || slot == "" {
				return
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			_ = sink.Observe(ctx, slot, cmd.InOrStdin())
		},
	}
	cmd.Flags().StringVar(&slot, "slot", "", "Internal generated native observation slot")
	return cmd
}
