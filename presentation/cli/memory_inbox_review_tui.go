package cli

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

func defaultReviewActionKeys() reviewActionKeys {
	return reviewActionKeys{
		Accept:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "accept")),
		Reject:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "reject")),
		Skip:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "skip")),
		Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit/distill")),
		Attach:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "attach evidence")),
		View:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "view evidence")),
		Confirm: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
		Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
}

func newReviewModel(items []apptypes.MemoryDetails, keys tui.KeyMap, styles tui.Styles) reviewModel {
	return reviewModel{
		keys:     keys,
		styles:   styles,
		items:    items,
		reviewed: make([]string, len(items)),
		mode:     reviewModeBrowse,
	}
}

// Init is the Bubble Tea lifecycle hook; the review screen does not need
// a startup command because the queue is loaded synchronously before the
// program starts.
func (m reviewModel) Init() tea.Cmd { return nil }

// Update is the testable seam: tests drive concrete tea.KeyMsg values and
// inspect the returned model state without going through a Program.
func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.mode == reviewModeEdit {
		return m.updateEdit(keyMsg)
	}
	if m.mode == reviewModeAttach {
		return m.updateAttach(keyMsg)
	}
	return m.updateBrowse(keyMsg)
}

// updateBrowse handles keys outside of edit mode. Quit, help toggle, and
// view toggle work from any non-edit mode; navigation and action keys
// (accept / reject / skip / edit) only fire in browse mode so a stray
// rune from the help or evidence modal cannot queue a destructive
// decision against the underlying candidate.
func (m reviewModel) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := defaultReviewActionKeys()
	// Esc has mode-specific semantics. The shared Quit binding maps esc
	// to quit, but inside the help / evidence overlays the operator
	// expects esc to dismiss the overlay (mirroring the ? / v toggles).
	// Handle that override before falling through to Quit so esc only
	// quits while the operator is actually on the browse screen.
	if key.Matches(msg, actions.Cancel) && (m.mode == reviewModeHelp || m.mode == reviewModeViewEvidence) {
		m.mode = reviewModeBrowse
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		if m.mode == reviewModeHelp {
			m.mode = reviewModeBrowse
		} else {
			m.mode = reviewModeHelp
		}
		return m, nil
	case key.Matches(msg, actions.View):
		if len(m.items) == 0 {
			return m, nil
		}
		if m.mode == reviewModeViewEvidence {
			m.mode = reviewModeBrowse
		} else {
			m.mode = reviewModeViewEvidence
		}
		return m, nil
	}
	if m.mode != reviewModeBrowse {
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Up):
		if len(m.items) > 0 && m.cursor > 0 {
			m.cursor--
		}
		m.statusMsg = ""
		m.acceptConfirmID = ""
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.cursor+1 < len(m.items) {
			m.cursor++
		}
		m.statusMsg = ""
		m.acceptConfirmID = ""
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.cursor = 0
		m.statusMsg = ""
		m.acceptConfirmID = ""
		return m, nil
	case key.Matches(msg, m.keys.End):
		if len(m.items) > 0 {
			m.cursor = len(m.items) - 1
		}
		m.statusMsg = ""
		m.acceptConfirmID = ""
		return m, nil
	case key.Matches(msg, actions.Accept):
		if m.currentCandidateBlocksAccept() {
			m.acceptConfirmID = ""
			m.statusMsg = Localize(
				"accept as-is is unavailable because accepted memory requires evidence; press r to attach evidence first",
				"accepted memory には evidence が必要なため accept as-is は利用できません。先に r で evidence を追加してください",
			)
			return m, nil
		}
		if m.currentCandidateNeedsAcceptConfirmation() && !m.acceptConfirmationMatchesCurrent() {
			m.acceptConfirmID = m.items[m.cursor].Summary().MemoryID()
			m.statusMsg = Localize(
				"accept as-is needs confirmation for this weak candidate; press a again only if the checklist passes, or use e to edit/distill",
				"この弱いメモリ候補を accept as-is するには確認が必要です。checklist を満たす場合だけ a を再入力し、不明なら e で edit/distill してください",
			)
			return m, nil
		}
		return m.queueDecision(reviewDecisionAccept, "")
	case key.Matches(msg, actions.Reject):
		return m.queueDecision(reviewDecisionReject, "")
	case key.Matches(msg, actions.Skip):
		if len(m.items) == 0 {
			return m, nil
		}
		idx := m.cursor
		if idx >= 0 && idx < len(m.items) {
			m.removeTerminalDecisionFor(m.items[idx].Summary().MemoryID())
			if m.hasAttachDecisionFor(m.items[idx].Summary().MemoryID()) {
				m.reviewed[idx] = decisionLabel(reviewDecisionAttach)
			} else {
				m.reviewed[idx] = ""
			}
		}
		m.statusMsg = Localize("skipped", "skip しました")
		m.acceptConfirmID = ""
		m.advanceCursor()
		return m, nil
	case key.Matches(msg, actions.Edit):
		if len(m.items) == 0 {
			return m, nil
		}
		if m.currentCandidateBlocksAccept() {
			m.statusMsg = Localize(
				"edit/distill is unavailable because the source memory candidate has no evidence to preserve; press r to attach evidence first",
				"source メモリ候補に引き継ぐ evidence がないため edit/distill は利用できません。先に r で evidence を追加してください",
			)
			m.acceptConfirmID = ""
			return m, nil
		}
		m.mode = reviewModeEdit
		m.editIndex = m.cursor
		m.editBuffer = ""
		m.statusMsg = ""
		m.acceptConfirmID = ""
		return m, nil
	case key.Matches(msg, actions.Attach):
		if len(m.items) == 0 {
			return m, nil
		}
		m.mode = reviewModeAttach
		m.attachIndex = m.cursor
		m.attachBuffer = ""
		m.statusMsg = Localize(
			"enter refs as kind:value; comma-separated refs and artifact:kind:value are supported",
			"kind:value 形式で refs を入力してください。カンマ区切りと artifact:kind:value も使えます",
		)
		m.acceptConfirmID = ""
		return m, nil
	}
	return m, nil
}

