package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

type payloadBackfillFlags struct {
	batchRows        int
	stopAfterBatches int64
	dbPath           string
}

func (f *payloadBackfillFlags) config() apptypes.PayloadBackfillConfig {
	return apptypes.PayloadBackfillConfig{
		BatchRows:        f.batchRows,
		StopAfterBatches: f.stopAfterBatches,
	}
}

func bindPayloadBackfillFlags(cmd *cobra.Command, f *payloadBackfillFlags) {
	cmd.Flags().StringVar(&f.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().IntVar(&f.batchRows, "batch-rows", apptypes.DefaultPayloadBackfillBatchRows, "maximum rows per atomic batch")
	cmd.Flags().Int64Var(&f.stopAfterBatches, "stop-after-batches", 0, "pause successfully after this many committed batches (0 means run to completion)")
}

func (c *RootCLI) newStorePayloadBackfillCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "payload-backfill",
		Short: Localize("Encode existing event bodies through the payload codec in place", "既存のイベント本文をペイロード codec でその場エンコードする"),
		Long: Localize(
			"Rewrite events.body through the versioned zstd codec on the live store. "+
				"The run is resumable at batch boundaries, terminates under a rowid high-water, "+
				"and leaves the search projection drifted/stale so operators rebuild it afterwards. "+
				"Physical file size only drops after `store compact`.",
			"ライブストア上で events.body をバージョン付き zstd codec で書き直します。"+
				"バッチ境界で再開可能で、rowid ハイウォーターで終端し、"+
				"検索 projection は drifted/stale になるため完了後に rebuild してください。"+
				"物理的なファイル縮小は `store compact` の後にだけ現れます。",
		),
	}
	for _, name := range []string{"preview", "run", "resume", "status"} {
		name := name
		f := &payloadBackfillFlags{}
		cmd := &cobra.Command{
			Use:  name,
			Args: noArgsLocalized(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return c.runPayloadBackfill(cmd, name, f)
			},
		}
		if name != "status" {
			bindPayloadBackfillFlags(cmd, f)
		} else {
			cmd.Flags().StringVar(&f.dbPath, "db-path", "", dbPathFlagUsage())
		}
		group.AddCommand(cmd)
	}
	return group
}

func (c *RootCLI) runPayloadBackfill(cmd *cobra.Command, operation string, f *payloadBackfillFlags) error {
	if c.payloadBackfill == nil {
		return errors.New("payload backfill is not configured")
	}
	resolvedDBPath, err := resolveDBPath(f.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)

	var result apptypes.PayloadBackfillResult
	switch operation {
	case "preview":
		result, err = c.payloadBackfill.Preview(cmd.Context(), f.config())
	case "run":
		result, err = c.payloadBackfill.Run(cmd.Context(), f.config())
	case "resume":
		result, err = c.payloadBackfill.Resume(cmd.Context(), f.config())
	case "status":
		result, err = c.payloadBackfill.Status(cmd.Context())
	default:
		return errors.New("unsupported payload backfill operation")
	}
	if err != nil {
		return xerrors.Errorf("payload backfill %s failed: %w", operation, err)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return xerrors.Errorf("encode payload backfill result: %w", err)
	}
	return nil
}
