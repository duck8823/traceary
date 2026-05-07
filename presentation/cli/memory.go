package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newMemoryCommand() *cobra.Command {
	memoryCmd := &cobra.Command{
		Use:   "memory",
		Short: Localize("Manage durable memories", "durable memory を管理する"),
	}
	// Daily read path (top-level, unchanged).
	memoryCmd.AddCommand(c.newMemoryListCommand())
	memoryCmd.AddCommand(c.newMemorySearchCommand())
	memoryCmd.AddCommand(c.newMemoryShowCommand())
	// Grouped namespaces by operator intent (#922).
	memoryCmd.AddCommand(c.newMemoryInboxCommand())
	memoryCmd.AddCommand(c.newMemoryStoreCommand())
	memoryCmd.AddCommand(c.newMemoryAdminCommand())
	// Hidden deprecated aliases preserve every legacy flat path
	// through one release of overlap; they are scheduled for removal
	// in v0.15 (see docs/operations/memory-command-surface.md).
	c.addDeprecatedMemoryAliases(memoryCmd)
	return memoryCmd
}

func (c *RootCLI) newMemoryListCommand() *cobra.Command {
	input := memoryListCommandInput{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List durable memories", "durable memory を一覧表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryList(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("filter by workspace scope (defaults to env/detected workspace when no other scope filter is set)", "workspace scope で絞り込む (他の scope filter がない場合は env/検出 workspace を使用)"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("filter by agent scope", "agent scope で絞り込む"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("filter by session-family scope", "session-family scope で絞り込む"))
	cmd.Flags().StringSliceVar(&input.statuses, "status", nil, Localize("filter by memory lifecycle status", "memory の lifecycle status で絞り込む"))
	cmd.Flags().StringSliceVar(&input.memoryTypes, "type", nil, Localize("filter by memory type", "memory type で絞り込む"))
	cmd.Flags().StringSliceVar(&input.sources, "source", nil, Localize("filter by memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported)", "memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported) で絞り込む"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden candidates (low-quality auto-extractions kept for audit)", "extracted-hidden の候補も含める (audit 用に保存された低品質自動抽出)"))
	cmd.Flags().IntVar(&input.limit, "limit", 20, Localize("maximum number of memories to return", "表示件数"))
	cmd.Flags().IntVar(&input.offset, "offset", 0, Localize("number of memories to skip before listing", "一覧表示前にスキップする件数"))
	cmd.Flags().StringVar(&input.asOf, "as-of", "", Localize("evaluate memory validity as of this timestamp (`YYYY-MM-DD` or RFC3339, defaults to now)", "この時点の validity で評価する (`YYYY-MM-DD` または RFC3339、既定は now)"))
	cmd.Flags().BoolVar(&input.includeExpired, "include-expired", false, Localize("include memories whose validTo is in the past (bypass the default validity-window filter)", "validTo が過去の memory も含める (既定の validity-window filter をバイパス)"))
	cmd.Flags().StringVar(&input.preset, "preset", "", Localize("apply a built-in retrieval preset (resume | review | incident); explicit filters still override preset defaults", "built-in の retrieval preset を適用する (resume | review | incident)。明示的な filter は preset を上書きする"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemorySearchCommand() *cobra.Command {
	input := memorySearchCommandInput{}
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: Localize("Search durable memories", "durable memory を検索する"),
		Args:  maximumNArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.query = ""
			if len(args) == 1 {
				input.query = args[0]
			}
			return c.runMemorySearch(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("filter by workspace scope", "workspace scope で絞り込む"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("filter by agent scope", "agent scope で絞り込む"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("filter by session-family scope", "session-family scope で絞り込む"))
	cmd.Flags().StringSliceVar(&input.statuses, "status", nil, Localize("filter by memory lifecycle status", "memory の lifecycle status で絞り込む"))
	cmd.Flags().StringSliceVar(&input.memoryTypes, "type", nil, Localize("filter by memory type", "memory type で絞り込む"))
	cmd.Flags().StringSliceVar(&input.sources, "source", nil, Localize("filter by memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported)", "memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported) で絞り込む"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden candidates (low-quality auto-extractions kept for audit)", "extracted-hidden の候補も含める (audit 用に保存された低品質自動抽出)"))
	cmd.Flags().IntVar(&input.limit, "limit", 20, Localize("maximum number of memories to return", "表示件数"))
	cmd.Flags().IntVar(&input.offset, "offset", 0, Localize("number of memories to skip before returning results", "結果を返す前にスキップする件数"))
	cmd.Flags().StringVar(&input.asOf, "as-of", "", Localize("evaluate memory validity as of this timestamp (`YYYY-MM-DD` or RFC3339, defaults to now)", "この時点の validity で評価する (`YYYY-MM-DD` または RFC3339、既定は now)"))
	cmd.Flags().BoolVar(&input.includeExpired, "include-expired", false, Localize("include memories whose validTo is in the past (bypass the default validity-window filter)", "validTo が過去の memory も含める (既定の validity-window filter をバイパス)"))
	cmd.Flags().StringVar(&input.preset, "preset", "", Localize("apply a built-in retrieval preset (resume | review | incident); explicit filters still override preset defaults", "built-in の retrieval preset を適用する (resume | review | incident)。明示的な filter は preset を上書きする"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryShowCommand() *cobra.Command {
	var (
		dbPath string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "show <memory-id>",
		Short: Localize("Show durable memory details", "durable memory の詳細を表示する"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runMemoryShow(cmd.Context(), cmd.OutOrStdout(), dbPath, args[0], asJSON)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryRememberCommand() *cobra.Command {
	input := memoryWriteCommandInput{}
	cmd := &cobra.Command{
		Use:   "remember",
		Short: Localize("Record an accepted durable memory", "accepted な durable memory を記録する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryRemember(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	configureMemoryWriteFlags(cmd, &input)
	return cmd
}

func (c *RootCLI) newMemoryProposeCommand() *cobra.Command {
	input := memoryWriteCommandInput{}
	cmd := &cobra.Command{
		Use:   "propose",
		Short: Localize("Record a candidate durable memory", "candidate な durable memory を記録する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryPropose(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	configureMemoryWriteFlags(cmd, &input)
	return cmd
}

func (c *RootCLI) newMemoryDistillCommand() *cobra.Command {
	input := memoryDistillCommandInput{}
	cmd := &cobra.Command{
		Use:   "distill",
		Short: Localize("Distill candidate memories into an accepted fact", "candidate memory を accepted fact に distill する"),
		Long: Localize(
			"Distill one or more candidate memories into a new accepted durable memory using an operator-provided fact. Traceary preserves the union of evidence and artifact refs from the source candidates and never rewrites content automatically.",
			"1つ以上の candidate memory を、operator が指定した fact で新しい accepted durable memory に distill します。Traceary は source candidate の evidence / artifact ref の union を保持し、内容を自動で書き換えません。",
		),
		Example: strings.Join([]string{
			"  traceary memory distill --from memory-f332,memory-7f83 --type constraint --workspace github.com/org/repo --fact 'SNS Publish error mapping must preserve AWS SDK v2 SNS error classes.' --replace=supersede",
			"  traceary memory distill --from memory-raw --type lesson --agent codex --fact 'Wait for Codex review completion before merging.' --replace=reject",
		}, "\n"),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryDistill(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.fromIDs, "from", nil, Localize("comma-separated source candidate memory ids (repeatable)", "source candidate memory id をカンマ区切りで指定 (複数指定可)"))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("accepted memory workspace scope", "accepted memory の workspace scope"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("accepted memory agent scope", "accepted memory の agent scope"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("accepted memory session-family scope", "accepted memory の session-family scope"))
	cmd.Flags().StringVar(&input.memoryType, "type", "", Localize("accepted memory type", "accepted memory type"))
	cmd.Flags().StringVar(&input.fact, "fact", "", Localize("operator-provided distilled memory fact", "operator が指定する distilled memory fact"))
	cmd.Flags().StringVar(&input.confidence, "confidence", "", Localize("accepted confidence (defaults to verified)", "accepted 時の confidence (既定値は verified)"))
	cmd.Flags().StringVar(&input.source, "source", "", Localize("memory source (defaults to manual)", "memory source (既定値は manual)"))
	cmd.Flags().StringVar(&input.replace, "replace", "keep", Localize("source candidate handling: keep, reject, or supersede", "source candidate の扱い: keep, reject, supersede"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	cmd.MarkFlagsMutuallyExclusive("workspace", "agent", "session-family")
	return cmd
}

func (c *RootCLI) newMemoryAcceptCommand() *cobra.Command {
	input := memoryMutationCommandInput{}
	cmd := &cobra.Command{
		Use:   "accept <memory-id>",
		Short: Localize("Accept a candidate durable memory", "candidate durable memory を accept する"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemoryAccept(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.confidence, "confidence", "", Localize("accepted confidence (defaults to verified)", "accepted 時の confidence (既定値は verified)"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

func (c *RootCLI) newMemoryRejectCommand() *cobra.Command {
	input := memoryMutationCommandInput{}
	cmd := &cobra.Command{
		Use:   "reject <memory-id>",
		Short: Localize("Reject a candidate durable memory", "candidate durable memory を reject する"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemoryReject(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

func (c *RootCLI) newMemorySupersedeCommand() *cobra.Command {
	input := memorySupersedeCommandInput{}
	cmd := &cobra.Command{
		Use:   "supersede <memory-id>",
		Short: Localize("Replace an accepted durable memory", "accepted durable memory を置き換える"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemorySupersede(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("replacement workspace scope (inherits the current memory scope when omitted)", "置換後の workspace scope (未指定時は現在の memory scope を継承)"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("replacement agent scope", "置換後の agent scope"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("replacement session-family scope", "置換後の session-family scope"))
	cmd.Flags().StringVar(&input.memoryType, "type", "", Localize("replacement memory type (inherits when omitted)", "置換後の memory type (未指定時は継承)"))
	cmd.Flags().StringVar(&input.fact, "fact", "", Localize("distilled memory fact", "記録する memory fact"))
	cmd.Flags().StringVar(&input.confidence, "confidence", "", Localize("replacement confidence (defaults to verified)", "置換後の confidence (既定値は verified)"))
	cmd.Flags().StringVar(&input.source, "source", "", Localize("memory source (defaults to manual)", "memory source (既定値は manual)"))
	cmd.Flags().StringArrayVar(&input.evidenceRefs, "evidence", nil, Localize("evidence ref as kind:value (repeatable)", "kind:value 形式の evidence ref (複数指定可)"))
	cmd.Flags().StringArrayVar(&input.artifactRefs, "artifact", nil, Localize("artifact ref as kind:value (repeatable)", "kind:value 形式の artifact ref (複数指定可)"))
	cmd.Flags().StringVar(&input.validFrom, "from", "", Localize("replacement validFrom (YYYY-MM-DD or RFC3339). Defaults to now when omitted.", "置換後の validFrom (YYYY-MM-DD または RFC3339)。省略時は現在時刻。"))
	cmd.Flags().StringVar(&input.validTo, "to", "", Localize("replacement validTo (YYYY-MM-DD or RFC3339). Defaults to open-ended when omitted.", "置換後の validTo (YYYY-MM-DD または RFC3339)。省略時は open-ended。"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	cmd.MarkFlagsMutuallyExclusive("workspace", "agent", "session-family")
	return cmd
}

func (c *RootCLI) newMemoryExpireCommand() *cobra.Command {
	input := memoryMutationCommandInput{}
	cmd := &cobra.Command{
		Use:   "expire <memory-id>",
		Short: Localize("Expire an active durable memory", "active な durable memory を expire する"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemoryExpire(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.expiresAt, "at", "", Localize("expiry time (`YYYY-MM-DD` or RFC3339, defaults to now)", "expiry time (`YYYY-MM-DD` または RFC3339。既定値は now)"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

func (c *RootCLI) newMemorySetValidityCommand() *cobra.Command {
	input := memoryValidityCommandInput{}
	cmd := &cobra.Command{
		Use:   "set-validity <memory-id>",
		Short: Localize("Set the content validity window (valid_from / valid_to) on a durable memory", "durable memory のコンテンツ有効期間 (valid_from / valid_to) を設定する"),
		Long: Localize(
			"Set or update the content validity window on a durable memory. validFrom and validTo describe when the fact is asserted to be true, separately from the lifecycle `expires_at` metadata written by `memory expire`. Omit a flag to leave that bound unchanged. Use --clear-to to remove an existing validTo (return to open-ended).",
			"durable memory のコンテンツ有効期間 (valid_from / valid_to) を設定します。これは fact が真として主張される期間であり、`memory expire` で記録される lifecycle `expires_at` とは別の軸です。フラグを省略すればその境界は変更しません。既存の validTo を外して再び open-ended に戻すには --clear-to を指定してください。",
		),
		Example: strings.Join([]string{
			"  traceary memory set-validity <id> --from 2026-04-20 --to 2026-07-01",
			"  traceary memory set-validity <id> --to 2026-12-31",
			"  traceary memory set-validity <id> --clear-to",
		}, "\n"),
		Args: exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemorySetValidity(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.validFrom, "from", "", Localize("start of validity window (`YYYY-MM-DD` or RFC3339)", "validity window 開始 (`YYYY-MM-DD` または RFC3339)"))
	cmd.Flags().StringVar(&input.validTo, "to", "", Localize("end of validity window (`YYYY-MM-DD` or RFC3339)", "validity window 終了 (`YYYY-MM-DD` または RFC3339)"))
	cmd.Flags().BoolVar(&input.clearTo, "clear-to", false, Localize("clear the existing validTo (return to open-ended validity)", "既存の validTo を外して open-ended に戻す"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	cmd.MarkFlagsMutuallyExclusive("to", "clear-to")
	return cmd
}

func configureMemoryWriteFlags(cmd *cobra.Command, input *memoryWriteCommandInput) {
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("workspace scope (defaults to env/detected workspace when no other scope is set)", "workspace scope (他の scope がない場合は env/検出 workspace を使用)"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("agent scope", "agent scope"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("session-family scope", "session-family scope"))
	cmd.Flags().StringVar(&input.memoryType, "type", "", Localize("memory type", "memory type"))
	cmd.Flags().StringVar(&input.fact, "fact", "", Localize("distilled memory fact", "記録する memory fact"))
	cmd.Flags().StringVar(&input.confidence, "confidence", "", Localize("accepted confidence (defaults to verified for remember)", "accepted 時の confidence (remember の既定値は verified)"))
	cmd.Flags().StringVar(&input.source, "source", "", Localize("memory source (defaults to manual)", "memory source (既定値は manual)"))
	cmd.Flags().StringArrayVar(&input.evidenceRefs, "evidence", nil, Localize("evidence ref as kind:value (repeatable)", "kind:value 形式の evidence ref (複数指定可)"))
	cmd.Flags().StringArrayVar(&input.artifactRefs, "artifact", nil, Localize("artifact ref as kind:value (repeatable)", "kind:value 形式の artifact ref (複数指定可)"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the resulting memory ID", "結果の memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	cmd.MarkFlagsMutuallyExclusive("workspace", "agent", "session-family")
}

func (c *RootCLI) runMemoryList(ctx context.Context, output io.Writer, input memoryListCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.Errorf(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset < 0 {
		return xerrors.Errorf(Localize("offset must be greater than or equal to 0", "offset は 0 以上である必要があります"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	scopes, err := resolveMemoryFilterScopes(ctx, input.workspace, input.agent, input.sessionFamily, true)
	if err != nil {
		return err
	}
	statuses, err := parseMemoryStatuses(input.statuses)
	if err != nil {
		return err
	}
	memoryTypes, err := parseMemoryTypes(input.memoryTypes)
	if err != nil {
		return err
	}
	sources, err := parseMemorySources(input.sources)
	if err != nil {
		return err
	}
	sources = applyExtractedHiddenDefault(sources, input.includeHidden)

	asOf, err := parseOptionalValidityTime(input.asOf)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
	}
	preset, err := apptypes.MemoryRetrievalPresetOf(input.preset)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --preset", "--preset の解析に失敗しました"), err)
	}
	criteriaBuilder := apptypes.NewMemoryListCriteriaBuilder(input.limit).Offset(input.offset).Scopes(scopes)
	if preset != "" {
		criteriaBuilder = preset.ApplyToMemoryListCriteriaBuilder(criteriaBuilder)
	}
	// Explicit --status / --type still win: setting them after the
	// preset overwrites the preset's defaults. Skip the call when the
	// user didn't pass anything so the preset's choice stays in effect.
	if len(statuses) > 0 {
		criteriaBuilder = criteriaBuilder.Statuses(statuses)
	}
	if len(memoryTypes) > 0 {
		criteriaBuilder = criteriaBuilder.MemoryTypes(memoryTypes)
	}
	if len(sources) > 0 {
		criteriaBuilder = criteriaBuilder.Sources(sources)
	}
	criteriaBuilder = criteriaBuilder.IncludeExpiredByValidity(input.includeExpired)
	if t, ok := asOf.Value(); ok {
		criteriaBuilder = criteriaBuilder.AsOf(t)
	}
	criteria := criteriaBuilder.Build()
	summaries, err := c.memory.List(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list durable memories", "durable memory の一覧取得に失敗しました"), err)
	}
	if err := writeMemorySummariesByFormat(output, summaries, input.asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory list", "durable memory 一覧の出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) runMemorySearch(ctx context.Context, output io.Writer, input memorySearchCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.Errorf(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset < 0 {
		return xerrors.Errorf(Localize("offset must be greater than or equal to 0", "offset は 0 以上である必要があります"))
	}
	if !hasMemorySearchInputConstraint(input) {
		return xerrors.Errorf(Localize("at least one search filter is required", "検索条件は1つ以上必要です"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	scopes, err := resolveMemoryFilterScopes(ctx, input.workspace, input.agent, input.sessionFamily, false)
	if err != nil {
		return err
	}
	statuses, err := parseMemoryStatuses(input.statuses)
	if err != nil {
		return err
	}
	memoryTypes, err := parseMemoryTypes(input.memoryTypes)
	if err != nil {
		return err
	}
	sources, err := parseMemorySources(input.sources)
	if err != nil {
		return err
	}
	sources = applyExtractedHiddenDefault(sources, input.includeHidden)

	asOf, err := parseOptionalValidityTime(input.asOf)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
	}
	preset, err := apptypes.MemoryRetrievalPresetOf(input.preset)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --preset", "--preset の解析に失敗しました"), err)
	}
	criteriaBuilder := apptypes.NewMemorySearchCriteriaBuilder(input.limit).
		Query(strings.TrimSpace(input.query)).
		Offset(input.offset).
		Scopes(scopes)
	if preset != "" {
		criteriaBuilder = preset.ApplyToMemorySearchCriteriaBuilder(criteriaBuilder)
	}
	if len(statuses) > 0 {
		criteriaBuilder = criteriaBuilder.Statuses(statuses)
	}
	if len(memoryTypes) > 0 {
		criteriaBuilder = criteriaBuilder.MemoryTypes(memoryTypes)
	}
	if len(sources) > 0 {
		criteriaBuilder = criteriaBuilder.Sources(sources)
	}
	criteriaBuilder = criteriaBuilder.IncludeExpiredByValidity(input.includeExpired)
	if t, ok := asOf.Value(); ok {
		criteriaBuilder = criteriaBuilder.AsOf(t)
	}
	criteria := criteriaBuilder.Build()
	summaries, err := c.memory.Search(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to search durable memories", "durable memory の検索に失敗しました"), err)
	}
	if err := writeMemorySummariesByFormat(output, summaries, input.asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory search results", "durable memory 検索結果の出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) runMemoryShow(ctx context.Context, output io.Writer, dbPath string, memoryID string, asJSON bool) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if err := c.initializeStore(ctx, dbPath); err != nil {
		return err
	}
	resolvedMemoryID, err := domtypes.MemoryIDFrom(memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	details, err := c.memory.Show(ctx, resolvedMemoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to get memory details", "durable memory 詳細の取得に失敗しました"), err)
	}
	if err := writeMemoryDetailsByFormat(output, details, asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory details", "durable memory 詳細の出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) runMemoryRemember(ctx context.Context, output io.Writer, input memoryWriteCommandInput) error {
	if err := validateMemoryWriteInput(input); err != nil {
		return err
	}
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryType, scope, confidence, source, evidenceRefs, artifactRefs, err := c.resolveMemoryWriteParameters(ctx, input)
	if err != nil {
		return err
	}
	details, err := c.memory.Remember(ctx, memoryType, scope, input.fact, confidence, source, evidenceRefs, artifactRefs)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record durable memory", "durable memory の記録に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemoryPropose(ctx context.Context, output io.Writer, input memoryWriteCommandInput) error {
	if err := validateMemoryWriteInput(input); err != nil {
		return err
	}
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	input.confidence = ""
	memoryType, scope, _, source, evidenceRefs, artifactRefs, err := c.resolveMemoryWriteParameters(ctx, input)
	if err != nil {
		return err
	}
	details, err := c.memory.Propose(ctx, memoryType, scope, input.fact, source, evidenceRefs, artifactRefs)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to record candidate durable memory", "candidate durable memory の記録に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemoryDistill(ctx context.Context, output io.Writer, input memoryDistillCommandInput) error {
	if strings.TrimSpace(input.memoryType) == "" {
		return xerrors.Errorf(Localize("memory type must not be empty", "memory type は空にできません"))
	}
	if strings.TrimSpace(input.fact) == "" {
		return xerrors.Errorf(Localize("fact must not be empty", "fact は空にできません"))
	}
	rawIDs := normaliseInboxIDs(input.fromIDs)
	if len(rawIDs) == 0 {
		return xerrors.Errorf(Localize("--from must list at least one source candidate memory id", "--from に少なくとも1つの source candidate memory id を指定してください"))
	}
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}

	fromIDs := make([]domtypes.MemoryID, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		memoryID, err := domtypes.MemoryIDFrom(rawID)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to resolve source memory ID", "source memory ID の解決に失敗しました"), err)
		}
		fromIDs = append(fromIDs, memoryID)
	}
	memoryType, err := parseRequiredMemoryType(input.memoryType)
	if err != nil {
		return err
	}
	scope, err := resolveRequiredExplicitMemoryScope(input.workspace, input.agent, input.sessionFamily)
	if err != nil {
		return err
	}
	confidence, err := parseOptionalConfidence(input.confidence)
	if err != nil {
		return err
	}
	source, err := parseMemorySource(input.source)
	if err != nil {
		return err
	}
	replace, err := parseMemoryDistillReplace(input.replace)
	if err != nil {
		return err
	}
	result, err := c.memory.Distill(ctx, apptypes.MemoryDistillCriteriaOf(fromIDs, memoryType, scope, input.fact, confidence, source, replace))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to distill durable memory", "durable memory の distill に失敗しました"), err)
	}
	return writeMemoryDistillResult(output, result, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemoryAccept(ctx context.Context, output io.Writer, input memoryMutationCommandInput) error {
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryID, err := domtypes.MemoryIDFrom(input.memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	confidence, err := parseOptionalConfidence(input.confidence)
	if err != nil {
		return err
	}
	details, err := c.memory.Accept(ctx, memoryID, confidence)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to accept durable memory", "durable memory の accept に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemoryReject(ctx context.Context, output io.Writer, input memoryMutationCommandInput) error {
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryID, err := domtypes.MemoryIDFrom(input.memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	details, err := c.memory.Reject(ctx, memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to reject durable memory", "durable memory の reject に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemorySupersede(ctx context.Context, output io.Writer, input memorySupersedeCommandInput) error {
	if strings.TrimSpace(input.fact) == "" {
		return xerrors.Errorf(Localize("fact must not be empty", "fact は空にできません"))
	}
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryID, err := domtypes.MemoryIDFrom(input.memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	memoryType, err := parseOptionalMemoryType(input.memoryType)
	if err != nil {
		return err
	}
	scope, err := resolveOptionalMemoryScope(input.workspace, input.agent, input.sessionFamily)
	if err != nil {
		return err
	}
	confidence, err := parseOptionalConfidence(input.confidence)
	if err != nil {
		return err
	}
	source, err := parseMemorySource(input.source)
	if err != nil {
		return err
	}
	evidenceRefs, err := parseEvidenceRefs(input.evidenceRefs)
	if err != nil {
		return err
	}
	artifactRefs, err := parseArtifactRefs(input.artifactRefs)
	if err != nil {
		return err
	}
	validFrom, err := parseOptionalValidityTime(input.validFrom)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --from", "--from の解釈に失敗しました"), err)
	}
	validTo, err := parseOptionalValidityTime(input.validTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --to", "--to の解釈に失敗しました"), err)
	}
	details, err := c.memory.Supersede(ctx, memoryID, memoryType, scope, input.fact, confidence, source, evidenceRefs, artifactRefs, validFrom, validTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to supersede durable memory", "durable memory の置換に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemoryExpire(ctx context.Context, output io.Writer, input memoryMutationCommandInput) error {
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryID, err := domtypes.MemoryIDFrom(input.memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	expiresAt, err := parseOptionalExpiryTime(input.expiresAt)
	if err != nil {
		return err
	}
	details, err := c.memory.Expire(ctx, memoryID, expiresAt)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to expire durable memory", "durable memory の expire に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

func (c *RootCLI) runMemorySetValidity(ctx context.Context, output io.Writer, input memoryValidityCommandInput) error {
	if err := c.initializeMemoryStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryID, err := domtypes.MemoryIDFrom(input.memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	validFrom, err := parseOptionalValidityTime(input.validFrom)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --from", "--from の解析に失敗しました"), err)
	}
	validTo, err := parseOptionalValidityTime(input.validTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --to", "--to の解析に失敗しました"), err)
	}
	details, err := c.memory.SetValidity(ctx, memoryID, validFrom, validTo, input.clearTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to set durable memory validity", "durable memory の validity 設定に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

// parseOptionalValidityTime parses a --from / --to flag into an
// Optional time. Unlike parseOptionalExpiryTime it does not default
// to now — the convention for validity flags is "unset means leave
// the current bound unchanged."
func parseOptionalValidityTime(value string) (domtypes.Optional[time.Time], error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return domtypes.None[time.Time](), nil
	}
	parsed, err := parseFlexibleTime(trimmed, false)
	if err != nil {
		return domtypes.None[time.Time](), err
	}
	return domtypes.Some(parsed), nil
}

func (c *RootCLI) initializeMemoryStore(ctx context.Context, dbPath string) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	return c.initializeStore(ctx, dbPath)
}

func (c *RootCLI) initializeStore(ctx context.Context, dbPath string) error {
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) resolveMemoryWriteParameters(ctx context.Context, input memoryWriteCommandInput) (
	domtypes.MemoryType,
	domtypes.MemoryScope,
	domtypes.Optional[domtypes.Confidence],
	domtypes.MemorySource,
	[]domtypes.EvidenceRef,
	[]domtypes.ArtifactRef,
	error,
) {
	memoryType, err := parseRequiredMemoryType(input.memoryType)
	if err != nil {
		return domtypes.MemoryType(""), nil, domtypes.None[domtypes.Confidence](), domtypes.MemorySource(""), nil, nil, err
	}
	scope, err := resolveMemoryWriteScope(ctx, input.workspace, input.agent, input.sessionFamily)
	if err != nil {
		return domtypes.MemoryType(""), nil, domtypes.None[domtypes.Confidence](), domtypes.MemorySource(""), nil, nil, err
	}
	confidence, err := parseOptionalConfidence(input.confidence)
	if err != nil {
		return domtypes.MemoryType(""), nil, domtypes.None[domtypes.Confidence](), domtypes.MemorySource(""), nil, nil, err
	}
	source, err := parseMemorySource(input.source)
	if err != nil {
		return domtypes.MemoryType(""), nil, domtypes.None[domtypes.Confidence](), domtypes.MemorySource(""), nil, nil, err
	}
	evidenceRefs, err := parseEvidenceRefs(input.evidenceRefs)
	if err != nil {
		return domtypes.MemoryType(""), nil, domtypes.None[domtypes.Confidence](), domtypes.MemorySource(""), nil, nil, err
	}
	artifactRefs, err := parseArtifactRefs(input.artifactRefs)
	if err != nil {
		return domtypes.MemoryType(""), nil, domtypes.None[domtypes.Confidence](), domtypes.MemorySource(""), nil, nil, err
	}
	return memoryType, scope, confidence, source, evidenceRefs, artifactRefs, nil
}

func validateMemoryWriteInput(input memoryWriteCommandInput) error {
	if strings.TrimSpace(input.memoryType) == "" {
		return xerrors.Errorf(Localize("memory type must not be empty", "memory type は空にできません"))
	}
	if strings.TrimSpace(input.fact) == "" {
		return xerrors.Errorf(Localize("fact must not be empty", "fact は空にできません"))
	}
	return nil
}

func parseRequiredMemoryType(value string) (domtypes.MemoryType, error) {
	resolved, err := domtypes.MemoryTypeFrom(value)
	if err != nil {
		return domtypes.MemoryType(""), xerrors.Errorf("%s: %w", Localize("failed to resolve memory type", "memory type の解決に失敗しました"), err)
	}
	return resolved, nil
}

func parseOptionalMemoryType(value string) (domtypes.MemoryType, error) {
	if strings.TrimSpace(value) == "" {
		return domtypes.MemoryType(""), nil
	}
	return parseRequiredMemoryType(value)
}

func parseOptionalConfidence(value string) (domtypes.Optional[domtypes.Confidence], error) {
	if strings.TrimSpace(value) == "" {
		return domtypes.None[domtypes.Confidence](), nil
	}
	confidence, err := domtypes.ConfidenceFrom(value)
	if err != nil {
		return domtypes.None[domtypes.Confidence](), xerrors.Errorf("%s: %w", Localize("failed to resolve confidence", "confidence の解決に失敗しました"), err)
	}
	return domtypes.Some(confidence), nil
}

func parseMemorySource(value string) (domtypes.MemorySource, error) {
	if strings.TrimSpace(value) == "" {
		return domtypes.MemorySource(""), nil
	}
	source, err := domtypes.MemorySourceFrom(value)
	if err != nil {
		return domtypes.MemorySource(""), xerrors.Errorf("%s: %w", Localize("failed to resolve memory source", "memory source の解決に失敗しました"), err)
	}
	return source, nil
}

func parseMemoryDistillReplace(value string) (apptypes.MemoryDistillReplace, error) {
	switch apptypes.MemoryDistillReplace(strings.TrimSpace(value)) {
	case "", apptypes.MemoryDistillReplaceKeep:
		return apptypes.MemoryDistillReplaceKeep, nil
	case apptypes.MemoryDistillReplaceReject:
		return apptypes.MemoryDistillReplaceReject, nil
	case apptypes.MemoryDistillReplaceSupersede:
		return apptypes.MemoryDistillReplaceSupersede, nil
	default:
		return "", xerrors.Errorf(Localize("replace must be one of keep, reject, supersede", "replace は keep, reject, supersede のいずれかである必要があります"))
	}
}

func parseMemoryStatuses(values []string) ([]domtypes.MemoryStatus, error) {
	statuses := make([]domtypes.MemoryStatus, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		status, err := domtypes.MemoryStatusFrom(value)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve memory status", "memory status の解決に失敗しました"), err)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func parseMemoryTypes(values []string) ([]domtypes.MemoryType, error) {
	memoryTypes := make([]domtypes.MemoryType, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		memoryType, err := domtypes.MemoryTypeFrom(value)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve memory type", "memory type の解決に失敗しました"), err)
		}
		memoryTypes = append(memoryTypes, memoryType)
	}
	return memoryTypes, nil
}

func parseEvidenceRefs(values []string) ([]domtypes.EvidenceRef, error) {
	refs := make([]domtypes.EvidenceRef, 0, len(values))
	for _, value := range values {
		kind, rawValue, err := parseKindValueToken(value)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to parse evidence ref", "evidence ref の解析に失敗しました"), err)
		}
		resolvedKind, err := domtypes.EvidenceRefKindFrom(kind)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve evidence ref kind", "evidence ref kind の解決に失敗しました"), err)
		}
		ref, err := domtypes.EvidenceRefFrom(resolvedKind, rawValue)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve evidence ref", "evidence ref の解決に失敗しました"), err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func parseArtifactRefs(values []string) ([]domtypes.ArtifactRef, error) {
	refs := make([]domtypes.ArtifactRef, 0, len(values))
	for _, value := range values {
		kind, rawValue, err := parseKindValueToken(value)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to parse artifact ref", "artifact ref の解析に失敗しました"), err)
		}
		resolvedKind, err := domtypes.ArtifactRefKindFrom(kind)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve artifact ref kind", "artifact ref kind の解決に失敗しました"), err)
		}
		ref, err := domtypes.ArtifactRefFrom(resolvedKind, rawValue)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve artifact ref", "artifact ref の解決に失敗しました"), err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func parseKindValueToken(value string) (string, string, error) {
	trimmed := strings.TrimSpace(value)
	separator := strings.Index(trimmed, ":")
	if separator <= 0 || separator == len(trimmed)-1 {
		return "", "", xerrors.Errorf(Localize("references must use kind:value format", "参照は kind:value 形式で指定する必要があります"))
	}
	return trimmed[:separator], trimmed[separator+1:], nil
}

func resolveRequiredExplicitMemoryScope(workspace string, agent string, sessionFamily string) (domtypes.MemoryScope, error) {
	scope, err := resolveOptionalMemoryScope(workspace, agent, sessionFamily)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, xerrors.Errorf(Localize("one of --workspace, --agent, or --session-family is required", "--workspace, --agent, --session-family のいずれかが必要です"))
	}
	return scope, nil
}

func resolveMemoryWriteScope(ctx context.Context, workspace string, agent string, sessionFamily string) (domtypes.MemoryScope, error) {
	if strings.TrimSpace(agent) != "" {
		resolvedAgent, err := domtypes.AgentFrom(agent)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve agent scope", "agent scope の解決に失敗しました"), err)
		}
		return domtypes.AgentScopeOf(resolvedAgent), nil
	}
	if strings.TrimSpace(sessionFamily) != "" {
		resolvedSessionID, err := domtypes.SessionIDFrom(sessionFamily)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve session-family scope", "session-family scope の解決に失敗しました"), err)
		}
		return domtypes.SessionFamilyScopeOf(resolvedSessionID), nil
	}
	resolvedWorkspace := resolveWorkspaceValue(ctx, workspace)
	if strings.TrimSpace(resolvedWorkspace) == "" {
		return nil, xerrors.Errorf(Localize("workspace scope could not be resolved", "workspace scope を解決できませんでした"))
	}
	workspaceValue, err := domtypes.WorkspaceFrom(resolvedWorkspace)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve workspace scope", "workspace scope の解決に失敗しました"), err)
	}
	return domtypes.WorkspaceScopeOf(workspaceValue), nil
}

func resolveOptionalMemoryScope(workspace string, agent string, sessionFamily string) (domtypes.MemoryScope, error) {
	if strings.TrimSpace(agent) != "" {
		resolvedAgent, err := domtypes.AgentFrom(agent)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve agent scope", "agent scope の解決に失敗しました"), err)
		}
		return domtypes.AgentScopeOf(resolvedAgent), nil
	}
	if strings.TrimSpace(sessionFamily) != "" {
		resolvedSessionID, err := domtypes.SessionIDFrom(sessionFamily)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve session-family scope", "session-family scope の解決に失敗しました"), err)
		}
		return domtypes.SessionFamilyScopeOf(resolvedSessionID), nil
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, nil
	}
	workspaceValue, err := domtypes.WorkspaceFrom(workspace)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve workspace scope", "workspace scope の解決に失敗しました"), err)
	}
	return domtypes.WorkspaceScopeOf(workspaceValue), nil
}

func resolveMemoryFilterScopes(ctx context.Context, workspace string, agent string, sessionFamily string, defaultWorkspace bool) ([]domtypes.MemoryScope, error) {
	scopes := make([]domtypes.MemoryScope, 0, 3)
	if strings.TrimSpace(workspace) != "" {
		workspaceValue, err := domtypes.WorkspaceFrom(workspace)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve workspace scope", "workspace scope の解決に失敗しました"), err)
		}
		scopes = append(scopes, domtypes.WorkspaceScopeOf(workspaceValue))
	}
	if strings.TrimSpace(agent) != "" {
		resolvedAgent, err := domtypes.AgentFrom(agent)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve agent scope", "agent scope の解決に失敗しました"), err)
		}
		scopes = append(scopes, domtypes.AgentScopeOf(resolvedAgent))
	}
	if strings.TrimSpace(sessionFamily) != "" {
		resolvedSessionID, err := domtypes.SessionIDFrom(sessionFamily)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve session-family scope", "session-family scope の解決に失敗しました"), err)
		}
		scopes = append(scopes, domtypes.SessionFamilyScopeOf(resolvedSessionID))
	}
	if len(scopes) > 0 || !defaultWorkspace {
		return scopes, nil
	}
	resolvedWorkspace := resolveWorkspaceValue(ctx, workspace)
	if strings.TrimSpace(resolvedWorkspace) == "" {
		return nil, nil
	}
	workspaceValue, err := domtypes.WorkspaceFrom(resolvedWorkspace)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve workspace scope", "workspace scope の解決に失敗しました"), err)
	}
	return []domtypes.MemoryScope{domtypes.WorkspaceScopeOf(workspaceValue)}, nil
}

func parseOptionalExpiryTime(value string) (domtypes.Optional[time.Time], error) {
	if strings.TrimSpace(value) == "" {
		return domtypes.None[time.Time](), nil
	}
	resolved, err := parseFlexibleTime(value, false)
	if err != nil {
		return domtypes.None[time.Time](), xerrors.Errorf("%s: %w", Localize("failed to resolve expiry time", "expiry time の解決に失敗しました"), err)
	}
	return domtypes.Some(resolved), nil
}

func hasMemorySearchInputConstraint(input memorySearchCommandInput) bool {
	return strings.TrimSpace(input.query) != "" ||
		strings.TrimSpace(input.workspace) != "" ||
		strings.TrimSpace(input.agent) != "" ||
		strings.TrimSpace(input.sessionFamily) != "" ||
		strings.TrimSpace(input.preset) != "" ||
		len(input.statuses) > 0 ||
		len(input.memoryTypes) > 0
}
