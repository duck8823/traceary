//nolint:wrapcheck // Cobra boundary preserves typed maintenance errors.
package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

func (c *RootCLI) newStoreSearchMaintenanceCommand() *cobra.Command {
	group := &cobra.Command{Use: "search-maintenance", Short: "Explicitly retire or restore the legacy search projection"}
	group.AddCommand(c.newSearchMaintenanceStatusCommand(), c.newSearchMaintenanceAdoptTargetCommand(), c.newSearchMaintenanceStartRetireCommand(), c.newSearchMaintenanceResumeRetireCommand(), c.newSearchMaintenanceStartRestoreCommand(), c.newSearchMaintenanceResumeRestoreCommand())
	return group
}

func (c *RootCLI) newSearchMaintenanceAdoptTargetCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "adopt-target", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := c.prepareSearchMaintenance(path); err != nil {
			return err
		}
		got, err := c.searchMaintenance.AdoptTarget(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) prepareSearchMaintenance(dbPath string) error {
	if c.searchMaintenance == nil {
		return xerrors.New("search maintenance usecase is not configured")
	}
	resolved, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolved)
	return nil
}
func (c *RootCLI) newSearchMaintenanceStatusCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := c.prepareSearchMaintenance(path); err != nil {
			return err
		}
		got, err := c.searchMaintenance.Inspect(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}
func (c *RootCLI) newSearchMaintenanceStartRetireCommand() *cobra.Command {
	var path, artifact, revision string
	cmd := &cobra.Command{Use: "start-retire", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := c.prepareSearchMaintenance(path); err != nil {
			return err
		}
		data, err := readBoundedEvidence(artifact)
		if err != nil {
			return err
		}
		got, err := c.searchMaintenance.StartRetire(cmd.Context(), data, strings.TrimSpace(revision))
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&artifact, "evidence", "", "same-head parity v2 artifact path")
	cmd.Flags().StringVar(&revision, "expected-revision", "", "expected clean 40-character commit")
	_ = cmd.MarkFlagRequired("evidence")
	_ = cmd.MarkFlagRequired("expected-revision")
	return cmd
}
func readBoundedEvidence(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, xerrors.New("read parity evidence")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, usecase.MaxSearchParityEvidenceBytes+1))
	if err != nil || len(data) > usecase.MaxSearchParityEvidenceBytes {
		return nil, xerrors.New("read parity evidence")
	}
	return data, nil
}
func (c *RootCLI) newSearchMaintenanceResumeRetireCommand() *cobra.Command {
	return c.newSearchMaintenanceBatchCommand("resume-retire", func(cmd *cobra.Command, rows int) (any, error) {
		return c.searchMaintenance.ResumeRetire(cmd.Context(), rows)
	})
}
func (c *RootCLI) newSearchMaintenanceResumeRestoreCommand() *cobra.Command {
	return c.newSearchMaintenanceBatchCommand("resume-restore", func(cmd *cobra.Command, rows int) (any, error) {
		return c.searchMaintenance.ResumeRestore(cmd.Context(), rows)
	})
}
func (c *RootCLI) newSearchMaintenanceBatchCommand(name string, run func(*cobra.Command, int) (any, error)) *cobra.Command {
	var path string
	var rows int
	cmd := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := c.prepareSearchMaintenance(path); err != nil {
			return err
		}
		got, err := run(cmd, rows)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	cmd.Flags().IntVar(&rows, "rows", 128, "maximum legacy projection rows per transaction")
	return cmd
}
func (c *RootCLI) newSearchMaintenanceStartRestoreCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "start-restore", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := c.prepareSearchMaintenance(path); err != nil {
			return err
		}
		got, err := c.searchMaintenance.StartRestore(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	}}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}
