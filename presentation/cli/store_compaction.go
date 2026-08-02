//nolint:wrapcheck // Cobra boundary preserves typed compaction errors.
package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

func (c *RootCLI) newStoreCompactionCommand() *cobra.Command {
	group := &cobra.Command{Use: "compact", Short: "Safely compact and atomically replace the SQLite store"}
	group.AddCommand(c.newStoreCompactionPlanCommand())
	for _, action := range []string{"apply", "resume", "status", "rollback"} {
		group.AddCommand(c.newStoreCompactionRunCommand(action))
	}
	return group
}

func (c *RootCLI) newStoreCompactionPlanCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		resolved, service, err := c.compactionFor(path)
		if err != nil {
			return err
		}
		run, err := service.Plan(cmd.Context(), resolved)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(run)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) newStoreCompactionRunCommand(action string) *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: action + " RUN_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, service, err := c.compactionFor(path)
		if err != nil {
			return err
		}
		var value any
		switch action {
		case "apply":
			value, err = service.Apply(cmd.Context(), args[0])
		case "resume":
			value, err = service.Resume(cmd.Context(), args[0])
		case "status":
			value, err = service.Status(cmd.Context(), args[0])
		case "rollback":
			value, err = service.Rollback(cmd.Context(), args[0])
		default:
			return xerrors.New("unsupported compaction action")
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) compactionFor(path string) (string, application.StoreCompactionUsecase, error) {
	if c.storeCompactionFactory == nil {
		return "", nil, xerrors.New("store compaction is not configured")
	}
	resolved, err := resolveDBPath(path)
	if err != nil {
		return "", nil, err
	}
	return resolved, c.storeCompactionFactory(resolved), nil
}
