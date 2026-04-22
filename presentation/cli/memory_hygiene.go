package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const defaultHygieneExpiryDays = 90

func (c *RootCLI) newMemoryHygieneCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hygiene",
		Short: Localize("Report and apply durable-memory hygiene suggestions", "durable memory の hygiene 候補を報告・適用する"),
	}
	cmd.AddCommand(c.newMemoryHygieneScanCommand())
	cmd.AddCommand(c.newMemoryHygieneApplyCommand())
	return cmd
}

func (c *RootCLI) newMemoryHygieneApplyCommand() *cobra.Command {
	input := memoryHygieneApplyCommandInput{expiryDays: defaultHygieneExpiryDays}
	cmd := &cobra.Command{
		Use:   "apply",
		Short: Localize("Apply hygiene suggestions by memory id", "memory id を指定して hygiene 候補を適用する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryHygieneApply(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringSliceVar(&input.ids, "ids", nil, Localize(
		"comma-separated list of memory ids whose hygiene suggestion should be applied (repeatable)",
		"適用対象の memory id をカンマ区切りで指定 (複数指定可)",
	))
	cmd.Flags().IntVar(&input.expiryDays, "expiry-days", defaultHygieneExpiryDays, Localize(
		"number of days without update before a memory is considered an expiry candidate",
		"expiry 候補として検出するまでの未更新日数",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) runMemoryHygieneApply(ctx context.Context, output io.Writer, input memoryHygieneApplyCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memoryHygiene == nil {
		return xerrors.Errorf(Localize("memory hygiene usecase is not configured", "memory hygiene ユースケースが設定されていません"))
	}
	ids := normaliseInboxIDs(input.ids)
	if len(ids) == 0 {
		return xerrors.Errorf(Localize("--ids must list at least one memory id", "--ids に少なくとも1つの memory id を指定してください"))
	}
	if input.expiryDays <= 0 {
		return xerrors.Errorf(Localize("--expiry-days must be greater than 0", "--expiry-days は 0 より大きい必要があります"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}
	result, err := c.memoryHygiene.Apply(ctx, apptypes.MemoryHygieneApplyCriteria{
		MemoryIDs:          ids,
		StalenessThreshold: time.Duration(input.expiryDays) * 24 * time.Hour,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to apply hygiene suggestions", "hygiene 候補の適用に失敗しました"), err)
	}
	return writeMemoryHygieneApplyResult(output, result, input.asJSON)
}

func writeMemoryHygieneApplyResult(output io.Writer, result apptypes.MemoryHygieneApplyResult, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		payload := struct {
			Applied  []applyAppliedOutput  `json:"applied"`
			Failures []applyFailureOutput  `json:"failures,omitempty"`
		}{
			Applied:  make([]applyAppliedOutput, 0, len(result.Applied)),
			Failures: make([]applyFailureOutput, 0, len(result.Failures)),
		}
		for _, applied := range result.Applied {
			payload.Applied = append(payload.Applied, applyAppliedOutput{
				MemoryID:   applied.MemoryID,
				Kind:       string(applied.Kind),
				Transition: applied.Transition,
				Status:     applied.Details.Summary().Status().String(),
			})
		}
		for _, failure := range result.Failures {
			payload.Failures = append(payload.Failures, applyFailureOutput{MemoryID: failure.MemoryID, Error: failure.Error})
		}
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode hygiene apply result", "hygiene apply 結果の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, Localize(
		"applied=%d failures=%d\n",
		"適用=%d 失敗=%d\n",
	), len(result.Applied), len(result.Failures)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hygiene apply summary", "hygiene apply サマリの出力に失敗しました"), err)
	}
	for _, applied := range result.Applied {
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\t%s\n", applied.MemoryID, applied.Kind, applied.Transition, applied.Details.Summary().Status()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print hygiene apply row", "hygiene apply 行の出力に失敗しました"), err)
		}
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(output, "FAILED\t%s\t%s\n", failure.MemoryID, failure.Error); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print hygiene apply failure", "hygiene apply 失敗行の出力に失敗しました"), err)
		}
	}
	return nil
}