// queueDecision records a terminal decision for the current item, marks the row
// reviewed, and advances the cursor to the next untouched candidate. Attach
// decisions are preserved for accept/distill so same-session attach -> accept
// and attach -> distill apply in operator order; reject removes prior attach
// decisions because the candidate is being discarded.
func (m reviewModel) queueDecision(kind reviewDecisionKind, fact string) (tea.Model, tea.Cmd) {
	if len(m.items) == 0 {
		return m, nil
	}
	idx := m.cursor
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	memoryID := m.items[idx].Summary().MemoryID()
	if kind == reviewDecisionReject {
		m.removeDecisionFor(memoryID)
	} else {
		m.removeTerminalDecisionFor(memoryID)
	}
	m.decisions = append(m.decisions, reviewDecision{kind: kind, memoryID: memoryID, fact: fact})
	m.reviewed[idx] = decisionLabel(kind)
	m.statusMsg = decisionLabel(kind)
	m.acceptConfirmID = ""
	m.advanceCursor()
	return m, nil
}

func (m reviewModel) queueAttachDecision(evidenceRefs []domtypes.EvidenceRef, artifactRefs []domtypes.ArtifactRef) (tea.Model, tea.Cmd) {
	if len(m.items) == 0 {
		return m, nil
	}
	idx := m.cursor
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	memoryID := m.items[idx].Summary().MemoryID()
	m.decisions = append(m.decisions, reviewDecision{
		kind:         reviewDecisionAttach,
		memoryID:     memoryID,
		evidenceRefs: append([]domtypes.EvidenceRef(nil), evidenceRefs...),
		artifactRefs: append([]domtypes.ArtifactRef(nil), artifactRefs...),
	})
	m.items[idx] = memoryDetailsWithRefs(m.items[idx], evidenceRefs, artifactRefs)
	m.reviewed[idx] = decisionLabel(reviewDecisionAttach)
	if len(artifactRefs) > 0 {
		m.statusMsg = Localize("evidence/artifact attach queued; accept/edit is now available for this candidate", "evidence/artifact 追加を保留しました。この候補は accept/edit できるようになりました")
	} else {
		m.statusMsg = Localize("evidence attach queued; accept/edit is now available for this candidate", "evidence 追加を保留しました。この候補は accept/edit できるようになりました")
	}
	m.acceptConfirmID = ""
	return m, nil
}

