//nolint:wrapcheck // Cobra boundary preserves typed retirement errors.
package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newStoreSearchRetireCommand() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "search-retire",
		Short: Localize("Drop the legacy migration-032 search index family", "レガシー migration-032 検索インデックス一式を削除する"),
		Long: Localize(
			"Drops event_search_documents, event_search_fts, and event_search_backfill_state in one transaction when present.\n"+
				"Idempotent: if the family is already gone, reports already_removed and exits 0.\n"+
				"Uses straight DROP (not row-by-row DELETE); FTS5 delete markers would otherwise grow the index.\n"+
				"DROP returns pages to the free list — the file does not shrink until `traceary store compact plan/apply`.\n"+
				"On a multi-GiB store this can take a couple of minutes; interruption rolls the transaction back cleanly.",
			"存在するとき event_search_documents / event_search_fts / event_search_backfill_state を1トランザクションで DROP します。\n"+
				"冪等: すでに無い場合は already_removed を報告して終了コード 0 です。\n"+
				"行単位 DELETE ではなく直接 DROP します（FTS5 の削除マーカーでインデックスが肥大化するのを避けるため）。\n"+
				"DROP はページを free list に返すだけで、ファイル自体は `traceary store compact plan/apply` まで縮小しません。\n"+
				"数 GiB 規模では数分かかることがあります。中断時はトランザクションがロールバックされます。",
		),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.legacySearchRetire == nil {
				return xerrors.New("legacy search retire usecase is not configured")
			}
			resolved, err := resolveDBPath(path)
			if err != nil {
				return err
			}
			c.applyDatabasePath(resolved)
			got, err := c.legacySearchRetire.Retire(cmd.Context())
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
		},
	}
	cmd.Flags().StringVar(&path, "db-path", "", dbPathFlagUsage())
	return cmd
}
