package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newStoreCapacityCommand() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "capacity",
		Short: Localize("Report metadata-only SQLite capacity attribution", "SQLite 容量のメタデータ限定内訳を報告する"),
		Args:  noArgsLocalized(),
		RunE:  func(cmd *cobra.Command, _ []string) error { return c.runStoreCapacity(cmd, cmd.OutOrStdout(), dbPath) },
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Long = Localize(
		"Emits traceary.capacity/v1 JSON containing only aggregate page, object, payload-size-class, free-page, and WAL measurements. It never emits stored values or identifiers. If SQLite dbstat is unavailable, evidence.status is unavailable and object attribution is omitted.",
		"保存値や識別子を含めず、ページ、オブジェクト、payload サイズ区分、空きページ、WAL の集計だけを traceary.capacity/v1 JSON で出力します。SQLite dbstat が利用できない場合は evidence.status が unavailable となり、オブジェクト内訳を省略します。",
	)
	return cmd
}

func (c *RootCLI) runStoreCapacity(cmd *cobra.Command, output io.Writer, dbPath string) error {
	if c.capacityInspector == nil {
		return xerrors.New(Localize("capacity inspector is not configured", "容量インスペクターが設定されていません"))
	}
	resolved, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolved)
	report, err := c.capacityInspector.InspectCapacity(cmd.Context())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to inspect store capacity", "ストア容量の検査に失敗しました"), err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("failed to encode capacity report: %w", err)
	}
	return nil
}
