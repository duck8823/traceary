package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func (m reviewModel) View() string {
	if len(m.items) == 0 {
		return m.styles.Title.Render(memoryReviewWorkflowLabel()) +
			"\n\n" +
			m.styles.Subtle.Render(memoryReviewEmptyQueueMessage()) +
			"\n\n" +
			m.styles.Help.Render(Localize("press q to quit", "終了するには q"))
	}
	switch m.mode {
	case reviewModeHelp:
		return m.renderHelp()
	case reviewModeViewEvidence:
		return m.renderEvidence()
	case reviewModeEdit:
		return m.renderEdit()
	case reviewModeAttach:
		return m.renderAttach()
	}
	return m.renderBrowse()
}

func (m reviewModel) renderBrowse() string {
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(memoryReviewWorkflowTitle(Localize("decision card", "判断カード"))))
	b.WriteString("\n")
	b.WriteString(m.styles.Subtle.Render(memoryCandidateCountLabel(m.cursor+1, len(m.items))))
	b.WriteString("\n\n")

	current := m.items[m.cursor]
	summary := current.Summary()
	b.WriteString(m.styles.Subtle.Render(Localize("DECISION CONTEXT", "判断コンテキスト")))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%-23s %s\n", Localize("MEMORY_ID:", "MEMORY_ID:"), summary.MemoryID())
	fmt.Fprintf(&b, "%-23s %s\n", Localize("TYPE:", "TYPE:"), summary.MemoryType())
	fmt.Fprintf(&b, "%-23s %s\n", Localize("SCOPE:", "SCOPE:"), formatMemoryScope(summary.Scope()))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("SOURCE:", "SOURCE:"), summary.Source())
	fmt.Fprintf(&b, "%-23s %s\n", Localize("CONFIDENCE:", "CONFIDENCE:"), summary.Confidence())
	fmt.Fprintf(&b, "%-23s %s\n", Localize("QUALITY_SIGNAL:", "QUALITY_SIGNAL:"), memoryReviewQualitySignal(current))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("REMEMBERED_BY_OPERATOR:", "OPERATOR_REMEMBERED:"), formatMemoryReviewRememberIntent(summary))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("EVIDENCE_REFS:", "EVIDENCE_REFS:"), Localizef("%d (press v to inspect)", "%d (v で確認)", len(current.EvidenceRefs())))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("ARTIFACT_REFS:", "ARTIFACT_REFS:"), Localizef("%d (press v to inspect)", "%d (v で確認)", len(current.ArtifactRefs())))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("CREATED_AT:", "CREATED_AT:"), formatJSONTime(summary.CreatedAt()))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("UPDATED_AT:", "UPDATED_AT:"), formatJSONTime(summary.UpdatedAt()))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("MEMORY_CANDIDATE_AGE:", "メモリ候補の経過時間:"), formatMemoryReviewCandidateAge(summary, topNowFunc().UTC()))
	fmt.Fprintf(&b, "%-23s %s\n", Localize("DUPLICATE_SUPERSEDE:", "DUPLICATE_SUPERSEDE:"), memoryReviewDuplicateSupersedeHint(summary))
	b.WriteString("\n")
	b.WriteString(m.styles.Active.Render(Localize("MEMORY CANDIDATE FACT:", "メモリ候補 fact:")))
	b.WriteString("\n")
	b.WriteString(summary.Fact())
	b.WriteString("\n\n")
	b.WriteString(m.styles.Subtle.Render(Localize("EVIDENCE-FIRST REVIEW", "evidence 優先 review")))
	b.WriteString("\n")
	for _, line := range memoryReviewEvidencePreview(current) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(memoryReviewDecisionGuidance(current))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Subtle.Render(Localize("ACCEPT AS-IS CHECKLIST", "accept as-is checklist")))
	b.WriteString("\n")
	for _, item := range memoryReviewAcceptChecklist(current) {
		b.WriteString("• ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	if !m.currentCandidateBlocksAccept() && m.currentCandidateNeedsAcceptConfirmation() {
		b.WriteString(m.styles.Warning.Render(Localize("• This weak candidate requires pressing `a` twice to accept as-is; prefer edit/distill when wording is unclear.", "• この弱いメモリ候補を accept as-is するには `a` を 2 回押す必要があります。文言が曖昧なら edit/distill を優先してください。")))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if state := m.reviewed[m.cursor]; state != "" {
		b.WriteString(m.styles.Subtle.Render(Localizef("queued action: %s", "予約済みアクション: %s", state)))
		b.WriteString("\n")
	}
	if m.acceptConfirmationMatchesCurrent() {
		b.WriteString(m.styles.Warning.Render(Localize("accept confirmation armed: press a again to accept this candidate as-is", "accept 確認中: このメモリ候補をそのまま accept するにはもう一度 a")))
		b.WriteString("\n")
	}
	if m.currentCandidateBlocksAccept() {
		b.WriteString(m.styles.Warning.Render(Localize("accept as-is unavailable: accepted memory requires at least one evidence ref", "accept as-is 不可: accepted memory には 1 件以上の evidence ref が必要です")))
		b.WriteString("\n")
	}
	if m.statusMsg != "" {
		b.WriteString(m.styles.Subtle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.styles.Help.Render(memoryReviewBrowseHelp(current)))
	return b.String()
}

func (m reviewModel) renderEvidence() string {
	current := m.items[m.cursor]
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(memoryReviewWorkflowTitle(Localize("evidence", "evidence"))))
	b.WriteString("\n\n")
	b.WriteString(Localize("EVIDENCE_REFS:", "EVIDENCE_REFS:"))
	b.WriteString("\n")
	if len(current.EvidenceRefs()) == 0 {
		b.WriteString("- -\n")
		b.WriteString("\n")
		b.WriteString(memoryReviewAttachGuidance(current))
		b.WriteString("\n")
	} else {
		for _, ref := range current.EvidenceRefs() {
			fmt.Fprintf(&b, "- %s\n", formatMemoryReviewRefLine(ref.Kind().String(), ref.Value()))
		}
	}
	b.WriteString("\n")
	b.WriteString(Localize("ARTIFACT_REFS:", "ARTIFACT_REFS:"))
	b.WriteString("\n")
	if len(current.ArtifactRefs()) == 0 {
		b.WriteString("- -\n")
	} else {
		for _, ref := range current.ArtifactRefs() {
			fmt.Fprintf(&b, "- %s\n", formatMemoryReviewRefLine(ref.Kind().String(), ref.Value()))
		}
	}
	b.WriteString("\n")
	b.WriteString(m.styles.Help.Render(Localize("r attach evidence · v / esc back · q quit", "r evidence 追加 · v / esc 戻る · q 終了")))
	return b.String()
}

func (m reviewModel) renderHelp() string {
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(memoryReviewWorkflowTitle(Localize("help", "ヘルプ"))))
	b.WriteString("\n\n")
	b.WriteString(Localize("Actions:\n", "アクション:\n"))
	b.WriteString("  a    " + Localize("accept as-is only when the checklist passes; weak candidates require a second a", "checklist を満たす場合だけ accept as-is。弱いメモリ候補は a の再入力が必要") + "\n")
	b.WriteString("  x    " + Localize("reject incorrect, stale, duplicate, or unsafe candidates", "誤り・古い・重複・危険なメモリ候補を reject") + "\n")
	b.WriteString("  s    " + Localize("skip when more context is needed before deciding", "判断に追加 context が必要な場合は skip") + "\n")
	b.WriteString("  e    " + Localize("edit / distill when wording is unclear or scope needs tightening (Enter to commit)", "文言が曖昧、または scope 調整が必要なら edit / distill (Enter で確定)") + "\n")
	b.WriteString("  r    " + Localize("attach one evidence ref to the current memory candidate", "現在のメモリ候補に evidence ref を1つ追加") + "\n")
	b.WriteString("  v    " + Localize("view evidence and artifact refs", "evidence と artifact refs を表示") + "\n")
	b.WriteString("  ?    " + Localize("toggle this help", "このヘルプの表示を切り替え") + "\n")
	b.WriteString("  q    " + Localize("quit and apply queued decisions", "終了して保留中のアクションを実行") + "\n")
	b.WriteString("\n")
	b.WriteString(Localize("Why accept as-is checklist:\n", "accept as-is の checklist:\n"))
	b.WriteString("  - " + Localize("the fact is factual and stable", "fact が事実で安定している") + "\n")
	b.WriteString("  - " + Localize("the memory will be useful in future sessions", "将来の session で有用") + "\n")
	b.WriteString("  - " + Localize("the scope and type are correct", "scope と type が正しい") + "\n")
	b.WriteString("  - " + Localize("evidence supports the candidate", "evidence がメモリ候補を支えている") + "\n")
	b.WriteString("  - " + Localize("it is not duplicate, stale, or superseded", "重複・古い・supersede 済みではない") + "\n")
	b.WriteString("\n")
	b.WriteString(Localize(
		"Edit / distill never auto-accepts the candidate's fact: the operator must type the durable fact, which is then run through `memory store distill --replace=supersede`.",
		"edit / distill ではメモリ候補の fact を自動採用しません。operator が新しい fact を入力した上で `memory store distill --replace=supersede` 経由で記録します。",
	))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Help.Render(Localize("? / esc close help · q quit", "? / esc ヘルプを閉じる · q quit")))
	return b.String()
}

