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
		Short: Localize("Report durable-memory hygiene suggestions", "durable memory の hygiene 候補を報告する"),
	}
	cmd.AddCommand(c.newMemoryHygieneScanCommand())
	return cmd
}

func (c *RootCLI) newMemoryHygieneScanCommand() *cobra.Command {
	input := memoryHygieneScanCommandInput{expiryDays: defaultHygieneExpiryDays}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: Localize("Scan accepted memories for redaction / expiry / duplicate suggestions", "accepted memory に対して redaction / expiry / duplicate の hygiene 候補を検出する"),
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
		StalenessThreshold: time.Duration(input.expiryDays) * 24 * time.Hour,
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
			RedactionHitCount    int                          `json:"redaction_hit_count"`
			ExpiryCandidateCount int                          `json:"expiry_candidate_count"`
			DuplicateCount       int                          `json:"duplicate_count"`
			Suggestions          []memoryHygieneOutputEntry   `json:"suggestions"`
		}{
			RedactionHitCount:    result.RedactionHitCount,
			ExpiryCandidateCount: result.ExpiryCandidateCount,
			DuplicateCount:       result.DuplicateCount,
			Suggestions:          make([]memoryHygieneOutputEntry, 0, len(result.Suggestions)),
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
	if _, err := fmt.Fprintf(output, "redaction_hits=%d expiry_candidates=%d duplicates=%d\n",
		result.RedactionHitCount, result.ExpiryCandidateCount, result.DuplicateCount); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hygiene summary", "hygiene サマリの出力に失敗しました"), err)
	}
	for _, suggestion := range result.Suggestions {
		extra := ""
		if suggestion.DuplicateMemoryID != "" {
			extra = fmt.Sprintf(" duplicate_of=%s", suggestion.DuplicateMemoryID.String())
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
	MemoryID          string `json:"memory_id"`
	Kind              string `json:"kind"`
	Reason            string `json:"reason"`
	Fact              string `json:"fact"`
	SanitizedFact     string `json:"sanitized_fact,omitempty"`
	DuplicateMemoryID string `json:"duplicate_memory_id,omitempty"`
	ScopeKind         string `json:"scope_kind,omitempty"`
	ScopeValue        string `json:"scope_value,omitempty"`
	UpdatedAt         string `json:"updated_at"`
}

func newMemoryHygieneOutputEntry(suggestion apptypes.MemoryHygieneSuggestion) memoryHygieneOutputEntry {
	entry := memoryHygieneOutputEntry{
		MemoryID:      suggestion.MemoryID.String(),
		Kind:          string(suggestion.Kind),
		Reason:        suggestion.Reason,
		Fact:          suggestion.Fact,
		SanitizedFact: suggestion.SanitizedFact,
		UpdatedAt:     suggestion.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if suggestion.DuplicateMemoryID != "" {
		entry.DuplicateMemoryID = suggestion.DuplicateMemoryID.String()
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
