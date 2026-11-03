package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	appruntime "github.com/duck8823/traceary/application/runtime"
)

func (c *RootCLI) newDaemonCommand() *cobra.Command {
	var root string
	group := &cobra.Command{Use: "daemon", Short: "Manage the owner-scoped local runtime transport"}
	group.PersistentFlags().StringVar(&root, "runtime-dir", "", "Absolute private runtime directory (default: XDG state home/traceary/desktop/runtime)")
	serve := &cobra.Command{Use: "serve", Short: "Serve the local Unix transport in the foreground", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.runtimeDaemon == nil {
			return appruntime.ErrRuntimeUnsupported
		}
		path, err := resolveRuntimeDir(root)
		if err != nil {
			return err
		}
		if err = c.runtimeDaemon.Serve(cmd.Context(), path); err != nil {
			return xerrors.Errorf("serve local runtime: %w", err)
		}
		return nil
	}}
	var asJSON bool
	status := &cobra.Command{Use: "status", Short: "Query transport readiness without starting a daemon", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.runtimeDaemon == nil {
			return appruntime.ErrRuntimeUnsupported
		}
		path, err := resolveRuntimeDir(root)
		if err != nil {
			return err
		}
		state, err := c.runtimeDaemon.Status(cmd.Context(), path)
		if err != nil {
			return xerrors.Errorf("query local runtime: %w", err)
		}
		if asJSON {
			value := struct {
				Version             int      `json:"protocol_version"`
				Epoch               string   `json:"epoch"`
				State               string   `json:"state"`
				Capabilities        []string `json:"capabilities"`
				ObservationDelivery string   `json:"observation_delivery"`
			}{state.ProtocolVersion, state.Epoch, state.State, state.Capabilities, state.ObservationDelivery}
			if err = json.NewEncoder(cmd.OutOrStdout()).Encode(value); err != nil {
				return xerrors.Errorf("write daemon status: %w", err)
			}
			return nil
		}
		if _, err = fmt.Fprintf(cmd.OutOrStdout(), "Local transport: %s (protocol %d, epoch %s)\n", state.State, state.ProtocolVersion, state.Epoch); err != nil {
			return xerrors.Errorf("write daemon status: %w", err)
		}
		return nil
	}}
	status.Flags().BoolVar(&asJSON, "json", false, "Print structured transport readiness (not agent liveness)")
	group.AddCommand(serve, status)
	return group
}
func resolveRuntimeDir(value string) (string, error) {
	if value == "" {
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", xerrors.Errorf("resolve runtime home: %w", err)
			}
			state = filepath.Join(home, ".local", "state")
		}
		if !filepath.IsAbs(state) {
			return "", appruntime.ErrUnsafeRuntime
		}
		value = filepath.Join(state, "traceary", "desktop", "runtime")
	}
	if !filepath.IsAbs(value) {
		return "", appruntime.ErrUnsafeRuntime
	}
	return filepath.Clean(value), nil
}
