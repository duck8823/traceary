package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
		Short: Localize("Review candidate durable memories with provenance", "candidate durable memory を provenance 付きでレビューする"),
	}
	cmd.AddCommand(c.newMemoryInboxListCommand())
	cmd.AddCommand(c.newMemoryInboxAcceptCommand())
	cmd.AddCommand(c.newMemoryInboxRejectCommand())
	return cmd
}

func (c *RootCLI) newMemoryInboxListCommand() *cobra.Command {
	input := memoryInboxListCommandInput{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List candidate durable memories awaiting review", "レビュー待ちの candidate durable memory を一覧する"),
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
	cmd.Flags().StringSliceVar(&input.sources, "source", nil, Localize("filter by memory source (manual / extracted / extracted-hidden / remember-intent / imported)", "memory source (manual / extracted / extracted-hidden / remember-intent / imported) で絞り込む"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden candidates (low-quality auto-extractions kept for audit)", "extracted-hidden の候補も含める (audit 用に保存された低品質自動抽出)"))
	cmd.Flags().IntVar(&input.limit, "limit", defaultMemoryInboxLimit, Localize("maximum number of candidates to return", "表示件数"))
	cmd.Flags().IntVar(&input.offset, "offset", 0, Localize("number of candidates to skip before listing", "一覧表示前にスキップする件数"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryInboxAcceptCommand() *cobra.Command {
	input := memoryInboxBatchCommandInput{}
	cmd := &cobra.Command{
		Use:   "accept",
		Short: Localize("Accept every candidate durable memory in the id list", "指定した candidate durable memory をまとめて accept する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryInboxBatch(cmd.Context(), cmd.OutOrStdout(), input, memoryInboxActionAccept)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.ids, "ids", nil, Localize(
		"comma-separated list of memory ids to accept (repeatable)",
		"accept 対象の memory id をカンマ区切りで指定 (複数指定可)",
	))
	cmd.Flags().StringVar(&input.confidence, "confidence", "", Localize("accepted confidence (defaults to verified)", "accepted 時の confidence (既定値は verified)"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) newMemoryInboxRejectCommand() *cobra.Command {
	input := memoryInboxBatchCommandInput{}
	cmd := &cobra.Command{
		Use:   "reject",
		Short: Localize("Reject every candidate durable memory in the id list", "指定した candidate durable memory をまとめて reject する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryInboxBatch(cmd.Context(), cmd.OutOrStdout(), input, memoryInboxActionReject)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.ids, "ids", nil, Localize(
		"comma-separated list of memory ids to reject (repeatable)",
		"reject 対象の memory id をカンマ区切りで指定 (複数指定可)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

type memoryInboxAction string

const (
	memoryInboxActionAccept memoryInboxAction = "accept"
	memoryInboxActionReject memoryInboxAction = "reject"
)

func (c *RootCLI) runMemoryInboxList(ctx context.Context, output io.Writer, input memoryInboxListCommandInput) error {
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
	memoryTypes, err := parseMemoryTypes(input.memoryTypes)
	if err != nil {
		return err
	}
	sources, err := parseMemorySources(input.sources)
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
	// RememberIntentPriority is enforced at the query layer so remember-intent
	// rows surface ahead of other candidates BEFORE limit/offset applies — a
	// post-fetch in-memory sort would only re-order the current page and
	// could let a remember-intent row that lives just past the page boundary
	// stay hidden until later pages (#856).
	criteria := apptypes.NewMemoryListCriteriaBuilder(input.limit).
		Offset(input.offset).
		Scopes(scopes).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}).
		MemoryTypes(memoryTypes).
		Sources(sources).
		RememberIntentPriority(true).
		Build()
	summaries, err := c.memory.List(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list candidate memories", "candidate memory の一覧取得に失敗しました"), err)
	}

	items := make([]apptypes.MemoryDetails, 0, len(summaries))
	for _, summary := range summaries {
		details, err := c.memory.Show(ctx, summary.MemoryID())
		if err != nil {
			return xerrors.Errorf("failed to load memory %s: %w", summary.MemoryID().String(), err)
		}
		items = append(items, details)
	}
	return writeMemoryInboxList(output, items, input.asJSON)
}

func (c *RootCLI) runMemoryInboxBatch(ctx context.Context, output io.Writer, input memoryInboxBatchCommandInput, action memoryInboxAction) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	ids := normaliseInboxIDs(input.ids)
	if len(ids) == 0 {
		return xerrors.Errorf(Localize("--ids must list at least one memory id", "--ids に少なくとも1つの memory id を指定してください"))
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
			return xerrors.Errorf("unsupported inbox action: %s", action)
		}
		if err != nil {
			result.Failures = append(result.Failures, memoryInboxFailure{ID: rawID, Error: err.Error()})
			continue
		}
		result.Processed = append(result.Processed, details)
	}
	return writeMemoryInboxBatch(output, result, input.asJSON)
}

// memoryInboxBatchResult is the domain-neutral summary of a batch run,
// shared by the CLI output and the MCP tool so both surfaces expose the
// same success / failure breakdown.
type memoryInboxBatchResult struct {
	Action    string
	Processed []apptypes.MemoryDetails
	Failures  []memoryInboxFailure
}

type memoryInboxFailure struct {
	ID    string
	Error string
}

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
		if _, err := fmt.Fprintln(output, Localize("No candidate durable memories in the inbox.", "inbox に candidate durable memory はありません")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty inbox message", "空の inbox メッセージの出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintln(output, "MEMORY_ID\tTYPE\tSCOPE\tSOURCE\tEVIDENCE\tARTIFACT\tFACT"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print inbox header", "inbox ヘッダーの出力に失敗しました"), err)
	}
	for _, details := range items {
		summary := details.Summary()
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			summary.MemoryID(),
			summary.MemoryType(),
			formatMemoryScope(summary.Scope()),
			summary.Source(),
			len(details.EvidenceRefs()),
			len(details.ArtifactRefs()),
			truncateMessage(summary.Fact()),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print inbox row", "inbox 行の出力に失敗しました"), err)
		}
	}
	return nil
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
			return xerrors.Errorf("%s: %w", Localize("failed to encode inbox batch result", "inbox batch 結果の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "action=%s processed=%d failures=%d\n", result.Action, len(result.Processed), len(result.Failures)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print inbox batch summary", "inbox batch サマリの出力に失敗しました"), err)
	}
	for _, details := range result.Processed {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\n", summary.MemoryID(), summary.Status(), summary.Fact()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print inbox batch row", "inbox batch 行の出力に失敗しました"), err)
		}
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(output, "FAILED\t%s\t%s\n", failure.ID, failure.Error); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print inbox failure row", "inbox 失敗行の出力に失敗しました"), err)
		}
	}
	return nil
}