func (m *reviewModel) removeDecisionFor(memoryID domtypes.MemoryID) {
	filtered := m.decisions[:0]
	for _, decision := range m.decisions {
		if decision.memoryID == memoryID {
			continue
		}
		filtered = append(filtered, decision)
	}
	m.decisions = filtered
}

func (m *reviewModel) removeTerminalDecisionFor(memoryID domtypes.MemoryID) {
	filtered := m.decisions[:0]
	for _, decision := range m.decisions {
		if decision.memoryID == memoryID && decision.kind != reviewDecisionAttach {
			continue
		}
		filtered = append(filtered, decision)
	}
	m.decisions = filtered
}

func (m reviewModel) hasAttachDecisionFor(memoryID domtypes.MemoryID) bool {
	for _, decision := range m.decisions {
		if decision.memoryID == memoryID && decision.kind == reviewDecisionAttach {
			return true
		}
	}
	return false
}

func decisionLabel(kind reviewDecisionKind) string {
	switch kind {
	case reviewDecisionAccept:
		return "accept"
	case reviewDecisionReject:
		return "reject"
	case reviewDecisionDistill:
		return "distill"
	case reviewDecisionAttach:
		return "attach"
	}
	return ""
}

// advanceCursor walks past the just-decided row. We deliberately stop at
// the end (no wrap) so the operator notices when the queue is exhausted
// instead of cycling silently.
func (m *reviewModel) advanceCursor() {
	if m.cursor+1 < len(m.items) {
		m.cursor++
	}
}

