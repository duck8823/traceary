package cli

import (
	"context"
	"io"
	"path/filepath"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newDaemonAutoStartCommand(root *string) *cobra.Command {
	group := &cobra.Command{Use: "autostart", Short: "Manage local opt-in and print a descriptor; never install a host job or start agents"}
	open := func(ctx context.Context, create bool) (*app.AutoStart, error) {
		factory, ok := c.runtimeOperators.(app.AutoStartFactory)
		if !ok {
			return nil, app.ErrRuntimeUnsupported
		}
		// Do not silently normalize explicit descriptor or preference targets.
		if *root != "" && filepath.Clean(*root) != *root {
			return nil, app.ErrUnsafeRuntime
		}
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return nil, err
		}
		var service *app.AutoStart
		if create {
			service, err = factory.CreateAutoStart(ctx, path)
		} else {
			service, err = factory.OpenAutoStart(ctx, path)
		}
		if err != nil {
			return nil, xerrors.Errorf("open auto-start metadata: %w", err)
		}
		if service == nil {
			return nil, app.ErrRuntimeProtocol
		}
		return service, nil
	}
	status := &cobra.Command{Use: "status", Short: "Read local opt-in; installed host state remains unknown", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		defer cancel()
		service, err := open(ctx, false)
		if err != nil {
			return err
		}
		value, err := service.Status(ctx)
		if err != nil {
			return xerrors.Errorf("read auto-start status: %w", err)
		}
		return writeRuntimeJSON(cmd, value)
	}}
	var enabled bool
	optIn := &cobra.Command{Use: "opt-in", Short: "Persist explicit --enabled=true|false; no host installation or removal", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("enabled") {
			return app.ErrUnsafeRuntime
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		defer cancel()
		service, err := open(ctx, true)
		if err != nil {
			return err
		}
		value, err := service.SetOptIn(ctx, enabled)
		if err != nil {
			return xerrors.Errorf("set auto-start opt-in: %w", err)
		}
		return writeRuntimeJSON(cmd, value)
	}}
	optIn.Flags().BoolVar(&enabled, "enabled", false, "Explicit local opt-in value; changing it does not install or remove host jobs")
	requireRuntimeFlags(optIn, "enabled")
	descriptor := &cobra.Command{Use: "descriptor", Short: "Print a verified macOS daemon-only plist after opt-in; no installation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		defer cancel()
		service, err := open(ctx, false)
		if err != nil {
			return err
		}
		value, err := service.Descriptor(ctx)
		if err != nil {
			return xerrors.Errorf("render auto-start descriptor: %w", err)
		}
		if _, err = io.WriteString(cmd.OutOrStdout(), value); err != nil {
			return xerrors.Errorf("write auto-start descriptor: %w", err)
		}
		return nil
	}}
	group.AddCommand(status, optIn, descriptor)
	return group
}