func (m reviewModel) renderEdit() string {
	current := m.items[m.editIndex]
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(Localize("edit · type a new operator-authored fact", "edit · operator が書き起こした fact を入力")))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Subtle.Render(Localize("source memory candidate fact:", "元のメモリ候補の fact:")))
	b.WriteString("\n")
	b.WriteString(current.Summary().Fact())
	b.WriteString("\n\n")
	b.WriteString(m.styles.Active.Render(Localize("YOUR FACT:", "あなたの FACT:")))
	b.WriteString("\n")
	b.WriteString("> ")
	b.WriteString(m.editBuffer)
	b.WriteString("\n\n")
	if m.statusMsg != "" {
		b.WriteString(m.styles.Warning.Render(m.statusMsg))
		b.WriteString("\n\n")
	}
	b.WriteString(m.styles.Help.Render(Localize(
		"enter commit · esc cancel · backspace edit",
		"enter 確定 · esc キャンセル · backspace 編集",
	)))
	return b.String()
}

func (m reviewModel) renderAttach() string {
	current := m.items[m.attachIndex]
	summary := current.Summary()
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(memoryReviewWorkflowTitle(Localize("attach evidence", "evidence 追加"))))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%-14s %s\n", Localize("MEMORY_ID:", "MEMORY_ID:"), summary.MemoryID())
	fmt.Fprintf(&b, "%-14s %s\n", Localize("TYPE:", "TYPE:"), summary.MemoryType())
	fmt.Fprintf(&b, "%-14s %s\n", Localize("SCOPE:", "SCOPE:"), formatMemoryScope(summary.Scope()))
	fmt.Fprintf(&b, "%-14s %d\n", Localize("EVIDENCE:", "EVIDENCE:"), len(current.EvidenceRefs()))
	fmt.Fprintf(&b, "%-14s %d\n", Localize("ARTIFACTS:", "ARTIFACTS:"), len(current.ArtifactRefs()))
	b.WriteString("\n")
	b.WriteString(m.styles.Subtle.Render(Localize(
		"Enter refs as kind:value. Comma-separated refs are supported; use artifact:kind:value for optional artifacts. Examples: event:evt-123, file:/tmp/notes.md#L10-L20, artifact:pr:#1073",
		"kind:value 形式の refs を入力してください。カンマ区切り可。任意の artifact は artifact:kind:value です。例: event:evt-123, file:/tmp/notes.md#L10-L20, artifact:pr:#1073",
	)))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Active.Render(Localize("REFS:", "REFS:")))
	b.WriteString("\n")
	b.WriteString("> ")
	b.WriteString(m.attachBuffer)
	if m.statusMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(m.styles.Warning.Render(m.statusMsg))
	}
	b.WriteString("\n\n")
	b.WriteString(m.styles.Help.Render(Localize("enter queues evidence attach · esc cancel", "enter で evidence 追加を保留 · esc キャンセル")))
	return b.String()
}