// updateEdit handles keys while the operator is typing a distilled fact.
// The buffer accepts printable runes and backspace; Enter commits the
// edit (only when the buffer is non-empty so the model never queues an
// empty fact), and Esc cancels back to browse.
func (m reviewModel) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := defaultReviewActionKeys()
	switch {
	case key.Matches(msg, actions.Cancel):
		m.mode = reviewModeBrowse
		m.editBuffer = ""
		m.statusMsg = Localize("edit cancelled", "edit をキャンセルしました")
		return m, nil
	case key.Matches(msg, actions.Confirm):
		fact := strings.TrimSpace(m.editBuffer)
		if fact == "" {
			m.statusMsg = Localize("edit requires an operator-authored fact (no auto-accept of LLM output)", "edit には operator が書き起こした fact が必要です (LLM 出力の自動採用は行いません)")
			return m, nil
		}
		// Restore the cursor to the row we entered edit mode on so that
		// the queued decision targets the right id even if the operator
		// somehow moved during edit (defensive: edit mode swallows arrow
		// keys, but the invariant is cheap to enforce).
		m.cursor = m.editIndex
		m.mode = reviewModeBrowse
		m.editBuffer = ""
		return m.queueDecision(reviewDecisionDistill, fact)
	case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH:
		runes := []rune(m.editBuffer)
		if len(runes) > 0 {
			m.editBuffer = string(runes[:len(runes)-1])
		}
		return m, nil
	case msg.Type == tea.KeySpace:
		m.editBuffer += " "
		return m, nil
	case msg.Type == tea.KeyRunes:
		m.editBuffer += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func (m reviewModel) updateAttach(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := defaultReviewActionKeys()
	switch {
	case key.Matches(msg, actions.Cancel):
		m.mode = reviewModeBrowse
		m.attachBuffer = ""
		m.statusMsg = Localize("evidence attach cancelled", "evidence 追加をキャンセルしました")
		return m, nil
	case key.Matches(msg, actions.Confirm):
		rawRefs := strings.TrimSpace(m.attachBuffer)
		if rawRefs == "" {
			m.statusMsg = Localize("attach requires at least one evidence ref", "evidence 追加には evidence ref が1つ以上必要です")
			return m, nil
		}
		evidenceRefs, artifactRefs, err := parseReviewAttachRefs(rawRefs)
		if err != nil {
			m.statusMsg = err.Error()
			return m, nil
		}
		if m.attachIndex < 0 || m.attachIndex >= len(m.items) {
			m.mode = reviewModeBrowse
			m.attachBuffer = ""
			m.statusMsg = Localize("attach target is no longer available", "追加対象のメモリ候補が見つかりません")
			return m, nil
		}
		if len(evidenceRefs) == 0 && len(m.items[m.attachIndex].EvidenceRefs()) == 0 {
			m.statusMsg = Localize("attach requires at least one evidence ref; artifact refs are optional", "evidence 追加には evidence ref が1つ以上必要です。artifact ref は任意です")
			return m, nil
		}
		m.cursor = m.attachIndex
		m.mode = reviewModeBrowse
		m.attachBuffer = ""
		return m.queueAttachDecision(evidenceRefs, artifactRefs)
	case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH:
		runes := []rune(m.attachBuffer)
		if len(runes) > 0 {
			m.attachBuffer = string(runes[:len(runes)-1])
		}
		return m, nil
	case msg.Type == tea.KeySpace:
		m.attachBuffer += " "
		return m, nil
	case msg.Type == tea.KeyRunes:
		m.attachBuffer += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func parseReviewAttachRefs(input string) ([]domtypes.EvidenceRef, []domtypes.ArtifactRef, error) {
	tokens := splitReviewAttachTokens(input)
	evidenceRefs := make([]domtypes.EvidenceRef, 0, len(tokens))
	artifactRefs := make([]domtypes.ArtifactRef, 0)
	for _, token := range tokens {
		kind, rawValue, err := parseKindValueToken(token)
		if err != nil {
			return nil, nil, err
		}
		switch kind {
		case "evidence":
			refs, err := parseEvidenceRefs([]string{rawValue})
			if err != nil {
				return nil, nil, err
			}
			evidenceRefs = append(evidenceRefs, refs...)
		case "artifact":
			refs, err := parseArtifactRefs([]string{rawValue})
			if err != nil {
				return nil, nil, err
			}
			artifactRefs = append(artifactRefs, refs...)
		default:
			refs, err := parseEvidenceRefs([]string{token})
			if err != nil {
				return nil, nil, err
			}
			evidenceRefs = append(evidenceRefs, refs...)
		}
	}
	return evidenceRefs, artifactRefs, nil
}

func splitReviewAttachTokens(input string) []string {
	tokens := []string{}
	start := 0
	for i, r := range input {
		if r != ',' || !looksLikeReviewAttachToken(input[i+1:]) {
			continue
		}
		if token := strings.TrimSpace(input[start:i]); token != "" {
			tokens = append(tokens, token)
		}
		start = i + 1
	}
	if token := strings.TrimSpace(input[start:]); token != "" {
		tokens = append(tokens, token)
	}
	return tokens
}

func looksLikeReviewAttachToken(input string) bool {
	trimmed := strings.TrimLeftFunc(input, unicode.IsSpace)
	kind, _, err := parseKindValueToken(trimmed)
	if err != nil {
		return false
	}
	if kind == "evidence" || kind == "artifact" {
		return true
	}
	_, err = domtypes.EvidenceRefKindFrom(kind)
	return err == nil
}

// View renders the current screen. The output is intentionally simple:
// the contract is "operator-readable" rather than "pixel-perfect"; rich
// layouts can land in a later refinement once the workflow is exercised
// in dogfood.