type applyAppliedOutput struct {
	MemoryID   string `json:"memory_id"`
	Kind       string `json:"kind"`
	Transition string `json:"transition"`
	Status     string `json:"status"`
}

type applyFailureOutput struct {
	MemoryID string `json:"memory_id"`
	Error    string `json:"error"`
}

func (c *RootCLI) newMemoryHygieneScanCommand() *cobra.Command {
	input := memoryHygieneScanCommandInput{expiryDays: defaultHygieneExpiryDays}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: Localize("Scan accepted memories for redaction / expiry / duplicate / supersede / validity-overlap suggestions", "accepted memory に対して redaction / expiry / duplicate / supersede / validity-overlap の hygiene 候補を検出する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryHygieneScan(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize(
		"workspace scope to scan (defaults to env/detected workspace; empty scans all scopes)",
		"スキャン対象の workspace scope (未指定時は env/検出 workspace、空で全 scope)",
	))
	cmd.Flags().IntVar(&input.expiryDays, "expiry-days", defaultHygieneExpiryDays, Localize(
		"number of days without update before a memory is flagged for expiry",
		"expiry 候補として検出するまでの未更新日数",
	))
	cmd.Flags().Float64Var(&input.similarity, "similarity", 0, Localize(
		"word-Jaccard threshold for supersede_candidate detection (0.0-1.0; 0 uses the default 0.6)",
		"supersede_candidate 検出の word-Jaccard 閾値 (0.0-1.0、0 は既定値 0.6)",
	))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) runMemoryHygieneScan(ctx context.Context, output io.Writer, input memoryHygieneScanCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memoryHygiene == nil {
		return xerrors.Errorf(Localize("memory hygiene usecase is not configured", "memory hygiene ユースケースが設定されていません"))
	}
	if input.expiryDays <= 0 {
		return xerrors.Errorf(Localize("--expiry-days must be greater than 0", "--expiry-days は 0 より大きい必要があります"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	scope, err := resolveExportScope(ctx, input.workspace)
	if err != nil {
		return err
	}
	criteria := apptypes.MemoryHygieneScanCriteria{
		StalenessThreshold:  time.Duration(input.expiryDays) * 24 * time.Hour,
		SimilarityThreshold: input.similarity,
	}
	if scope != nil {
		criteria.Scopes = []domtypes.MemoryScope{scope}
	}

	result, err := c.memoryHygiene.Scan(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to scan memories for hygiene", "hygiene スキャンに失敗しました"), err)
	}
	return writeMemoryHygieneScanResult(output, result, input.asJSON)
}

func writeMemoryHygieneScanResult(output io.Writer, result apptypes.MemoryHygieneScanResult, asJSON bool) error {
	if asJSON {
		payload := struct {
			RedactionHitCount             int                        `json:"redaction_hit_count"`
			ExpiryCandidateCount          int                        `json:"expiry_candidate_count"`
			DuplicateCount                int                        `json:"duplicate_count"`
			SupersedeCandidateCount       int                        `json:"supersede_candidate_count"`
			ValidityOverlapSupersedeCount int                        `json:"validity_overlap_supersede_count"`
			Suggestions                   []memoryHygieneOutputEntry `json:"suggestions"`
		}{
			RedactionHitCount:             result.RedactionHitCount,
			ExpiryCandidateCount:          result.ExpiryCandidateCount,
			DuplicateCount:                result.DuplicateCount,
			SupersedeCandidateCount:       result.SupersedeCandidateCount,
			ValidityOverlapSupersedeCount: result.ValidityOverlapSupersedeCount,
			Suggestions:                   make([]memoryHygieneOutputEntry, 0, len(result.Suggestions)),
		}
		for _, suggestion := range result.Suggestions {
			payload.Suggestions = append(payload.Suggestions, newMemoryHygieneOutputEntry(suggestion))
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to encode hygiene scan result", "hygiene scan 結果の JSON 出力に失敗しました"), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, Localize(
		"redaction_hits=%d expiry_candidates=%d duplicates=%d supersede_candidates=%d validity_overlap_supersedes=%d\n",
		"redaction ヒット=%d expiry 候補=%d 重複=%d supersede 候補=%d validity 重複 supersede=%d\n",
	), result.RedactionHitCount, result.ExpiryCandidateCount, result.DuplicateCount, result.SupersedeCandidateCount, result.ValidityOverlapSupersedeCount); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hygiene summary", "hygiene サマリの出力に失敗しました"), err)
	}
	for _, suggestion := range result.Suggestions {
		extra := ""
		if suggestion.DuplicateMemoryID != "" {
			extra = fmt.Sprintf(" duplicate_of=%s", suggestion.DuplicateMemoryID.String())
		}
		if suggestion.ReplacementMemoryID != "" {
			extra += fmt.Sprintf(" replacement=%s similarity=%.2f", suggestion.ReplacementMemoryID.String(), suggestion.Similarity)
		}
		if suggestion.SanitizedFact != "" {
			extra += fmt.Sprintf(" sanitized=%q", truncateMessage(suggestion.SanitizedFact))
		}
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\t%s%s\t%s\n",
			suggestion.MemoryID.String(),
			suggestion.Kind,
			memoryScopeLabelOrDash(suggestion.Scope),
			suggestion.Reason,
			extra,
			truncateMessage(suggestion.Fact),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print hygiene suggestion row", "hygiene 候補行の出力に失敗しました"), err)
		}
	}
	return nil
}

type memoryHygieneOutputEntry struct {
	MemoryID            string  `json:"memory_id"`
	Kind                string  `json:"kind"`
	Reason              string  `json:"reason"`
	Fact                string  `json:"fact"`
	SanitizedFact       string  `json:"sanitized_fact,omitempty"`
	DuplicateMemoryID   string  `json:"duplicate_memory_id,omitempty"`
	ReplacementMemoryID string  `json:"replacement_memory_id,omitempty"`
	ReplacementFact     string  `json:"replacement_fact,omitempty"`
	Similarity          float64 `json:"similarity,omitempty"`
	ScopeKind           string  `json:"scope_kind,omitempty"`
	ScopeValue          string  `json:"scope_value,omitempty"`
	UpdatedAt           string  `json:"updated_at"`
}

func newMemoryHygieneOutputEntry(suggestion apptypes.MemoryHygieneSuggestion) memoryHygieneOutputEntry {
	entry := memoryHygieneOutputEntry{
		MemoryID:      suggestion.MemoryID.String(),
		Kind:          string(suggestion.Kind),
		Reason:        suggestion.Reason,
		Fact:          suggestion.Fact,
		SanitizedFact: suggestion.SanitizedFact,
		Similarity:    suggestion.Similarity,
		UpdatedAt:     suggestion.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if suggestion.DuplicateMemoryID != "" {
		entry.DuplicateMemoryID = suggestion.DuplicateMemoryID.String()
	}
	if suggestion.ReplacementMemoryID != "" {
		entry.ReplacementMemoryID = suggestion.ReplacementMemoryID.String()
		entry.ReplacementFact = suggestion.ReplacementFact
	}
	if suggestion.Scope != nil {
		entry.ScopeKind = suggestion.Scope.Kind().String()
		entry.ScopeValue = suggestion.Scope.Key()
	}
	return entry
}

func memoryScopeLabelOrDash(scope domtypes.MemoryScope) string {
	if scope == nil {
		return "-"
	}
	return fmt.Sprintf("%s=%s", scope.Kind().String(), scope.Key())
}