// Decisions returns the queued decisions in operator order so the
// runner can apply them after Bubble Tea exits.
func (m reviewModel) Decisions() []reviewDecision {
	out := make([]reviewDecision, len(m.decisions))
	copy(out, m.decisions)
	return out
}

func (m reviewModel) currentCandidateNeedsAcceptConfirmation() bool {
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return false
	}
	return memoryReviewRequiresAcceptConfirmation(m.items[m.cursor])
}

func (m reviewModel) currentCandidateBlocksAccept() bool {
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return false
	}
	return memoryReviewBlocksAccept(m.items[m.cursor])
}

func (m reviewModel) acceptConfirmationMatchesCurrent() bool {
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return false
	}
	return m.acceptConfirmID != "" && m.acceptConfirmID == m.items[m.cursor].Summary().MemoryID()
}

func memoryReviewBlocksAccept(details apptypes.MemoryDetails) bool {
	return len(details.EvidenceRefs()) == 0
}

func memoryReviewRequiresAcceptConfirmation(details apptypes.MemoryDetails) bool {
	summary := details.Summary()
	return summary.Source() == domtypes.MemorySourceExtractedHidden || summary.Confidence() == domtypes.ConfidenceLow
}

func memoryReviewDecisionStatus(details apptypes.MemoryDetails) string {
	switch {
	case memoryReviewBlocksAccept(details):
		return "blocked:no-evidence"
	case memoryReviewRequiresAcceptConfirmation(details):
		return "needs-confirmation"
	default:
		return "ready"
	}
}

