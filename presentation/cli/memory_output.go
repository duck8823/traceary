package cli

import (
	"fmt"
	"io"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func writeMemorySummariesByFormat(output io.Writer, summaries []apptypes.MemorySummary, asJSON bool) error {
	if asJSON {
		serialized := make([]memorySummaryOutput, 0, len(summaries))
		for _, summary := range summaries {
			serialized = append(serialized, newMemorySummaryOutput(summary))
		}
		return writeJSON(output, serialized)
	}

	return writeMemorySummaries(output, summaries)
}

func writeMemoryDetailsByFormat(output io.Writer, details apptypes.MemoryDetails, asJSON bool) error {
	if asJSON {
		return writeJSON(output, newMemoryDetailsOutput(details))
	}

	return writeMemoryDetails(output, details)
}

func writeMemoryMutationResult(output io.Writer, details apptypes.MemoryDetails, idOnly bool, asJSON bool) error {
	if idOnly {
		if _, err := fmt.Fprintln(output, details.Summary().MemoryID()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory ID", "memory ID の出力に失敗しました"), err)
		}
		return nil
	}

	return writeMemoryDetailsByFormat(output, details, asJSON)
}

func writeExtractedMemoryCandidatesByFormat(output io.Writer, details []apptypes.MemoryDetails, asJSON bool) error {
	if asJSON {
		serialized := make([]memoryDetailsOutput, 0, len(details))
		for _, detail := range details {
			serialized = append(serialized, newMemoryDetailsOutput(detail))
		}
		return writeJSON(output, serialized)
	}

	if len(details) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No extractable durable-memory candidates were found.", "抽出可能な durable memory candidate は見つかりませんでした")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty extraction message", "空の抽出結果メッセージの出力に失敗しました"), err)
		}
		return nil
	}

	summaries := make([]apptypes.MemorySummary, 0, len(details))
	for _, detail := range details {
		summaries = append(summaries, detail.Summary())
	}
	return writeMemorySummaries(output, summaries)
}

func writeMemorySummaries(output io.Writer, summaries []apptypes.MemorySummary) error {
	if len(summaries) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No matching durable memories.", "一致する durable memory はありません")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty memory list message", "空の durable memory 一覧メッセージの出力に失敗しました"), err)
		}
		return nil
	}

	if _, err := fmt.Fprintln(output, "UPDATED_AT\tMEMORY_ID\tTYPE\tSCOPE\tSTATUS\tCONFIDENCE\tSOURCE\tFACT"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory list header", "durable memory 一覧ヘッダーの出力に失敗しました"), err)
	}
	for _, summary := range summaries {
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			summary.UpdatedAt().UTC().Format(time.RFC3339),
			summary.MemoryID(),
			summary.MemoryType(),
			formatMemoryScope(summary.Scope()),
			summary.Status(),
			summary.Confidence(),
			summary.Source(),
			truncateMessage(summary.Fact()),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory summary row", "durable memory 一覧行の出力に失敗しました"), err)
		}
	}

	return nil
}

func writeMemoryDetails(output io.Writer, details apptypes.MemoryDetails) error {
	summary := details.Summary()
	if _, err := fmt.Fprintf(
		output,
		"MEMORY_ID: %s\nTYPE: %s\nSCOPE: %s\nSTATUS: %s\nCONFIDENCE: %s\nSOURCE: %s\nSUPERSEDES: %s\nEXPIRES_AT: %s\nCREATED_AT: %s\nUPDATED_AT: %s\nFACT: %s\n",
		summary.MemoryID(),
		summary.MemoryType(),
		formatMemoryScope(summary.Scope()),
		summary.Status(),
		summary.Confidence(),
		summary.Source(),
		formatOptionalMemoryID(summary.Supersedes()),
		formatOptionalTime(summary.ExpiresAt()),
		summary.CreatedAt().UTC().Format(time.RFC3339),
		summary.UpdatedAt().UTC().Format(time.RFC3339),
		summary.Fact(),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory fields", "durable memory 共通項目の出力に失敗しました"), err)
	}

	if err := writeEvidenceRefSection(output, details.EvidenceRefs()); err != nil {
		return err
	}
	if err := writeArtifactRefSection(output, details.ArtifactRefs()); err != nil {
		return err
	}

	return nil
}

