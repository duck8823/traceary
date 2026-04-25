package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// newMemoryGraphCommand is the v0.9.0 entry point for the additive
// memory-graph overlay introduced for #573. The overlay layers typed
// relationships (supersedes / contradicts / supports / related-to /
// causes) on top of the canonical memory table without moving any
// data into a graph DB. See docs/architecture/temporal-memory.md.
func (c *RootCLI) newMemoryGraphCommand() *cobra.Command {
	graphCmd := &cobra.Command{
		Use:   "graph",
		Short: Localize("Manage typed relationships between memories (graph overlay)", "memory 間の型付き関係 (graph overlay) を管理する"),
	}
	graphCmd.AddCommand(c.newMemoryGraphAddCommand())
	graphCmd.AddCommand(c.newMemoryGraphListCommand())
	return graphCmd
}

func (c *RootCLI) newMemoryGraphAddCommand() *cobra.Command {
	var (
		dbPath    string
		toMemory  string
		relation  string
		validFrom string
		validTo   string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "add <from-memory-id>",
		Short: Localize("Record a typed relationship between two memories", "memory 間の型付き関係を記録する"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runMemoryGraphAdd(cmd.Context(), cmd.OutOrStdout(), memoryGraphAddInput{
				dbPath:    dbPath,
				fromID:    args[0],
				toID:      toMemory,
				relation:  relation,
				validFrom: validFrom,
				validTo:   validTo,
				asJSON:    asJSON,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&toMemory, "to", "", Localize("target memory ID (required)", "関係の対象 memory ID (必須)"))
	cmd.Flags().StringVar(&relation, "relation", "", Localize("relation type (e.g. supersedes, contradicts, supports, related-to, causes)", "関係種別 (例: supersedes, contradicts, supports, related-to, causes)"))
	cmd.Flags().StringVar(&validFrom, "from", "", Localize("validity window lower bound (YYYY-MM-DD or RFC3339); defaults to now", "validity 窓の下限 (YYYY-MM-DD または RFC3339); 既定は現在時刻"))
	cmd.Flags().StringVar(&validTo, "to-date", "", Localize("validity window upper bound (exclusive); open-ended when omitted", "validity 窓の上限 (排他); 省略時は open-ended"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("relation")
	return cmd
}

func (c *RootCLI) newMemoryGraphListCommand() *cobra.Command {
	var (
		dbPath   string
		memoryID string
		relation string
		asOf     string
		limit    int
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List memory edges matching the given filters", "指定した filter に一致する memory edge を表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryGraphList(cmd.Context(), cmd.OutOrStdout(), memoryGraphListInput{
				dbPath:   dbPath,
				memoryID: memoryID,
				relation: relation,
				asOf:     asOf,
				limit:    limit,
				asJSON:   asJSON,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&memoryID, "memory-id", "", Localize("restrict to edges touching this memory (source or target)", "この memory に接続する edge (source / target どちらでも) に絞る"))
	cmd.Flags().StringVar(&relation, "relation", "", Localize("filter by relation type", "関係種別でフィルタ"))
	cmd.Flags().StringVar(&asOf, "as-of", "", Localize("evaluate edge validity at the given timestamp (YYYY-MM-DD or RFC3339)", "指定時刻 (YYYY-MM-DD または RFC3339) で edge の validity を評価する"))
	cmd.Flags().IntVar(&limit, "limit", 50, Localize("maximum number of edges to return (0 = no cap)", "最大取得件数 (0 で上限なし)"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

type memoryGraphAddInput struct {
	dbPath    string
	fromID    string
	toID      string
	relation  string
	validFrom string
	validTo   string
	asJSON    bool
}

type memoryGraphListInput struct {
	dbPath   string
	memoryID string
	relation string
	asOf     string
	limit    int
	asJSON   bool
}

func (c *RootCLI) runMemoryGraphAdd(ctx context.Context, output io.Writer, input memoryGraphAddInput) error {
	if c.memoryEdge == nil {
		return xerrors.Errorf(Localize("memory edge usecase is not configured", "memory edge usecase が設定されていません"))
	}
	resolved, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolved)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	fromID, err := domtypes.MemoryIDFrom(strings.TrimSpace(input.fromID))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse from memory ID", "from memory ID の解析に失敗しました"), err)
	}
	toID, err := domtypes.MemoryIDFrom(strings.TrimSpace(input.toID))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse to memory ID", "to memory ID の解析に失敗しました"), err)
	}
	relation := domtypes.MemoryEdgeRelationOf(input.relation)
	if relation.String() == "" {
		return xerrors.Errorf(Localize("--relation must not be empty", "--relation は空にできません"))
	}
	validFrom, err := parseOptionalValidityTime(input.validFrom)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --from", "--from の解析に失敗しました"), err)
	}
	validTo, err := parseOptionalValidityTime(input.validTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --to-date", "--to-date の解析に失敗しました"), err)
	}

	edge, err := c.memoryEdge.Add(ctx, fromID, toID, relation, validFrom, validTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record memory edge", "memory edge の記録に失敗しました"), err)
	}
	return printMemoryEdge(output, edge, input.asJSON)
}

func (c *RootCLI) runMemoryGraphList(ctx context.Context, output io.Writer, input memoryGraphListInput) error {
	// The CLI contract says only --limit=0 disables the cap; reject
	// negative values up front so SQLite cannot be tricked into
	// returning an unbounded result via `--limit -1`.
	if input.limit < 0 {
		return xerrors.Errorf(Localize("--limit must be greater than or equal to 0 (0 disables the cap)", "--limit は 0 以上である必要があります (0 で上限なし)"))
	}
	if c.memoryEdge == nil {
		return xerrors.Errorf(Localize("memory edge usecase is not configured", "memory edge usecase が設定されていません"))
	}
	resolved, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolved)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	asOf, err := parseOptionalValidityTime(input.asOf)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
	}
	filter := model.MemoryEdgeListFilter{
		MemoryID: domtypes.MemoryID(strings.TrimSpace(input.memoryID)),
		Relation: domtypes.MemoryEdgeRelationOf(input.relation),
		AsOf:     asOf,
		Limit:    input.limit,
	}
	edges, err := c.memoryEdge.List(ctx, filter)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list memory edges", "memory edge 一覧の取得に失敗しました"), err)
	}
	return printMemoryEdges(output, edges, input.asJSON)
}

type memoryEdgeJSON struct {
	ID        string `json:"id"`
	From      string `json:"from_memory_id"`
	To        string `json:"to_memory_id"`
	Relation  string `json:"relation_type"`
	ValidFrom string `json:"valid_from"`
	ValidTo   string `json:"valid_to,omitempty"`
	CreatedAt string `json:"created_at"`
}

func memoryEdgeToJSON(edge *model.MemoryEdge) memoryEdgeJSON {
	out := memoryEdgeJSON{
		ID:        edge.EdgeID().String(),
		From:      edge.FromMemoryID().String(),
		To:        edge.ToMemoryID().String(),
		Relation:  edge.RelationType().String(),
		ValidFrom: formatJSONTime(edge.ValidFrom()),
		CreatedAt: formatJSONTime(edge.CreatedAt()),
	}
	if to, ok := edge.ValidTo().Value(); ok {
		out.ValidTo = formatJSONTime(to)
	}
	return out
}

func printMemoryEdge(output io.Writer, edge *model.MemoryEdge, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(memoryEdgeToJSON(edge)); err != nil {
			return xerrors.Errorf("failed to encode edge JSON: %w", err)
		}
		return nil
	}
	_, err := fmt.Fprintf(
		output,
		"%s\t%s\t%s\t%s\tvalid_from=%s\tvalid_to=%s\n",
		edge.EdgeID(),
		edge.FromMemoryID(),
		edge.RelationType(),
		edge.ToMemoryID(),
		formatJSONTime(edge.ValidFrom()),
		formatOptionalEdgeBound(edge.ValidTo()),
	)
	if err != nil {
		return xerrors.Errorf("failed to print edge: %w", err)
	}
	return nil
}

func printMemoryEdges(output io.Writer, edges []*model.MemoryEdge, asJSON bool) error {
	if asJSON {
		rows := make([]memoryEdgeJSON, 0, len(edges))
		for _, edge := range edges {
			rows = append(rows, memoryEdgeToJSON(edge))
		}
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return xerrors.Errorf("failed to encode edges JSON: %w", err)
		}
		return nil
	}
	if len(edges) == 0 {
		if _, err := fmt.Fprintln(output, Localize("- No matching edges.", "- 一致する edge はありません")); err != nil {
			return xerrors.Errorf("failed to print empty edge list: %w", err)
		}
		return nil
	}
	for _, edge := range edges {
		if err := printMemoryEdge(output, edge, false); err != nil {
			return err
		}
	}
	return nil
}

func formatOptionalEdgeBound(value domtypes.Optional[time.Time]) string {
	if t, ok := value.Value(); ok {
		return formatJSONTime(t)
	}
	return "-"
}