func memoryReviewQualitySignal(details apptypes.MemoryDetails) string {
	summary := details.Summary()
	signals := make([]string, 0, 4)
	switch summary.Confidence() {
	case domtypes.ConfidenceVerified, domtypes.ConfidenceHigh:
		signals = append(signals, Localize("strong confidence", "信頼度が高い"))
	case domtypes.ConfidenceLow:
		signals = append(signals, Localize("low confidence", "信頼度が低い"))
	default:
		signals = append(signals, Localize("medium confidence", "信頼度は中程度"))
	}
	switch summary.Source() {
	case domtypes.MemorySourceRememberIntent:
		signals = append(signals, Localize("explicit remember intent", "明示的な remember intent"))
	case domtypes.MemorySourceExtractedHidden:
		// Keep the exact filter value visible for copy/paste parity with
		// memory source filters and exported audit data.
		signals = append(signals, "source=extracted-hidden")
	case domtypes.MemorySourceExtracted, domtypes.MemorySourceCompactSummary:
		signals = append(signals, Localize("generated memory candidate", "生成されたメモリ候補"))
	case domtypes.MemorySourceManual:
		signals = append(signals, Localize("manual source", "手動 source"))
	default:
		signals = append(signals, summary.Source().String())
	}
	if len(details.EvidenceRefs()) == 0 {
		signals = append(signals, Localize("no evidence refs", "evidence ref なし"))
	}
	if memoryReviewBlocksAccept(details) {
		signals = append(signals, Localize("accept blocked until evidence exists", "evidence 追加まで accept 不可"))
	} else if memoryReviewRequiresAcceptConfirmation(details) {
		signals = append(signals, Localize("accept requires confirmation", "accept には確認が必要"))
	}
	return strings.Join(signals, "; ")
}

func writeMemoryReviewDecisionCard(output io.Writer, details apptypes.MemoryDetails) error {
	summary := details.Summary()
	if _, err := fmt.Fprintf(
		output,
		"DECISION_CONTEXT:\nMEMORY_ID: %s\nTYPE: %s\nSCOPE: %s\nSTATUS: %s\nCONFIDENCE: %s\nSOURCE: %s\nREVIEW_STATUS: %s\nQUALITY_SIGNAL: %s\nSUPERSEDES: %s\nEXPIRES_AT: %s\nVALID_FROM: %s\nVALID_TO: %s\nCREATED_AT: %s\nUPDATED_AT: %s\nFACT:\n%s\n",
		summary.MemoryID(),
		summary.MemoryType(),
		formatMemoryScope(summary.Scope()),
		summary.Status(),
		summary.Confidence(),
		summary.Source(),
		memoryReviewDecisionStatus(details),
		memoryReviewQualitySignal(details),
		formatOptionalMemoryID(summary.Supersedes()),
		formatOptionalTime(summary.ExpiresAt()),
		formatTextTime(summary.ValidFrom()),
		formatOptionalTime(summary.ValidTo()),
		formatTextTime(summary.CreatedAt()),
		formatTextTime(summary.UpdatedAt()),
		summary.Fact(),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review decision context", "memory review decision context の出力に失敗しました"), err)
	}
	if err := writeMemoryReviewSourceContext(output, details); err != nil {
		return err
	}
	if err := writeEvidenceRefSection(output, details.EvidenceRefs()); err != nil {
		return err
	}
	if err := writeArtifactRefSection(output, details.ArtifactRefs()); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "\nACCEPT_GUIDANCE:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review guidance heading", "memory review guidance 見出しの出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, memoryReviewDecisionGuidance(details)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review guidance", "memory review guidance の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, "\nACCEPT_AS_IS_CHECKLIST:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print memory review checklist heading", "memory review checklist 見出しの出力に失敗しました"), err)
	}
	for _, item := range memoryReviewAcceptChecklist(details) {
		if _, err := fmt.Fprintln(output, "- "+item); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print memory review checklist item", "memory review checklist 項目の出力に失敗しました"), err)
		}
	}
	if _, err := fmt.Fprintln(output, "\nRELATED_MEMORY:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print related memory heading", "related memory 見出しの出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, "- "+memoryReviewDuplicateSupersedeHint(summary)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print related memory hint", "related memory hint の出力に失敗しました"), err)
	}
	return nil
}