func writeEvidenceRefSection(output io.Writer, refs []domtypes.EvidenceRef) error {
	if _, err := fmt.Fprintln(output, "\nEVIDENCE_REFS:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print evidence refs heading", "evidence refs 見出しの出力に失敗しました"), err)
	}
	if len(refs) == 0 {
		if _, err := fmt.Fprintln(output, "- -"); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty evidence refs", "空の evidence refs の出力に失敗しました"), err)
		}
		return nil
	}
	for _, ref := range refs {
		if _, err := fmt.Fprintf(output, "- %s:%s\n", ref.Kind(), ref.Value()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print evidence ref", "evidence ref の出力に失敗しました"), err)
		}
	}

	return nil
}

func writeArtifactRefSection(output io.Writer, refs []domtypes.ArtifactRef) error {
	if _, err := fmt.Fprintln(output, "\nARTIFACT_REFS:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print artifact refs heading", "artifact refs 見出しの出力に失敗しました"), err)
	}
	if len(refs) == 0 {
		if _, err := fmt.Fprintln(output, "- -"); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty artifact refs", "空の artifact refs の出力に失敗しました"), err)
		}
		return nil
	}
	for _, ref := range refs {
		if _, err := fmt.Fprintf(output, "- %s:%s\n", ref.Kind(), ref.Value()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print artifact ref", "artifact ref の出力に失敗しました"), err)
		}
	}

	return nil
}

func newMemorySummaryOutput(summary apptypes.MemorySummary) memorySummaryOutput {
	var supersedes *string
	if value, ok := summary.Supersedes().Value(); ok {
		resolved := value.String()
		supersedes = &resolved
	}

	var expiresAt *string
	if value, ok := summary.ExpiresAt().Value(); ok {
		resolved := value.UTC().Format(time.RFC3339)
		expiresAt = &resolved
	}

	return memorySummaryOutput{
		MemoryID:   summary.MemoryID().String(),
		Type:       summary.MemoryType().String(),
		ScopeKind:  summary.Scope().Kind().String(),
		ScopeValue: summary.Scope().Key(),
		Fact:       summary.Fact(),
		Status:     summary.Status().String(),
		Confidence: summary.Confidence().String(),
		Source:     summary.Source().String(),
		Supersedes: supersedes,
		ExpiresAt:  expiresAt,
		CreatedAt:  summary.CreatedAt().UTC().Format(time.RFC3339),
		UpdatedAt:  summary.UpdatedAt().UTC().Format(time.RFC3339),
	}
}

func newMemoryDetailsOutput(details apptypes.MemoryDetails) memoryDetailsOutput {
	evidenceRefs := make([]string, 0, len(details.EvidenceRefs()))
	for _, ref := range details.EvidenceRefs() {
		evidenceRefs = append(evidenceRefs, fmt.Sprintf("%s:%s", ref.Kind(), ref.Value()))
	}

	artifactRefs := make([]string, 0, len(details.ArtifactRefs()))
	for _, ref := range details.ArtifactRefs() {
		artifactRefs = append(artifactRefs, fmt.Sprintf("%s:%s", ref.Kind(), ref.Value()))
	}

	return memoryDetailsOutput{
		Summary:      newMemorySummaryOutput(details.Summary()),
		EvidenceRefs: evidenceRefs,
		ArtifactRefs: artifactRefs,
	}
}

func formatMemoryScope(scope domtypes.MemoryScope) string {
	if scope == nil {
		return "-"
	}

	return fmt.Sprintf("%s:%s", scope.Kind(), scope.Key())
}

func formatOptionalMemoryID(value domtypes.Optional[domtypes.MemoryID]) string {
	if memoryID, ok := value.Value(); ok {
		return memoryID.String()
	}

	return "-"
}

func formatOptionalTime(value domtypes.Optional[time.Time]) string {
	if resolved, ok := value.Value(); ok {
		return resolved.UTC().Format(time.RFC3339)
	}

	return "-"
}
