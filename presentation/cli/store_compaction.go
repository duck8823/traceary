//nolint:wrapcheck // Cobra boundary preserves typed compaction errors.
package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
)

func (c *RootCLI) newStoreCompactionCommand() *cobra.Command {
	var (
		path    string
		workDir string
	)

	cmd := &cobra.Command{
		Use:   "compact",
		Short: Localize("Rewrite the store, reclaiming free pages and dropping retired indexes", "ストアを書き換え、空きページを回収し退役済み index を落とす"),
		Long: Localize(
			"Rewrite the store, reclaiming free pages and dropping retired indexes.",
			"ストアを書き換え、空きページを回収し退役済み index を落とします。",
		),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreCompact(cmd, storeCompactInput{
				path:    path,
				workDir: workDir,
			})
		},
	}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&workDir, "work-dir", "", Localize("stage the source-sized work copy on another volume when this volume cannot hold a replica", "このボリュームにレプリカを置けないとき、source サイズの work copy を別ボリュームに置く"))
	cmd.AddCommand(c.newStoreCompactionRollbackCommand())
	return cmd
}

type storeCompactInput struct {
	path    string
	workDir string
}

func (c *RootCLI) runStoreCompact(cmd *cobra.Command, input storeCompactInput) error {
	resolved, service, err := c.compactionFor(input.path)
	if err != nil {
		return err
	}
	usecase.BindCompactionProgress(service, newCLICompactionProgress(cmd.ErrOrStderr()))
	result, err := service.Compact(cmd.Context(), application.CompactInput{
		Source:  resolved,
		WorkDir: input.workDir,
	})
	if err != nil {
		if result.CompactStrategy == "" {
			return err
		}
		// Rewrite already finished; still emit compact_strategy JSON (#2298).
		if encodeErr := encodeCompactResultJSON(cmd, result); encodeErr != nil {
			return encodeErr
		}
		return err
	}
	return encodeCompactResultJSON(cmd, result)
}

func encodeCompactResultJSON(cmd *cobra.Command, result application.CompactResult) error {
	payload := map[string]any{
		"run_id":                      result.Run.ID,
		"phase":                       result.Run.Phase,
		"bytes_before":                result.BytesBefore,
		"bytes_after":                 result.BytesAfter,
		"released_command_body_rows":  result.ReleasedCommandBodyRows,
		"released_command_body_bytes": result.ReleasedCommandBodyBytes,
		"estimated_reclaimable_bytes": result.EstimatedReclaimableBytes,
		"compact_strategy":            result.CompactStrategy,
		"rollback_path":               result.Run.RollbackPath,
		// Apply-time VerifyPair is not in-use proof. The operator
		// deletes this file when they accept the rewrite (#1827).
		"rollback_retained": result.CompactStrategy != application.CompactStrategyInPlace,
	}
	if steps := compactStepsJSON(result.Steps); len(steps) > 0 {
		payload["steps"] = steps
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(payload)
}

func compactStepsJSON(steps application.CompactSteps) map[string]any {
	if len(steps) == 0 {
		return nil
	}
	out := make(map[string]any, len(steps))
	for _, step := range steps {
		detail := step.Detail
		if detail == nil {
			detail = map[string]int64{}
		}
		out[step.Name] = map[string]any{
			"rows":            step.Rows,
			"bytes_before":    step.BytesBefore,
			"bytes_after":     step.BytesAfter,
			"bytes_reclaimed": step.BytesReclaimed,
			"skipped":         step.Skipped,
			"detail":          detail,
		}
	}
	return out
}

func (c *RootCLI) newStoreCompactionRollbackCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "rollback RUN_ID",
		Short: Localize("Restore the pre-compact store from the rollback inode", "rollback inode から compact 前のストアを戻す"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := c.compactionFor(path)
			if err != nil {
				return err
			}
			value, err := service.Rollback(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
		},
	}
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
