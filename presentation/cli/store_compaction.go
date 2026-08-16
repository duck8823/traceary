//nolint:wrapcheck // Cobra boundary preserves typed compaction errors.
package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

func (c *RootCLI) newStoreCompactionCommand() *cobra.Command {
	var (
		path     string
		force    bool
		keepDays int
		workDir  string
	)
	cmd := &cobra.Command{
		Use:   "compact",
		Short: Localize("Rewrite the store, dropping reclaimable bodies and retired indexes", "ストアを書き換え、回収できる本文と退役済み index を落とす"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, service, err := c.compactionFor(path)
			if err != nil {
				return err
			}
			result, err := service.Compact(cmd.Context(), application.CompactInput{
				Source:   resolved,
				Force:    force,
				KeepDays: keepDays,
				WorkDir:  workDir,
			})
			if err != nil {
				var unrefined application.UnrefinedMaterialError
				if errors.As(err, &unrefined) {
					return xerrors.Errorf("%s", unrefined.Error())
				}
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"run_id":                      result.Run.ID,
				"phase":                       result.Run.Phase,
				"bytes_before":                result.BytesBefore,
				"bytes_after":                 result.BytesAfter,
				"unrefined_remaining":         result.UnrefinedRemaining,
				"unrefined_bytes":             result.UnrefinedBytes,
				"mechanical_summaries":        result.MechanicalSummaries,
				"released_command_body_rows":  result.ReleasedCommandBodyRows,
				"released_command_body_bytes": result.ReleasedCommandBodyBytes,
				"estimated_reclaimable_bytes": result.EstimatedReclaimableBytes,
				"compact_strategy":            result.CompactStrategy,
				"rollback_path":               result.Run.RollbackPath,
				// Apply-time VerifyPair is not in-use proof. The operator
				// deletes this file when they accept the rewrite (#1827).
				"rollback_retained": result.CompactStrategy != application.CompactStrategyInPlace,
			})
		},
	}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&force, "force", false, Localize("write mechanical summaries for unrefined discardable sessions and discard those bodies", "未 refine の破棄対象へ機械要約を書いて本文を捨てる"))
	cmd.Flags().IntVar(&keepDays, "keep-days", application.DefaultCompactKeepDays, Localize("retain bodies newer than this many days", "この日数より新しい本文は保持する"))
	cmd.Flags().StringVar(&workDir, "work-dir", "", Localize("stage the source-sized work copy on another volume when this volume cannot hold a replica", "このボリュームにレプリカを置けないとき、source サイズの work copy を別ボリュームに置く"))
	cmd.AddCommand(c.newStoreCompactionRollbackCommand())
	return cmd
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