func writeMemoryReviewSourceContext(output io.Writer, details apptypes.MemoryDetails) error {
	if _, err := fmt.Fprintln(output, "\nSOURCE_CONTEXT:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print source context heading", "source context 見出しの出力に失敗しました"), err)
	}
	sourceRefs := memoryReviewSourceContextRefs(details)
	if len(sourceRefs) == 0 {
		if _, err := fmt.Fprintln(output, "- "+Localize("no event/session evidence refs recorded", "event/session evidence ref は記録されていません")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty source context", "空の source context の出力に失敗しました"), err)
		}
		return nil
	}
	for _, ref := range sourceRefs {
		if _, err := fmt.Fprintln(output, "- "+ref); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print source context ref", "source context ref の出力に失敗しました"), err)
		}
	}
	return nil
}

func memoryReviewSourceContextRefs(details apptypes.MemoryDetails) []string {
	var refs []string
	for _, ref := range details.EvidenceRefs() {
		switch ref.Kind() {
		case domtypes.EvidenceRefKindEvent, domtypes.EvidenceRefKindSession:
			refs = append(refs, formatMemoryReviewRefLine(ref.Kind().String(), ref.Value()))
		}
	}
	return refs
}

func formatMemoryReviewRememberIntent(summary apptypes.MemorySummary) string {
	if summary.Source() == domtypes.MemorySourceRememberIntent {
		return Localize("yes (remember-intent)", "はい (remember-intent)")
	}
	return Localize("no", "いいえ")
}

func formatMemoryReviewCandidateAge(summary apptypes.MemorySummary, now time.Time) string {
	if summary.CreatedAt().IsZero() || now.Before(summary.CreatedAt()) {
		return Localize("unknown", "不明")
	}
	return formatDuration(now.Sub(summary.CreatedAt()))
}

func memoryReviewDuplicateSupersedeHint(summary apptypes.MemorySummary) string {
	if supersedes, ok := summary.Supersedes().Value(); ok {
		return Localizef("supersedes %s", "%s を supersede", supersedes)
	}
	return Localize("not checked in cockpit yet; use edit/distill or skip if duplicate risk is unclear", "cockpit では未チェック。重複リスクが不明なら edit/distill または skip")
}

func memoryReviewEvidencePreview(details apptypes.MemoryDetails) []string {
	lines := []string{}
	evidence := details.EvidenceRefs()
	if len(evidence) == 0 {
		lines = append(lines, Localize("• evidence: none recorded; accept as-is is unavailable until evidence exists", "• evidence: 記録なし。evidence が追加されるまで accept as-is は利用できません"))
	} else {
		lines = append(lines, Localizef("• evidence: %d ref(s)", "• evidence: %d 件", len(evidence)))
		for i, ref := range evidence {
			if i >= memoryReviewRefPreviewLimit {
				lines = append(lines, Localizef("  + %d more (press v to inspect)", "  + 他 %d 件 (v で確認)", len(evidence)-i))
				break
			}
			lines = append(lines, "  - "+formatMemoryReviewRefLine(ref.Kind().String(), ref.Value()))
		}
	}
	artifacts := details.ArtifactRefs()
	if len(artifacts) == 0 {
		lines = append(lines, Localize("• artifacts: none", "• artifacts: なし"))
	} else {
		lines = append(lines, Localizef("• artifacts: %d ref(s)", "• artifacts: %d 件", len(artifacts)))
		for i, ref := range artifacts {
			if i >= memoryReviewRefPreviewLimit {
				lines = append(lines, Localizef("  + %d more (press v to inspect)", "  + 他 %d 件 (v で確認)", len(artifacts)-i))
				break
			}
			lines = append(lines, "  - "+formatMemoryReviewRefLine(ref.Kind().String(), ref.Value()))
		}
	}
	return lines
}

