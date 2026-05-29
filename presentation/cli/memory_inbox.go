package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// defaultMemoryInboxLimit is the page size applied when the operator does
// not pass --limit. The value matches the existing `memory list` default
// so operators can move between the two surfaces without re-tuning.
const defaultMemoryInboxLimit = 20

func (c *RootCLI) newMemoryInboxCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: Localize("Review memory candidates with provenance", "メモリ候補を provenance 付きで確認する"),
	}
	cmd.AddCommand(c.newMemoryInboxListCommand())
	cmd.AddCommand(c.newMemoryInboxShowCommand())
	cmd.AddCommand(c.newMemoryInboxAcceptCommand())
	cmd.AddCommand(c.newMemoryInboxRejectCommand())
	cmd.AddCommand(c.newMemoryInboxAttachCommand())
	cmd.AddCommand(c.newMemoryInboxCleanupCommand())
	cmd.AddCommand(c.newMemoryInboxReviewCommand())
	return cmd
}

func (c *RootCLI) newMemoryInboxListCommand() *cobra.Command {
	input := memoryInboxListCommandInput{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List memory candidates in the review queue", "メモリ候補の確認キューを一覧する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryInboxList(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("filter by workspace scope (defaults to env/detected workspace)", "workspace scope で絞り込む (未指定時は env/検出 workspace)"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("filter by agent scope", "agent scope で絞り込む"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("filter by session-family scope", "session-family scope で絞り込む"))
	cmd.Flags().StringSliceVar(&input.memoryTypes, "type", nil, Localize("filter by memory type", "memory type で絞り込む"))
	cmd.Flags().StringSliceVar(&input.sources, "source", nil, Localize("filter by memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported)", "memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported) で絞り込む"))
	cmd.Flags().BoolVar(&input.rememberIntent, "remember-intent", false, Localize("shortcut for --source remember-intent", "--source remember-intent のショートカット"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden memory candidates (low-quality auto-extractions kept for audit)", "extracted-hidden のメモリ候補も含める (audit 用に保存された低品質自動抽出)"))
	cmd.Flags().DurationVar(&input.olderThan, "older-than", 0, Localize("only include memory candidates not updated within this duration (for example 168h)", "この duration 以内に更新されていないメモリ候補だけを含める (例: 168h)"))
	cmd.Flags().DurationVar(&input.newerThan, "newer-than", 0, Localize("only include memory candidates updated within this duration (for example 24h)", "この duration 以内に更新されたメモリ候補だけを含める (例: 24h)"))
	cmd.Flags().StringVar(&input.quality, "quality", "any", Localize("filter by quality category (any / low / normal)", "品質カテゴリで絞り込む (any / low / normal)"))
	cmd.Flags().IntVar(&input.limit, "limit", defaultMemoryInboxLimit, Localize("maximum number of candidates to return", "表示件数"))
	cmd.Flags().IntVar(&input.offset, "offset", 0, Localize("number of candidates to skip before listing", "一覧表示前にスキップする件数"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryInboxShowCommand() *cobra.Command {
	input := memoryInboxShowCommandInput{}
	cmd := &cobra.Command{
		Use:   "show <memory-id>",
		Short: Localize("Show an evidence-first decision card for a memory candidate", "メモリ候補の evidence 優先 decision card を表示する"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemoryInboxShow(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryInboxAcceptCommand() *cobra.Command {
	input := memoryInboxBatchCommandInput{}
	cmd := &cobra.Command{
		Use:   "accept [memory-id]",
		Short: Localize("Accept one or more memory candidates", "メモリ候補を accept する (単一/複数)"),
		Args:  maximumNArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.ids = mergePositionalInboxID(args, input.ids)
			return c.runMemoryInboxBatch(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), input, memoryInboxActionAccept)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.ids, "ids", nil, Localize(
		"comma-separated list of memory ids to accept (repeatable; alternative to the positional id for batch scripts)",
		"accept 対象の memory id をカンマ区切りで指定 (複数指定可。バッチ用途では positional id の代わりに使用)",
	))
	cmd.Flags().StringVar(&input.confidence, "confidence", "", Localize("accepted confidence (defaults to verified)", "accepted 時の confidence (既定値は verified)"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize(
		"print only the resulting memory ids (one per successful row); failures go to stderr",
		"処理に成功した memory id だけを 1 行ずつ出力する (失敗は stderr に書き出す)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

func (c *RootCLI) newMemoryInboxCleanupCommand() *cobra.Command {
	input := memoryInboxCleanupCommandInput{
		quality: "low",
		limit:   defaultMemoryInboxLimit,
	}
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: Localize("Preview or reject stale/low-quality memory candidates in bulk", "古い/低品質のメモリ候補を一括プレビューまたは reject する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryInboxCleanup(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("filter by workspace scope (defaults to env/detected workspace)", "workspace scope で絞り込む (未指定時は env/検出 workspace)"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("filter by agent scope", "agent scope で絞り込む"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("filter by session-family scope", "session-family scope で絞り込む"))
	cmd.Flags().StringSliceVar(&input.memoryTypes, "type", nil, Localize("filter by memory type", "memory type で絞り込む"))
	cmd.Flags().StringSliceVar(&input.sources, "source", nil, Localize("filter by memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported)", "memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported) で絞り込む"))
	cmd.Flags().BoolVar(&input.rememberIntent, "remember-intent", false, Localize("shortcut for --source remember-intent", "--source remember-intent のショートカット"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden memory candidates", "extracted-hidden のメモリ候補も含める"))
	cmd.Flags().DurationVar(&input.olderThan, "older-than", 0, Localize("only target memory candidates not updated within this duration (for example 168h)", "この duration 以内に更新されていないメモリ候補だけを対象にする (例: 168h)"))
	cmd.Flags().DurationVar(&input.newerThan, "newer-than", 0, Localize("only target memory candidates updated within this duration (for example 24h)", "この duration 以内に更新されたメモリ候補だけを対象にする (例: 24h)"))
	cmd.Flags().StringVar(&input.quality, "quality", "low", Localize("target quality category (low / any / normal); default low", "対象の品質カテゴリ (low / any / normal); 既定は low"))
	cmd.Flags().IntVar(&input.limit, "limit", defaultMemoryInboxLimit, Localize("maximum number of memory candidates to target", "対象メモリ候補数"))
	cmd.Flags().BoolVar(&input.apply, "apply", false, Localize("reject the matched memory candidates; omit for dry-run preview", "一致したメモリ候補を reject する。省略時は dry-run preview"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryInboxRejectCommand() *cobra.Command {
	input := memoryInboxBatchCommandInput{}
	cmd := &cobra.Command{
		Use:   "reject [memory-id]",
		Short: Localize("Reject one or more memory candidates", "メモリ候補を reject する (単一/複数)"),
		Args:  maximumNArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.ids = mergePositionalInboxID(args, input.ids)
			return c.runMemoryInboxBatch(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), input, memoryInboxActionReject)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.ids, "ids", nil, Localize(
		"comma-separated list of memory ids to reject (repeatable; alternative to the positional id for batch scripts)",
		"reject 対象の memory id をカンマ区切りで指定 (複数指定可。バッチ用途では positional id の代わりに使用)",
	))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize(
		"print only the resulting memory ids (one per successful row); failures go to stderr",
		"処理に成功した memory id だけを 1 行ずつ出力する (失敗は stderr に書き出す)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

func (c *RootCLI) newMemoryInboxAttachCommand() *cobra.Command {
	input := memoryInboxAttachCommandInput{}
	cmd := &cobra.Command{
		Use:   "attach <memory-id>",
		Short: Localize("Attach evidence refs to a memory candidate", "メモリ候補に evidence refs を追加する"),
		Long: Localize(
			"Attach evidence refs, plus optional artifact refs, to an existing memory candidate without changing its review status. Use this when Memory review finds a useful candidate that cannot be accepted or distilled yet because accepted memories require evidence.",
			"既存のメモリ候補に evidence refs と任意の artifact refs を追加します。review status は変更しません。Memory review で有用な候補を見つけたものの、accepted memory に evidence が必要なため accept / distill できない場合に使います。",
		),
		Example: "  traceary memory inbox attach memory-raw --evidence event:evt-123 --evidence file:/tmp/notes.md#L10-L20",
		Args:    exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input.memoryID = args[0]
			return c.runMemoryInboxAttach(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringArrayVar(&input.evidenceRefs, "evidence", nil, Localize("evidence ref as kind:value (repeatable)", "kind:value 形式の evidence ref (複数指定可)"))
	cmd.Flags().StringArrayVar(&input.artifactRefs, "artifact", nil, Localize("artifact ref as kind:value (repeatable)", "kind:value 形式の artifact ref (複数指定可)"))
	cmd.Flags().BoolVar(&input.idOnly, "id-only", false, Localize("print only the memory ID", "memory ID だけを出力する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

// mergePositionalInboxID prepends the optional positional id onto the
// --ids slice so the canonical single-id form (`memory inbox accept <id>`)
// shares the same dedupe / parse path as `--ids`. The caller's --ids value
// is preserved verbatim (the runner re-normalises with normaliseInboxIDs),
// which keeps batch scripts working unchanged.
func mergePositionalInboxID(args []string, ids []string) []string {
	if len(args) == 0 {
		return ids
	}
	merged := make([]string, 0, len(args)+len(ids))
	merged = append(merged, args...)
	merged = append(merged, ids...)
	return merged
}

type memoryInboxAction string

const (
	memoryInboxActionAccept memoryInboxAction = "accept"
	memoryInboxActionReject memoryInboxAction = "reject"
)

func (c *RootCLI) runMemoryInboxList(ctx context.Context, output io.Writer, input memoryInboxListCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset < 0 {
		return xerrors.New(Localize("offset must be greater than or equal to 0", "offset は 0 以上である必要があります"))
	}
	if input.olderThan < 0 || input.newerThan < 0 {
		return xerrors.New(Localize("--older-than and --newer-than must be greater than or equal to 0", "--older-than と --newer-than は 0 以上である必要があります"))
	}
	quality, err := parseMemoryInboxQuality(input.quality)
	if err != nil {
		return err
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	scopes, err := resolveMemoryFilterScopes(ctx, input.workspace, input.agent, input.sessionFamily, true)
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
	sources = applyRememberIntentSourceShortcut(sources, input.rememberIntent)
	lowQualityIDs, err := c.loadLowQualityCandidateIDs(ctx, scopes, input.includeHidden || memorySourcesContain(sources, domtypes.MemorySourceExtractedHidden), quality)
	if err != nil {
		return err
	}

	// Default inbox view excludes the extracted-hidden source so the
	// reviewer is not drowned by low-quality auto-extractions. The
	// rows are still in the store for audit; `--include-hidden`
	// surfaces them. Explicit `--source` always wins (#810/#830).
	sources = applyExtractedHiddenDefault(sources, input.includeHidden)

	// Inbox is always scoped to candidate — that is the point of the view.
	// Source filters go into the criteria so pagination is consistent: if
	// the operator asks for `--source imported --limit 20` and only the
	// 21st imported candidate matches, the datasource returns the match
	// instead of handing back an empty page because the first 20 rows
	// were some other source.
	//
	// RememberIntentPriority is enforced at the query layer so prioritized
	// inbox sources (remember-intent, then compact-summary) surface ahead of
	// other candidates BEFORE limit/offset applies — a post-fetch in-memory
	// sort would only re-order the current page and could let a prioritized
	// row that lives just past the page boundary stay hidden until later pages
	// (#856/#857).
	criteriaBuilder := apptypes.NewMemoryListCriteriaBuilder(input.limit).
		Offset(input.offset).
		Scopes(scopes).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}).
		MemoryTypes(memoryTypes).
		Sources(sources).
		RememberIntentPriority(true)
	criteriaBuilder = applyMemoryInboxAgeFilters(criteriaBuilder, input.olderThan, input.newerThan, time.Now())
	criteria := criteriaBuilder.Build()
	summaries, err := c.memory.List(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list memory review queue candidates", "メモリ候補の確認キューの一覧取得に失敗しました"), err)
	}

	items := make([]apptypes.MemoryDetails, 0, len(summaries))
	for _, summary := range summaries {
		details, err := c.memory.Show(ctx, summary.MemoryID())
		if err != nil {
			return xerrors.Errorf("failed to load memory %s: %w", summary.MemoryID().String(), err)
		}
		if memoryInboxDetailsMatchesQuality(details, quality, lowQualityIDs) {
			items = append(items, details)
		}
	}
	return writeMemoryInboxList(output, items, input.asJSON)
}

func (c *RootCLI) runMemoryInboxShow(ctx context.Context, output io.Writer, input memoryInboxShowCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if strings.TrimSpace(input.memoryID) == "" {
		return xerrors.New(Localize("memory id must not be empty", "memory id は空にできません"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}
	details, err := c.memory.Show(ctx, domtypes.MemoryID(strings.TrimSpace(input.memoryID)))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to show memory candidate", "メモリ候補の取得に失敗しました"), err)
	}
	if details.Summary().Status() != domtypes.MemoryStatusCandidate {
		return xerrors.New(Localize("memory inbox show only accepts memory candidates; use `traceary memory show` for other statuses", "memory inbox show は memory candidate のみ対象です。他の status は `traceary memory show` を使ってください"))
	}
	if input.asJSON {
		return writeJSON(output, newMemoryDetailsOutput(details))
	}
	return writeMemoryReviewDecisionCard(output, details)
}

func (c *RootCLI) runMemoryInboxCleanup(ctx context.Context, output io.Writer, input memoryInboxCleanupCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.olderThan < 0 || input.newerThan < 0 {
		return xerrors.New(Localize("--older-than and --newer-than must be greater than or equal to 0", "--older-than と --newer-than は 0 以上である必要があります"))
	}
	quality, err := parseMemoryInboxQuality(input.quality)
	if err != nil {
		return err
	}
	if quality == memoryInboxQualityAny && input.olderThan == 0 {
		return xerrors.New(Localize("cleanup with --quality any requires --older-than to avoid rejecting the whole memory review queue", "--quality any の cleanup ではメモリ候補の確認キュー全体の reject を避けるため --older-than が必要です"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	items, err := c.loadMemoryInboxCleanupCandidates(ctx, input, quality, time.Now())
	if err != nil {
		return err
	}
	result := memoryInboxCleanupResult{
		Action:  "cleanup-reject",
		DryRun:  !input.apply,
		Summary: summarizeMemoryInboxCleanup(items),
		Matched: items,
	}
	if !input.apply {
		return writeMemoryInboxCleanupResult(output, result, input.asJSON)
	}

	for _, item := range items {
		summary := item.Summary()
		if summary.Status() != domtypes.MemoryStatusCandidate {
			result.Failures = append(result.Failures, memoryInboxFailure{
				ID:        summary.MemoryID().String(),
				Error:     memoryInboxCleanupNonCandidateError,
				ErrorCode: memoryInboxCleanupNonCandidateCode,
			})
			continue
		}
		details, err := c.memory.Reject(ctx, summary.MemoryID())
		if err != nil {
			result.Failures = append(result.Failures, memoryInboxFailure{
				ID:    summary.MemoryID().String(),
				Error: err.Error(),
			})
			continue
		}
		result.Processed = append(result.Processed, details)
	}
	return writeMemoryInboxCleanupResult(output, result, input.asJSON)
}

func (c *RootCLI) loadMemoryInboxCleanupCandidates(ctx context.Context, input memoryInboxCleanupCommandInput, quality memoryInboxQuality, now time.Time) ([]apptypes.MemoryDetails, error) {
	scopes, err := resolveMemoryFilterScopes(ctx, input.workspace, input.agent, input.sessionFamily, true)
	if err != nil {
		return nil, err
	}
	memoryTypes, err := parseMemoryTypes(input.memoryTypes)
	if err != nil {
		return nil, err
	}
	sources, err := parseMemorySources(input.sources)
	if err != nil {
		return nil, err
	}
	sources = applyRememberIntentSourceShortcut(sources, input.rememberIntent)
	lowQualityIDs, err := c.loadLowQualityCandidateIDs(ctx, scopes, input.includeHidden || memorySourcesContain(sources, domtypes.MemorySourceExtractedHidden), quality)
	if err != nil {
		return nil, err
	}
	sources = applyExtractedHiddenDefault(sources, input.includeHidden)
	criteriaBuilder := apptypes.NewMemoryListCriteriaBuilder(input.limit).
		Scopes(scopes).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}).
		MemoryTypes(memoryTypes).
		Sources(sources).
		RememberIntentPriority(true)
	criteriaBuilder = applyMemoryInboxAgeFilters(criteriaBuilder, input.olderThan, input.newerThan, now)
	summaries, err := c.memory.List(ctx, criteriaBuilder.Build())
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list cleanup memory candidates", "cleanup 対象メモリ候補の一覧取得に失敗しました"), err)
	}
	items := make([]apptypes.MemoryDetails, 0, len(summaries))
	for _, summary := range summaries {
		details, err := c.memory.Show(ctx, summary.MemoryID())
		if err != nil {
			return nil, xerrors.Errorf("failed to load memory %s: %w", summary.MemoryID().String(), err)
		}
		if memoryInboxDetailsMatchesQuality(details, quality, lowQualityIDs) {
			items = append(items, details)
		}
	}
	return items, nil
}

type memoryInboxQuality string

const (
	memoryInboxQualityAny    memoryInboxQuality = "any"
	memoryInboxQualityLow    memoryInboxQuality = "low"
	memoryInboxQualityNormal memoryInboxQuality = "normal"
)

func parseMemoryInboxQuality(value string) (memoryInboxQuality, error) {
	switch memoryInboxQuality(strings.ToLower(strings.TrimSpace(value))) {
	case "", memoryInboxQualityAny:
		return memoryInboxQualityAny, nil
	case memoryInboxQualityLow:
		return memoryInboxQualityLow, nil
	case memoryInboxQualityNormal:
		return memoryInboxQualityNormal, nil
	default:
		return "", xerrors.New(Localizef(
			"unknown memory candidate quality %q (allowed values: any, low, normal)",
			"メモリ候補の品質カテゴリ %q は不明です (使用可能な値: any, low, normal)",
			value,
		))
	}
}

func (c *RootCLI) loadLowQualityCandidateIDs(ctx context.Context, scopes []domtypes.MemoryScope, includeHidden bool, quality memoryInboxQuality) (map[string]struct{}, error) {
	if quality == memoryInboxQualityAny {
		return nil, nil
	}
	result, err := c.memory.Scan(ctx, apptypes.MemoryHygieneScanCriteria{
		Scopes:                  scopes,
		IncludeHiddenCandidates: includeHidden,
	})
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to scan low-quality memory candidates", "低品質メモリ候補のスキャンに失敗しました"), err)
	}
	ids := make(map[string]struct{}, result.LowQualityCandidateCount)
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind != apptypes.MemoryHygieneSuggestionLowQualityCandidate {
			continue
		}
		ids[suggestion.MemoryID.String()] = struct{}{}
	}
	return ids, nil
}

func memoryInboxDetailsMatchesQuality(details apptypes.MemoryDetails, quality memoryInboxQuality, lowQualityIDs map[string]struct{}) bool {
	switch quality {
	case memoryInboxQualityAny:
		return true
	case memoryInboxQualityLow:
		_, ok := lowQualityIDs[details.Summary().MemoryID().String()]
		return ok
	case memoryInboxQualityNormal:
		_, ok := lowQualityIDs[details.Summary().MemoryID().String()]
		return !ok
	default:
		return true
	}
}

func applyMemoryInboxAgeFilters(builder *apptypes.MemoryListCriteriaBuilder, olderThan time.Duration, newerThan time.Duration, now time.Time) *apptypes.MemoryListCriteriaBuilder {
	if builder == nil {
		return builder
	}
	if olderThan > 0 {
		builder = builder.UpdatedBefore(now.Add(-olderThan))
	}
	if newerThan > 0 {
		builder = builder.UpdatedAfter(now.Add(-newerThan))
	}
	return builder
}

func memorySourcesContain(sources []domtypes.MemorySource, target domtypes.MemorySource) bool {
	for _, source := range sources {
		if source == target {
			return true
		}
	}
	return false
}

func applyRememberIntentSourceShortcut(sources []domtypes.MemorySource, rememberIntent bool) []domtypes.MemorySource {
	if !rememberIntent {
		return sources
	}
	return []domtypes.MemorySource{domtypes.MemorySourceRememberIntent}
}

func (c *RootCLI) runMemoryInboxBatch(ctx context.Context, output io.Writer, errOutput io.Writer, input memoryInboxBatchCommandInput, action memoryInboxAction) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	ids := normaliseInboxIDs(input.ids)
	if len(ids) == 0 {
		return xerrors.New(Localize("at least one memory id is required (positional id or --ids)", "memory id を1つ以上指定してください (positional id または --ids)"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	confidence, err := parseOptionalConfidence(input.confidence)
	if err != nil {
		return err
	}

	result := memoryInboxBatchResult{Action: string(action)}
	for _, rawID := range ids {
		memoryID, err := domtypes.MemoryIDFrom(rawID)
		if err != nil {
			result.Failures = append(result.Failures, memoryInboxFailure{ID: rawID, Error: err.Error()})
			continue
		}
		var details apptypes.MemoryDetails
		switch action {
		case memoryInboxActionAccept:
			details, err = c.memory.Accept(ctx, memoryID, confidence)
		case memoryInboxActionReject:
			details, err = c.memory.Reject(ctx, memoryID)
		default:
			return xerrors.New(Localizef(
				"unsupported memory review queue action: %s",
				"メモリ候補の確認キューのアクション %s は未対応です",
				action,
			))
		}
		if err != nil {
			result.Failures = append(result.Failures, memoryInboxFailure{ID: rawID, Error: err.Error()})
			continue
		}
		result.Processed = append(result.Processed, details)
	}
	if input.idOnly {
		return writeMemoryInboxBatchIDOnly(output, errOutput, result)
	}
	return writeMemoryInboxBatch(output, result, input.asJSON)
}

func (c *RootCLI) runMemoryInboxAttach(ctx context.Context, output io.Writer, input memoryInboxAttachCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if len(input.evidenceRefs) == 0 && len(input.artifactRefs) == 0 {
		return xerrors.New(Localize("at least one --evidence or --artifact ref is required", "--evidence または --artifact ref を1つ以上指定してください"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}
	memoryID, err := domtypes.MemoryIDFrom(input.memoryID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve memory ID", "memory ID の解決に失敗しました"), err)
	}
	evidenceRefs, err := parseEvidenceRefs(input.evidenceRefs)
	if err != nil {
		return err
	}
	artifactRefs, err := parseArtifactRefs(input.artifactRefs)
	if err != nil {
		return err
	}
	details, err := c.memory.AttachCandidateRefs(ctx, memoryID, evidenceRefs, artifactRefs)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to attach memory candidate refs", "メモリ候補 refs の追加に失敗しました"), err)
	}
	return writeMemoryMutationResult(output, details, input.idOnly, input.asJSON)
}

// memoryInboxBatchResult is the domain-neutral summary of a batch run,
// shared by the CLI output and the MCP tool so both surfaces expose the
// same success / failure breakdown.
type memoryInboxBatchResult struct {
	Action    string
	Processed []apptypes.MemoryDetails
	Failures  []memoryInboxFailure
}

type memoryInboxCleanupResult struct {
	Action    string
	DryRun    bool
	Summary   memoryInboxCleanupSummary
	Matched   []apptypes.MemoryDetails
	Processed []apptypes.MemoryDetails
	Failures  []memoryInboxFailure
}

// memoryInboxCleanupSummary is the aggregate composition of the matched memory
// candidates, surfaced before --apply so an operator triaging a large backlog
// sees the source / type breakdown at a glance instead of scanning every row.
type memoryInboxCleanupSummary struct {
	Total    int            `json:"total"`
	BySource map[string]int `json:"by_source,omitempty"`
	ByType   map[string]int `json:"by_type,omitempty"`
}

func summarizeMemoryInboxCleanup(matched []apptypes.MemoryDetails) memoryInboxCleanupSummary {
	summary := memoryInboxCleanupSummary{Total: len(matched)}
	if len(matched) == 0 {
		return summary
	}
	summary.BySource = make(map[string]int)
	summary.ByType = make(map[string]int)
	for _, details := range matched {
		s := details.Summary()
		summary.BySource[s.Source().String()]++
		summary.ByType[s.MemoryType().String()]++
	}
	return summary
}

// formatMemoryCountMap renders a count map as sorted "key=value" pairs so the
// text summary is deterministic.
func formatMemoryCountMap(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

type memoryInboxFailureCode string

// memoryInboxFailure is part of the public `memory inbox ... --json` contract.
// Keep the historical Go-style JSON field names stable; text-mode rendering
// may localize Error via localizedMemoryInboxFailureError, but JSON Error stays
// a machine-readable raw error string and ErrorCode is the stable discriminator.
type memoryInboxFailure struct {
	ID        string                 `json:"ID"`
	Error     string                 `json:"Error"`
	ErrorCode memoryInboxFailureCode `json:"ErrorCode,omitempty"`
}

const memoryInboxCleanupNonCandidateCode memoryInboxFailureCode = "cleanup_non_candidate"

const memoryInboxCleanupNonCandidateError = "cleanup only modifies memory candidates"

// normaliseInboxIDs de-duplicates and trims the --ids slice. StringSliceVar
// already splits on commas so repeated --ids flags accumulate; the helper
// keeps a stable order while dropping empty entries so `--ids ,abc,,def` is
// equivalent to `--ids abc,def`.
func normaliseInboxIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func parseMemorySources(values []string) ([]domtypes.MemorySource, error) {
	out := make([]domtypes.MemorySource, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		source, err := domtypes.MemorySourceFrom(value)
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to resolve memory source", "memory source の解決に失敗しました"), err)
		}
		out = append(out, source)
	}
	return out, nil
}

// applyExtractedHiddenDefault returns the source filter to use when
// the operator did not pass `--source` and `--include-hidden` is
// false. It pins the default to the visible-by-default sources so
// `extracted-hidden` rows are skipped. When the operator passed an
// explicit source filter, this function returns it unchanged so
// explicit always wins. Shared by `memory list`, `memory search`, and
// `memory inbox list` to avoid drift.
func applyExtractedHiddenDefault(sources []domtypes.MemorySource, includeHidden bool) []domtypes.MemorySource {
	if len(sources) > 0 || includeHidden {
		return sources
	}
	return []domtypes.MemorySource{
		domtypes.MemorySourceManual,
		domtypes.MemorySourceExtracted,
		domtypes.MemorySourceRememberIntent,
		domtypes.MemorySourceCompactSummary,
		domtypes.MemorySourceImported,
	}
}

func writeMemoryInboxList(output io.Writer, items []apptypes.MemoryDetails, asJSON bool) error {
	if asJSON {
		serialized := make([]memoryDetailsOutput, 0, len(items))
		for _, details := range items {
			serialized = append(serialized, newMemoryDetailsOutput(details))
		}
		return writeJSON(output, serialized)
	}
	if len(items) == 0 {
		if _, err := fmt.Fprintln(output, memoryReviewEmptyQueueMessage()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty memory review queue message", "メモリ候補の確認キューの空状態メッセージ出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintln(output, "MEMORY_ID\tTYPE\tSCOPE\tSOURCE\tCONFIDENCE\tEVIDENCE\tARTIFACT\tREVIEW\tFACT"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue header", "メモリ候補の確認キューヘッダーの出力に失敗しました"), err)
	}
	for _, details := range items {
		summary := details.Summary()
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			summary.MemoryID(),
			summary.MemoryType(),
			formatMemoryScope(summary.Scope()),
			summary.Source(),
			summary.Confidence(),
			len(details.EvidenceRefs()),
			len(details.ArtifactRefs()),
			memoryReviewDecisionStatus(details),
			truncateMessage(summary.Fact()),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue row", "メモリ候補の確認キュー行の出力に失敗しました"), err)
		}
	}
	return nil
}

// writeMemoryInboxBatchIDOnly writes one memory id per successfully
// processed row to stdout so scripted callers can pipe the result list.
// Per-id failures are reported on stderr (non-empty Failures yields an
// aggregated error, mirroring the old `memory accept <id> --id-only`
// contract that exited non-zero on a failed Accept). The stdout shape
// stays a strict superset of the old single-id form: when exactly one
// id succeeds, the only stdout line is that id.
func writeMemoryInboxBatchIDOnly(output io.Writer, errOutput io.Writer, result memoryInboxBatchResult) error {
	for _, details := range result.Processed {
		if _, err := fmt.Fprintln(output, details.Summary().MemoryID()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory ID", "memory ID の出力に失敗しました"), err)
		}
	}
	if len(result.Failures) == 0 {
		return nil
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(errOutput, "FAILED\t%s\t%s\n", failure.ID, localizedMemoryInboxFailureError(failure)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue failure row", "メモリ候補の確認キュー失敗行の出力に失敗しました"), err)
		}
	}
	return memoryInboxBatchFailureError(result)
}

func writeMemoryInboxBatch(output io.Writer, result memoryInboxBatchResult, asJSON bool) error {
	if asJSON {
		payload := memoryInboxBatchOutput{
			Action:    result.Action,
			Processed: make([]memoryDetailsOutput, 0, len(result.Processed)),
			Failures:  result.Failures,
		}
		for _, details := range result.Processed {
			payload.Processed = append(payload.Processed, newMemoryDetailsOutput(details))
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode memory review queue batch result", "メモリ候補の確認キュー batch 結果の JSON 出力に失敗しました"), err)
		}
		if len(result.Failures) > 0 {
			return memoryInboxBatchFailureError(result)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "action=%s processed=%d failures=%d\n", result.Action, len(result.Processed), len(result.Failures)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue batch summary", "メモリ候補の確認キュー batch サマリの出力に失敗しました"), err)
	}
	for _, details := range result.Processed {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\t%s\n", summary.MemoryID(), summary.Status(), summary.Source(), summary.Fact()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue batch row", "メモリ候補の確認キュー batch 行の出力に失敗しました"), err)
		}
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(output, "FAILED\t%s\t%s\n", failure.ID, localizedMemoryInboxFailureError(failure)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue failure row", "メモリ候補の確認キュー失敗行の出力に失敗しました"), err)
		}
	}
	if len(result.Failures) > 0 {
		return memoryInboxBatchFailureError(result)
	}
	return nil
}

func writeMemoryInboxCleanupResult(output io.Writer, result memoryInboxCleanupResult, asJSON bool) error {
	if asJSON {
		payload := memoryInboxCleanupOutput{
			Action:    result.Action,
			DryRun:    result.DryRun,
			Summary:   result.Summary,
			Matched:   make([]memoryDetailsOutput, 0, len(result.Matched)),
			Processed: make([]memoryDetailsOutput, 0, len(result.Processed)),
			Failures:  result.Failures,
		}
		for _, details := range result.Matched {
			payload.Matched = append(payload.Matched, newMemoryDetailsOutput(details))
		}
		for _, details := range result.Processed {
			payload.Processed = append(payload.Processed, newMemoryDetailsOutput(details))
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode memory review queue cleanup result", "メモリ候補の確認キュー cleanup 結果の JSON 出力に失敗しました"), err)
		}
		if len(result.Failures) > 0 {
			return memoryInboxCleanupFailureError(result)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "action=%s dry_run=%t matched=%d processed=%d failures=%d\n", result.Action, result.DryRun, len(result.Matched), len(result.Processed), len(result.Failures)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue cleanup summary", "メモリ候補の確認キュー cleanup サマリの出力に失敗しました"), err)
	}
	if result.Summary.Total > 0 {
		if _, err := fmt.Fprintf(output, "summary total=%d by_source[%s] by_type[%s]\n", result.Summary.Total, formatMemoryCountMap(result.Summary.BySource), formatMemoryCountMap(result.Summary.ByType)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue cleanup composition summary", "メモリ候補の確認キュー cleanup 構成サマリの出力に失敗しました"), err)
		}
	}
	if result.DryRun {
		for _, details := range result.Matched {
			summary := details.Summary()
			if _, err := fmt.Fprintf(output, "DRY_RUN\t%s\t%s\t%s\t%s\n", summary.MemoryID(), summary.Status(), summary.Source(), summary.Fact()); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue cleanup dry-run row", "メモリ候補の確認キュー cleanup dry-run 行の出力に失敗しました"), err)
			}
		}
	}
	for _, details := range result.Processed {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\n", summary.MemoryID(), summary.Status(), summary.Fact()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue cleanup processed row", "メモリ候補の確認キュー cleanup 処理済み行の出力に失敗しました"), err)
		}
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(output, "FAILED\t%s\t%s\n", failure.ID, localizedMemoryInboxFailureError(failure)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review queue cleanup failure row", "メモリ候補の確認キュー cleanup 失敗行の出力に失敗しました"), err)
		}
	}
	if len(result.Failures) > 0 {
		return memoryInboxCleanupFailureError(result)
	}
	return nil
}

func memoryInboxBatchFailureError(result memoryInboxBatchResult) error {
	return xerrors.New(Localizef(
		"memory review queue %s action failed for %d memory id(s)",
		"メモリ候補の確認キューの %s action が %d 件の memory id で失敗しました",
		result.Action, len(result.Failures),
	))
}

func localizedMemoryInboxFailureError(failure memoryInboxFailure) string {
	if failure.ErrorCode == memoryInboxCleanupNonCandidateCode {
		return Localize(memoryInboxCleanupNonCandidateError, "cleanup はメモリ候補だけを変更します")
	}
	return failure.Error
}

func memoryInboxCleanupFailureError(result memoryInboxCleanupResult) error {
	return xerrors.New(Localizef(
		"memory review queue cleanup failed for %d memory id(s)",
		"メモリ候補の確認キュー cleanup が %d 件の memory id で失敗しました",
		len(result.Failures),
	))
}