func memoryReviewDecisionGuidance(details apptypes.MemoryDetails) string {
	if memoryReviewBlocksAccept(details) {
		return Localize(
			"GUIDANCE: accepted memory requires evidence; press r to attach evidence, or use the attach command shown in evidence view.",
			"判断ガイド: accepted memory には evidence が必要です。r で evidence を追加するか、evidence 画面に表示される attach command を使ってください。",
		)
	}
	if memoryReviewRequiresAcceptConfirmation(details) {
		return Localize(
			"GUIDANCE: prefer edit/distill or skip until evidence, wording, and scope are clear; accept as-is requires a second confirmation.",
			"判断ガイド: evidence・文言・scope が明確になるまでは edit/distill または skip を優先。accept as-is には再確認が必要です。",
		)
	}
	return Localize(
		"GUIDANCE: accept as-is only if the evidence supports the exact wording; use edit/distill to tighten ambiguous wording.",
		"判断ガイド: evidence が文言をそのまま支える場合だけ accept as-is。曖昧なら edit/distill で整えます。",
	)
}

func memoryReviewAttachGuidance(details apptypes.MemoryDetails) string {
	id := details.Summary().MemoryID().String()
	return Localizef(
		"ATTACH PATH: press r to attach evidence in this review, or run `traceary memory inbox attach %s --evidence event:<event-id>` in another shell and reopen review.",
		"ATTACH PATH: この review 内では r で evidence を追加できます。別 shell では `traceary memory inbox attach %s --evidence event:<event-id>` を実行してから review を開き直してください。",
		id,
	)
}

func formatMemoryReviewRefLine(kind string, value string) string {
	return fmt.Sprintf("%s:%s", kind, sanitizeMemoryReviewRefValue(value))
}

func sanitizeMemoryReviewRefValue(value string) string {
	value = strings.ToValidUTF8(value, "�")
	var b strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == '\u2028', r == '\u2029':
			if b.Len() > 0 {
				b.WriteRune(' ')
			}
			continue
		default:
			b.WriteRune(r)
		}
	}
	safe := strings.Join(strings.Fields(b.String()), " ")
	runes := []rune(safe)
	if len(runes) <= memoryReviewRefDisplayMaxRunes {
		return safe
	}
	return string(runes[:memoryReviewRefDisplayMaxRunes-len([]rune(memoryReviewTruncatedRefSuffix))]) + memoryReviewTruncatedRefSuffix
}

func memoryDetailsWithRefs(details apptypes.MemoryDetails, evidenceRefs []domtypes.EvidenceRef, artifactRefs []domtypes.ArtifactRef) apptypes.MemoryDetails {
	return apptypes.MemoryDetailsOf(
		details.Summary(),
		domtypes.AppendEvidenceRefs(details.EvidenceRefs(), evidenceRefs),
		domtypes.AppendArtifactRefs(details.ArtifactRefs(), artifactRefs),
	)
}

func memoryReviewAcceptChecklist(details apptypes.MemoryDetails) []string {
	checks := []string{
		Localize("factual and stable", "事実で安定している"),
		Localize("useful for future sessions", "将来の session で有用"),
		Localize("scope/type are correct", "scope/type が正しい"),
	}
	if len(details.EvidenceRefs()) > 0 {
		checks = append(checks, Localize("supported by evidence refs", "evidence refs に支えられている"))
	} else {
		checks = append(checks, Localize("evidence missing; accept as-is is unavailable", "evidence がないため accept as-is は利用できません"))
	}
	checks = append(checks, Localize("not duplicate, stale, or superseded", "重複・古い・supersede 済みではない"))
	return checks
}

func memoryReviewBrowseHelp(details apptypes.MemoryDetails) string {
	if memoryReviewBlocksAccept(details) {
		return Localize(
			"a unavailable (evidence required) · r attach evidence · x reject · s skip · v evidence · ↑/↓ navigate · ? help · q quit",
			"a 不可 (evidence 必須) · r evidence 追加 · x reject · s skip · v evidence · ↑/↓ 移動 · ? ヘルプ · q 終了",
		)
	}
	return Localize(
		"a accept as-is · x reject · s skip · e edit/distill · r attach evidence · v evidence · ↑/↓ navigate · ? help · q quit",
		"a accept as-is · x reject · s skip · e edit/distill · r evidence 追加 · v evidence · ↑/↓ 移動 · ? ヘルプ · q 終了",
	)
}

// inboxReviewIO resolves the stdin/stdout pair the review TUI should drive.
// Tests pass a non-file writer (e.g. *bytes.Buffer) into cobra, which
// makes the type assertion fail and `tui.Interactive` then refuses the
// run — exactly the behavior the non-TTY contract requires.
func inboxReviewIO(output io.Writer) (*os.File, *os.File) {
	stdout, _ := output.(*os.File)
	return os.Stdin, stdout
}
