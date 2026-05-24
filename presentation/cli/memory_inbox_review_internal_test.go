package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

// buildReviewCandidate constructs a candidate MemoryDetails the model can
// drive. Tests stay focused on the model/update seam, so the helper keeps
// the field set minimal but realistic (workspace scope + one evidence ref
// so the evidence-mode renderer has something to print).
func buildReviewCandidate(t *testing.T, id string, fact string) apptypes.MemoryDetails {
	t.Helper()
	return buildReviewCandidateWithOptions(t, reviewCandidateOptions{id: id, fact: fact})
}

type reviewCandidateOptions struct {
	id            string
	fact          string
	confidence    domtypes.Confidence
	source        domtypes.MemorySource
	supersedes    domtypes.Optional[domtypes.MemoryID]
	noEvidence    bool
	evidenceValue string
}

func buildReviewCandidateWithOptions(t *testing.T, opts reviewCandidateOptions) apptypes.MemoryDetails {
	t.Helper()
	if opts.confidence == "" {
		opts.confidence = domtypes.ConfidenceMedium
	}
	if opts.source == "" {
		opts.source = domtypes.MemorySourceManual
	}
	if opts.fact == "" {
		opts.fact = "fact"
	}
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(opts.id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		opts.fact,
		domtypes.MemoryStatusCandidate,
		opts.confidence,
		opts.source,
		opts.supersedes,
		domtypes.None[time.Time](),
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	if opts.noEvidence {
		return apptypes.MemoryDetailsOf(summary, nil, nil)
	}
	evidenceValue := opts.evidenceValue
	if evidenceValue == "" {
		evidenceValue = "/tmp/MEMORY.md#L1-L2"
	}
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, evidenceValue)
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{evidence}, nil)
}

func newReviewTestModel(items ...apptypes.MemoryDetails) reviewModel {
	return newReviewModel(items, tui.DefaultKeyMap(), tui.DefaultStyles())
}

func mustReviewEvidenceRef(t *testing.T, kind domtypes.EvidenceRefKind, value string) domtypes.EvidenceRef {
	t.Helper()
	ref, err := domtypes.EvidenceRefFrom(kind, value)
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	return ref
}

// TestReviewModel_AcceptQueuesDecisionAndAdvances pins that pressing the
// accept binding queues an Accept decision and moves the cursor to the
// next item without calling any usecase. The runner — not the model —
// is responsible for executing decisions.
func TestReviewModel_AcceptQueuesDecisionAndAdvances(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := updated.(reviewModel)

	decisions := got.Decisions()
	if len(decisions) != 1 {
		t.Fatalf("decisions length = %d, want 1", len(decisions))
	}
	if decisions[0].kind != reviewDecisionAccept {
		t.Fatalf("decisions[0].kind = %v, want accept", decisions[0].kind)
	}
	if got := decisions[0].memoryID.String(); got != "id-1" {
		t.Fatalf("decisions[0].memoryID = %q, want id-1", got)
	}
	if got.cursor != 1 {
		t.Fatalf("cursor = %d, want advanced to 1", got.cursor)
	}
}

func TestReviewModel_DecisionCardShowsAcceptEvidenceContext(t *testing.T) {
	previousTopNow := topNowFunc
	topNowFunc = func() time.Time { return time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { topNowFunc = previousTopNow })

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "id-context",
		fact:       "fact with enough context",
		confidence: domtypes.ConfidenceHigh,
		source:     domtypes.MemorySourceRememberIntent,
		supersedes: domtypes.Some(domtypes.MemoryID("old-memory")),
	}))

	view := model.View()
	for _, must := range []string{
		"memory review · decision card",
		"DECISION CONTEXT",
		"MEMORY_ID:              id-context",
		"TYPE:                   preference",
		"SCOPE:                  workspace:github.com/example/repo",
		"SOURCE:                 remember-intent",
		"CONFIDENCE:             high",
		"QUALITY_SIGNAL:         strong confidence; explicit remember intent",
		"REMEMBERED_BY_OPERATOR: yes (remember-intent)",
		"EVIDENCE_REFS:          1 (press v to inspect)",
		"CREATED_AT:             2026-05-07T00:00:00Z",
		"MEMORY_CANDIDATE_AGE:   48h0m",
		"DUPLICATE_SUPERSEDE:    supersedes old-memory",
		"EVIDENCE-FIRST REVIEW",
		"• evidence: 1 ref(s)",
		"file:/tmp/MEMORY.md#L1-L2",
		"GUIDANCE: accept as-is only if the evidence supports the exact wording",
		"ACCEPT AS-IS CHECKLIST",
		"factual and stable",
		"not duplicate, stale, or superseded",
		"a accept as-is",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("decision card missing %q:\n%s", must, view)
		}
	}
}

func TestReviewModel_NoEvidenceCandidateBlocksAcceptAsIs(t *testing.T) {
	t.Parallel()

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "id-no-evidence",
		fact:       "fact without evidence",
		confidence: domtypes.ConfidenceHigh,
		source:     domtypes.MemorySourceManual,
		noEvidence: true,
	}))

	first, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	firstM := first.(reviewModel)
	if len(firstM.Decisions()) != 0 {
		t.Fatalf("first accept without evidence must not queue decisions, got %+v", firstM.Decisions())
	}
	if firstM.acceptConfirmationMatchesCurrent() {
		t.Fatalf("first accept without evidence must not arm accept confirmation")
	}
	view := firstM.View()
	for _, must := range []string{
		"evidence: none recorded; accept as-is is unavailable",
		"accept as-is unavailable",
		"accepted memory requires evidence",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("no-evidence accept guard missing %q:\n%s", must, view)
		}
	}
	second, _ := firstM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	secondM := second.(reviewModel)
	if len(secondM.Decisions()) != 0 || secondM.acceptConfirmationMatchesCurrent() {
		t.Fatalf("second accept without evidence must remain blocked, decisions=%+v confirm=%q", secondM.Decisions(), secondM.acceptConfirmID)
	}
	edit, _ := secondM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	editM := edit.(reviewModel)
	if editM.mode != reviewModeBrowse || len(editM.Decisions()) != 0 {
		t.Fatalf("edit/distill without evidence must stay in browse without queuing decisions, mode=%v decisions=%+v", editM.mode, editM.Decisions())
	}
	if !strings.Contains(editM.statusMsg, "edit/distill is unavailable") {
		t.Fatalf("edit/distill block status = %q", editM.statusMsg)
	}
}

func TestReviewModel_AttachEvidenceThenAcceptQueuesOrderedDecisions(t *testing.T) {
	t.Parallel()

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "id-attach",
		fact:       "fact without evidence",
		confidence: domtypes.ConfidenceHigh,
		source:     domtypes.MemorySourceManual,
		noEvidence: true,
	}))

	opened, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	attachM := opened.(reviewModel)
	if attachM.mode != reviewModeAttach {
		t.Fatalf("r should open attach mode, got %v", attachM.mode)
	}
	for _, r := range "event:evt-1, artifact:pr:#1074" {
		updated, _ := attachM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		attachM = updated.(reviewModel)
	}
	queued, _ := attachM.Update(tea.KeyMsg{Type: tea.KeyEnter})
	queuedM := queued.(reviewModel)
	if queuedM.mode != reviewModeBrowse {
		t.Fatalf("enter should return to browse, got %v", queuedM.mode)
	}
	if queuedM.currentCandidateBlocksAccept() {
		t.Fatalf("attached evidence should make accept available")
	}

	accepted, _ := queuedM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	acceptedM := accepted.(reviewModel)
	decisions := acceptedM.Decisions()
	if len(decisions) != 2 {
		t.Fatalf("decisions len = %d, want attach+accept: %+v", len(decisions), decisions)
	}
	if decisions[0].kind != reviewDecisionAttach || decisions[1].kind != reviewDecisionAccept {
		t.Fatalf("decisions order = %+v, want attach then accept", decisions)
	}
	if len(decisions[0].evidenceRefs) != 1 || decisions[0].evidenceRefs[0].Value() != "evt-1" {
		t.Fatalf("attach evidence refs = %+v", decisions[0].evidenceRefs)
	}
	if len(decisions[0].artifactRefs) != 1 || decisions[0].artifactRefs[0].Value() != "#1074" {
		t.Fatalf("attach artifact refs = %+v", decisions[0].artifactRefs)
	}
}

func TestReviewModel_AttachEvidenceUnlocksGeneratedCandidateAcceptGate(t *testing.T) {
	t.Parallel()

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "id-generated",
		fact:       "generated fact without evidence",
		confidence: domtypes.ConfidenceLow,
		source:     domtypes.MemorySourceExtractedHidden,
		noEvidence: true,
	}))

	opened, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	attachM := opened.(reviewModel)
	for _, r := range "event:evt-generated" {
		updated, _ := attachM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		attachM = updated.(reviewModel)
	}
	queued, _ := attachM.Update(tea.KeyMsg{Type: tea.KeyEnter})
	queuedM := queued.(reviewModel)

	if queuedM.currentCandidateBlocksAccept() {
		t.Fatalf("attached evidence should clear the evidence-only accept block")
	}
	if !memoryReviewRequiresAcceptConfirmation(queuedM.items[queuedM.cursor]) {
		t.Fatalf("generated low-confidence candidate should still require accept confirmation")
	}
}

func TestReviewModel_AttachArtifactOnlyRequiresEvidence(t *testing.T) {
	t.Parallel()

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "id-artifact-only",
		fact:       "fact without evidence",
		noEvidence: true,
	}))

	opened, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	attachM := opened.(reviewModel)
	for _, r := range "artifact:pr:#1074" {
		updated, _ := attachM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		attachM = updated.(reviewModel)
	}
	submitted, _ := attachM.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := submitted.(reviewModel)

	if got.mode != reviewModeAttach {
		t.Fatalf("artifact-only attach should stay in attach mode, got %v", got.mode)
	}
	if len(got.Decisions()) != 0 {
		t.Fatalf("artifact-only attach must not queue decisions: %+v", got.Decisions())
	}
	if !strings.Contains(got.statusMsg, "at least one evidence ref") {
		t.Fatalf("artifact-only attach status = %q", got.statusMsg)
	}
}

func TestFormatMemoryReviewRefLineSanitizesAndTruncates(t *testing.T) {
	t.Parallel()

	line := formatMemoryReviewRefLine("file", "path\n\x1b[31m\u009b32m\u202e\u2066\u200b"+strings.Repeat("x", memoryReviewRefDisplayMaxRunes+20))
	for _, forbidden := range []string{"\n", "\x1b", "\u009b", "\u202e", "\u2066", "\u200b", "\r", "\t"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("ref line contains control sequence %q: %q", forbidden, line)
		}
	}
	if !strings.HasSuffix(line, memoryReviewTruncatedRefSuffix) {
		t.Fatalf("ref line should be truncated with suffix: %q", line)
	}
	value := strings.TrimPrefix(line, "file:")
	if got := len([]rune(value)); got != memoryReviewRefDisplayMaxRunes {
		t.Fatalf("sanitized ref value length = %d, want %d: %q", got, memoryReviewRefDisplayMaxRunes, value)
	}
}

func TestReviewModel_WeakCandidateRequiresDoubleAcceptAsIs(t *testing.T) {
	t.Parallel()

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "id-weak",
		fact:       "ambiguous extracted fact",
		confidence: domtypes.ConfidenceLow,
		source:     domtypes.MemorySourceExtractedHidden,
	}))

	first, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	firstM := first.(reviewModel)
	if len(firstM.Decisions()) != 0 {
		t.Fatalf("first accept on weak candidate must not queue decisions, got %+v", firstM.Decisions())
	}
	if firstM.cursor != 0 {
		t.Fatalf("first accept cursor = %d, want stay on weak candidate", firstM.cursor)
	}
	if !firstM.acceptConfirmationMatchesCurrent() {
		t.Fatalf("first accept should arm accept confirmation")
	}
	if !strings.Contains(firstM.statusMsg, "needs confirmation") {
		t.Fatalf("first accept status = %q, want confirmation guidance", firstM.statusMsg)
	}
	if view := firstM.View(); !strings.Contains(view, "accept confirmation armed") || !strings.Contains(view, "source=extracted-hidden") {
		t.Fatalf("weak candidate view missing confirmation/risk context:\n%s", view)
	}

	second, _ := firstM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	secondM := second.(reviewModel)
	decisions := secondM.Decisions()
	if len(decisions) != 1 || decisions[0].kind != reviewDecisionAccept {
		t.Fatalf("second accept should queue accept, got %+v", decisions)
	}
	if secondM.acceptConfirmID != "" {
		t.Fatalf("accept confirmation should clear after decision, got %q", secondM.acceptConfirmID)
	}
}

func TestReviewModel_RejectQueuesDecisionAndAdvances(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(reviewModel)

	decisions := got.Decisions()
	if len(decisions) != 1 || decisions[0].kind != reviewDecisionReject {
		t.Fatalf("expected one reject decision, got %+v", decisions)
	}
	if got.cursor != 1 {
		t.Fatalf("cursor = %d, want advanced to 1", got.cursor)
	}
}

// TestReviewModel_SkipDoesNotQueue confirms skip advances the cursor but
// records nothing — the runner has no work to apply for skipped rows.
func TestReviewModel_SkipDoesNotQueue(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	got := updated.(reviewModel)
	if len(got.Decisions()) != 0 {
		t.Fatalf("skip should not queue a decision, got %+v", got.Decisions())
	}
	if got.cursor != 1 {
		t.Fatalf("cursor = %d, want advanced to 1 after skip", got.cursor)
	}
}

// TestReviewModel_SkipClearsPriorDecision pins that revisiting a row that
// already has a queued accept/reject and pressing skip discards the prior
// decision instead of leaving it queued. Without this guard, the runner
// would still apply the previous accept/reject after quit even though the
// operator's last word for the row was "skip".
func TestReviewModel_SkipClearsPriorDecision(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)

	// accept id-1, then go back and skip it.
	step1, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	step2, _ := step1.(reviewModel).Update(tea.KeyMsg{Type: tea.KeyUp})
	step3, _ := step2.(reviewModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	got := step3.(reviewModel)

	if len(got.Decisions()) != 0 {
		t.Fatalf("skip after accept must drop the queued decision; got %+v", got.Decisions())
	}
	if got.reviewed[0] != "" {
		t.Fatalf("skip must clear reviewed marker for current row; got %q", got.reviewed[0])
	}
	if got.cursor != 1 {
		t.Fatalf("cursor = %d, want advanced to 1 after skip", got.cursor)
	}
}

// TestReviewModel_ActionKeysIgnoredInHelpMode pins that accept / reject /
// skip / edit are inert while the help modal is open. Without this guard,
// pressing 'a' while reading help would queue a destructive accept on the
// row underneath the modal.
func TestReviewModel_ActionKeysIgnoredInHelpMode(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)
	helpOn, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	helpM := helpOn.(reviewModel)
	if helpM.mode != reviewModeHelp {
		t.Fatalf("expected help mode, got %v", helpM.mode)
	}

	for _, action := range []rune{'a', 'x', 's', 'e'} {
		next, _ := helpM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{action}})
		nextM := next.(reviewModel)
		if len(nextM.Decisions()) != 0 {
			t.Fatalf("%q in help mode must not queue a decision; got %+v", action, nextM.Decisions())
		}
		if nextM.mode != reviewModeHelp {
			t.Fatalf("%q in help mode must not change mode; got %v", action, nextM.mode)
		}
		if nextM.cursor != 0 {
			t.Fatalf("%q in help mode must not advance cursor; got %d", action, nextM.cursor)
		}
		if nextM.reviewed[0] != "" {
			t.Fatalf("%q in help mode must not mark row reviewed; got %q", action, nextM.reviewed[0])
		}
	}
}

// TestReviewModel_ActionKeysIgnoredInEvidenceMode pins the same modal
// guard for the evidence overlay. The evidence view is read-only; an
// accidental rune press while reading evidence must not queue a decision
// against the candidate behind it.
func TestReviewModel_ActionKeysIgnoredInEvidenceMode(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)
	evOn, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	evM := evOn.(reviewModel)
	if evM.mode != reviewModeViewEvidence {
		t.Fatalf("expected evidence mode, got %v", evM.mode)
	}

	for _, action := range []rune{'a', 'x', 's', 'e'} {
		next, _ := evM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{action}})
		nextM := next.(reviewModel)
		if len(nextM.Decisions()) != 0 {
			t.Fatalf("%q in evidence mode must not queue a decision; got %+v", action, nextM.Decisions())
		}
		if nextM.mode != reviewModeViewEvidence {
			t.Fatalf("%q in evidence mode must not change mode; got %v", action, nextM.mode)
		}
		if nextM.cursor != 0 {
			t.Fatalf("%q in evidence mode must not advance cursor; got %d", action, nextM.cursor)
		}
		if nextM.reviewed[0] != "" {
			t.Fatalf("%q in evidence mode must not mark row reviewed; got %q", action, nextM.reviewed[0])
		}
	}
}

// TestReviewModel_RequeueReplacesPriorDecision pins that re-deciding on
// the same id replaces the prior decision so the operator's last word
// wins. Without this guard, accidentally tapping `a` twice would queue
// duplicate Accept calls and the runner would log a spurious failure on
// the second pass.
func TestReviewModel_RequeueReplacesPriorDecision(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
		buildReviewCandidate(t, "id-2", "fact two"),
	)

	// accept id-1, then go back and reject it.
	step1, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	step1m := step1.(reviewModel)
	step2, _ := step1m.Update(tea.KeyMsg{Type: tea.KeyUp})
	step2m := step2.(reviewModel)
	step3, _ := step2m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := step3.(reviewModel)

	decisions := got.Decisions()
	if len(decisions) != 1 {
		t.Fatalf("decisions length = %d, want 1 after replace", len(decisions))
	}
	if decisions[0].kind != reviewDecisionReject || decisions[0].memoryID.String() != "id-1" {
		t.Fatalf("decisions[0] = %+v, want reject of id-1", decisions[0])
	}
}

// TestReviewModel_EditRequiresOperatorAuthoredFact pins the central
// safety property of #925: edit/distill never auto-accepts the
// candidate's fact. An empty buffer must not queue a distill decision,
// and only after the operator types a non-empty fact does the model
// queue the Distill operation.
func TestReviewModel_EditRequiresOperatorAuthoredFact(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact one"),
	)

	editing, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	editingM := editing.(reviewModel)
	if editingM.mode != reviewModeEdit {
		t.Fatalf("expected mode=edit, got %v", editingM.mode)
	}

	emptySubmit, _ := editingM.Update(tea.KeyMsg{Type: tea.KeyEnter})
	emptyM := emptySubmit.(reviewModel)
	if len(emptyM.Decisions()) != 0 {
		t.Fatalf("empty edit must not queue decisions, got %+v", emptyM.Decisions())
	}
	if emptyM.mode != reviewModeEdit {
		t.Fatalf("empty submit should keep edit mode for retry, got %v", emptyM.mode)
	}
	if !strings.Contains(emptyM.statusMsg, "operator-authored") && !strings.Contains(emptyM.statusMsg, "operator") {
		t.Fatalf("empty submit must explain why; got %q", emptyM.statusMsg)
	}

	step, _ := emptyM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("revised fact")})
	stepM := step.(reviewModel)
	commit, _ := stepM.Update(tea.KeyMsg{Type: tea.KeyEnter})
	committed := commit.(reviewModel)

	decisions := committed.Decisions()
	if len(decisions) != 1 {
		t.Fatalf("expected one distill decision, got %+v", decisions)
	}
	if decisions[0].kind != reviewDecisionDistill {
		t.Fatalf("decisions[0].kind = %v, want distill", decisions[0].kind)
	}
	if decisions[0].fact != "revised fact" {
		t.Fatalf("decisions[0].fact = %q, want %q", decisions[0].fact, "revised fact")
	}
	if committed.mode != reviewModeBrowse {
		t.Fatalf("after commit, mode = %v, want browse", committed.mode)
	}
}

// TestReviewModel_EditCancelDoesNotQueue confirms Esc inside edit mode
// drops the buffer and returns to browse without queuing a Distill.
func TestReviewModel_EditCancelDoesNotQueue(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact"),
	)
	editing, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	editingM := editing.(reviewModel)
	typed, _ := editingM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft")})
	cancelled, _ := typed.(reviewModel).Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := cancelled.(reviewModel)

	if got.mode != reviewModeBrowse {
		t.Fatalf("after cancel, mode = %v, want browse", got.mode)
	}
	if got.editBuffer != "" {
		t.Fatalf("after cancel, editBuffer = %q, want empty", got.editBuffer)
	}
	if len(got.Decisions()) != 0 {
		t.Fatalf("cancel must not queue decisions, got %+v", got.Decisions())
	}
}

// TestReviewModel_EditBackspaceTrimsBuffer verifies the in-model edit
// buffer handles backspace correctly — required because the model owns
// the buffer rather than delegating to a textinput.
func TestReviewModel_EditBackspaceTrimsBuffer(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "fact"),
	)
	editing, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	editingM := editing.(reviewModel)
	typed, _ := editingM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	typedM := typed.(reviewModel)
	bspd, _ := typedM.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	got := bspd.(reviewModel)
	if got.editBuffer != "hell" {
		t.Fatalf("editBuffer after backspace = %q, want %q", got.editBuffer, "hell")
	}
}

// TestReviewModel_QuitProducesTeaQuit pins that quitting issues the
// canonical tui.Quit command so the runner exits cleanly. Three keys
// (q, ctrl+c, esc) all map to quit per the shared keymap while the
// operator is on the browse screen — overlay-mode Esc behavior is
// covered by TestReviewModel_EscDismissesHelpOverlay /
// TestReviewModel_EscDismissesEvidenceOverlay.
func TestReviewModel_QuitProducesTeaQuit(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(buildReviewCandidate(t, "id-1", "fact"))
	cases := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
	}
	for _, msg := range cases {
		_, cmd := model.Update(msg)
		if cmd == nil {
			t.Fatalf("expected non-nil cmd for %#v", msg)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("expected tea.QuitMsg for %#v, got %T", msg, cmd())
		}
	}
}

// TestReviewModel_EscDismissesHelpOverlay pins that Esc inside the help
// overlay returns to browse instead of quitting the program. The shared
// keymap binds esc to Quit, so without an overlay-aware override the
// operator's Esc would yank them out of the review entirely — surprising
// when the matching `?` toggle just dismisses the overlay.
func TestReviewModel_EscDismissesHelpOverlay(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(buildReviewCandidate(t, "id-1", "fact"))
	on, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	helpM := on.(reviewModel)
	if helpM.mode != reviewModeHelp {
		t.Fatalf("expected help mode before Esc, got %v", helpM.mode)
	}
	off, cmd := helpM.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := off.(reviewModel)
	if got.mode != reviewModeBrowse {
		t.Fatalf("Esc in help mode should return to browse, got mode=%v", got.mode)
	}
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatalf("Esc in help mode must not quit the program")
		}
	}
}

// TestReviewModel_EscDismissesEvidenceOverlay mirrors the help-overlay
// guard for the evidence modal: the overlay's hint text says "v / esc
// back" and the model must honor it instead of falling through to the
// shared Quit binding.
func TestReviewModel_EscDismissesEvidenceOverlay(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(buildReviewCandidate(t, "id-1", "fact"))
	on, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	evM := on.(reviewModel)
	if evM.mode != reviewModeViewEvidence {
		t.Fatalf("expected evidence mode before Esc, got %v", evM.mode)
	}
	off, cmd := evM.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := off.(reviewModel)
	if got.mode != reviewModeBrowse {
		t.Fatalf("Esc in evidence mode should return to browse, got mode=%v", got.mode)
	}
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatalf("Esc in evidence mode must not quit the program")
		}
	}
}

// TestReviewModel_HelpToggle covers the "?" binding flipping into and
// out of help mode without changing the queue.
func TestReviewModel_HelpToggle(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(buildReviewCandidate(t, "id-1", "fact"))
	on, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if on.(reviewModel).mode != reviewModeHelp {
		t.Fatalf("? should open help, got mode=%v", on.(reviewModel).mode)
	}
	off, _ := on.(reviewModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if off.(reviewModel).mode != reviewModeBrowse {
		t.Fatalf("second ? should close help, got mode=%v", off.(reviewModel).mode)
	}
}

func TestReviewModel_EvidenceToggle(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(buildReviewCandidate(t, "id-1", "fact"))
	on, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	got := on.(reviewModel)
	if got.mode != reviewModeViewEvidence {
		t.Fatalf("v should open evidence, got mode=%v", got.mode)
	}
	view := got.View()
	if !strings.Contains(view, "EVIDENCE_REFS") {
		t.Fatalf("evidence view missing header, got:\n%s", view)
	}
	if !strings.Contains(view, "/tmp/MEMORY.md") {
		t.Fatalf("evidence view missing ref value, got:\n%s", view)
	}
}

func TestReviewModel_EvidenceViewSanitizesRefs(t *testing.T) {
	t.Parallel()

	model := newReviewTestModel(buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:            "id-unsafe-ref",
		evidenceValue: "safe\n\x1b[31m\u202eunsafe",
	}))
	on, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	view := on.(reviewModel).View()
	for _, forbidden := range []string{"\n\x1b", "\u202e"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("evidence view contains unsafe ref sequence %q:\n%s", forbidden, view)
		}
	}
	if !strings.Contains(view, "file:safe [31m unsafe") {
		t.Fatalf("evidence view missing sanitized ref:\n%s", view)
	}
}

// TestReviewModel_EmptyInboxRendersGuidanceAndQuits pins that an empty
// inbox does not crash the model and surfaces a guidance line plus a
// working quit key.
func TestReviewModel_EmptyInboxRendersGuidanceAndQuits(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel()
	view := model.View()
	if !strings.Contains(view, "memory review queue") {
		t.Fatalf("empty inbox view should mention the empty state, got:\n%s", view)
	}
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q on empty inbox should still quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q on empty inbox should produce tea.QuitMsg, got %T", cmd())
	}
}

// TestReviewModel_NavigationStaysWithinBounds covers cursor up/down at
// the ends of the queue. The model must clamp rather than wrap so the
// operator notices when they reach the bottom.
func TestReviewModel_NavigationStaysWithinBounds(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(
		buildReviewCandidate(t, "id-1", "f1"),
		buildReviewCandidate(t, "id-2", "f2"),
	)

	up, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	if up.(reviewModel).cursor != 0 {
		t.Fatalf("cursor at top after Up = %d, want 0", up.(reviewModel).cursor)
	}
	down1, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	down2, _ := down1.(reviewModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	if down2.(reviewModel).cursor != 1 {
		t.Fatalf("cursor clamped at last = %d, want 1", down2.(reviewModel).cursor)
	}
}

// TestReviewMode_DefaultActionKeysNoOverlap guards against future key
// rebinding accidentally letting accept and reject fire on the same
// rune. The default review action keys are the "a/x" pair; if a
// follow-up moves to another rune it must remain disjoint.
func TestReviewMode_DefaultActionKeysNoOverlap(t *testing.T) {
	t.Parallel()
	keys := defaultReviewActionKeys()
	all := []key.Binding{keys.Accept, keys.Reject, keys.Skip, keys.Edit, keys.Attach, keys.View, keys.Confirm, keys.Cancel}
	seen := make(map[string]string)
	for _, b := range all {
		for _, k := range b.Keys() {
			if name, ok := seen[k]; ok && name != b.Help().Key {
				t.Fatalf("key %q is bound to multiple actions (%s and %s)", k, name, b.Help().Key)
			}
			seen[k] = b.Help().Key
		}
	}
}

// TestInboxReviewExitError_CarriesExitCodeTwo pins the public contract
// that the non-TTY refusal exits with code 2 so shell callers can branch
// on it without parsing stderr.
func TestInboxReviewExitError_CarriesExitCodeTwo(t *testing.T) {
	t.Parallel()
	err := newInboxReviewNonInteractiveError()
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("non-TTY error must implement ExitCode(); got %T", err)
	}
	if coder.ExitCode() != 2 {
		t.Fatalf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	msg := err.Error()
	for _, must := range []string{"memory inbox list", "memory inbox attach", "memory inbox accept", "memory inbox reject"} {
		if !strings.Contains(msg, must) {
			t.Fatalf("non-TTY guidance missing %q; got:\n%s", must, msg)
		}
	}
}

func TestWriteMemoryInboxReviewSummary_FailureReturnsError(t *testing.T) {
	t.Parallel()
	candidate := buildReviewCandidate(t, "id-ok", "accepted fact")

	okResult := memoryInboxReviewResult{Accepted: []apptypes.MemoryDetails{candidate}}
	okOut := &strings.Builder{}
	if err := writeMemoryInboxReviewSummary(okOut, okResult); err != nil {
		t.Fatalf("writeMemoryInboxReviewSummary(success) error = %v, want nil", err)
	}

	failureResult := memoryInboxReviewResult{
		Accepted: []apptypes.MemoryDetails{candidate},
		Failures: []memoryInboxFailure{
			{ID: "id-fail", Error: "synthetic failure"},
		},
	}
	out := &strings.Builder{}
	err := writeMemoryInboxReviewSummary(out, failureResult)
	if err == nil {
		t.Fatalf("writeMemoryInboxReviewSummary(failure) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "memory review failed for 1 memory id(s)") {
		t.Fatalf("unexpected failure error: %v", err)
	}
	want := "review accepted=1 rejected=0 distilled=0 failures=1 attached=0\nACCEPT\tid-ok\tcandidate\nFAILED\tid-fail\tsynthetic failure\n"
	if got := out.String(); got != want {
		t.Fatalf("summary output changed:\n got %q\nwant %q", got, want)
	}
}

func TestFinishMemoryInboxReview_ReturnsFailureError(t *testing.T) {
	t.Parallel()
	candidate := buildReviewCandidate(t, "id-1", "accepted fact")
	final := newReviewTestModel(candidate)
	final.decisions = []reviewDecision{
		{kind: reviewDecisionAccept, memoryID: candidate.Summary().MemoryID()},
	}
	stub := &reviewWriterStub{acceptErr: errors.New("synthetic accept failure")}
	out := &strings.Builder{}

	err := finishMemoryInboxReview(context.Background(), out, stub, final, []apptypes.MemoryDetails{candidate})
	if err == nil {
		t.Fatal("finishMemoryInboxReview error = nil, want failure summary error")
	}
	if !strings.Contains(err.Error(), "memory review failed for 1 memory id(s)") {
		t.Fatalf("unexpected failure error: %v", err)
	}
	if stub.acceptCalls != 1 {
		t.Fatalf("acceptCalls = %d, want 1", stub.acceptCalls)
	}
	want := "review accepted=0 rejected=0 distilled=0 failures=1 attached=0\nFAILED\tid-1\tsynthetic accept failure\n"
	if got := out.String(); got != want {
		t.Fatalf("summary output changed:\n got %q\nwant %q", got, want)
	}
}

// TestApplyInboxReviewDecisions_DispatchesToUsecases drives the post-quit
// runner directly with a list of decisions and verifies each one routes
// to the correct usecase method exactly once. The corresponding stubs
// for List/Show are not exercised here because the runner takes the
// already-loaded items list as input.
func TestApplyInboxReviewDecisions_DispatchesToUsecases(t *testing.T) {
	t.Parallel()
	candidate1 := buildReviewCandidate(t, "id-1", "fact 1")
	candidate2 := buildReviewCandidate(t, "id-2", "fact 2")
	candidate3 := buildReviewCandidate(t, "id-3", "fact 3")
	candidate4 := buildReviewCandidateWithOptions(t, reviewCandidateOptions{id: "id-4", fact: "fact 4", noEvidence: true})
	attachRef := mustReviewEvidenceRef(t, domtypes.EvidenceRefKindEvent, "evt-4")
	attachArtifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindPR, "#1074")
	if err != nil {
		t.Fatalf("ArtifactRefFrom: %v", err)
	}
	stub := &reviewWriterStub{
		acceptDetails: candidate1,
		rejectDetails: candidate2,
		distillResult: apptypes.MemoryDistillResultOf(candidate3, nil, apptypes.MemoryDistillReplaceSupersede),
		attachDetails: candidate4,
	}

	decisions := []reviewDecision{
		{kind: reviewDecisionAttach, memoryID: candidate4.Summary().MemoryID(), evidenceRefs: []domtypes.EvidenceRef{attachRef}, artifactRefs: []domtypes.ArtifactRef{attachArtifact}},
		{kind: reviewDecisionAccept, memoryID: candidate1.Summary().MemoryID()},
		{kind: reviewDecisionReject, memoryID: candidate2.Summary().MemoryID()},
		{kind: reviewDecisionDistill, memoryID: candidate3.Summary().MemoryID(), fact: "operator wrote this"},
	}
	items := []apptypes.MemoryDetails{candidate1, candidate2, candidate3, candidate4}

	result, err := applyInboxReviewDecisions(context.Background(), stub, decisions, items)
	if err != nil {
		t.Fatalf("applyInboxReviewDecisions: %v", err)
	}
	if stub.attachCalls != 1 || stub.acceptCalls != 1 || stub.rejectCalls != 1 || stub.distillCalls != 1 {
		t.Fatalf("usecase call counts (attach=%d accept=%d reject=%d distill=%d) want all 1", stub.attachCalls, stub.acceptCalls, stub.rejectCalls, stub.distillCalls)
	}
	if len(stub.lastAttachEvidence) != 1 || stub.lastAttachEvidence[0].Value() != "evt-4" {
		t.Fatalf("AttachCandidateRefs evidence = %+v", stub.lastAttachEvidence)
	}
	if len(stub.lastAttachArtifact) != 1 || stub.lastAttachArtifact[0].Value() != "#1074" {
		t.Fatalf("AttachCandidateRefs artifact = %+v", stub.lastAttachArtifact)
	}
	if got := stub.lastDistillCriteria.Fact(); got != "operator wrote this" {
		t.Fatalf("Distill received fact=%q, want operator-authored input", got)
	}
	if stub.lastDistillCriteria.Replace() != apptypes.MemoryDistillReplaceSupersede {
		t.Fatalf("Distill replace = %v, want supersede", stub.lastDistillCriteria.Replace())
	}
	if got := stub.lastDistillCriteria.MemoryType(); got != candidate3.Summary().MemoryType() {
		t.Fatalf("Distill memoryType = %v, want %v (inherited from candidate)", got, candidate3.Summary().MemoryType())
	}
	if len(result.Attached) != 1 || len(result.Accepted) != 1 || len(result.Rejected) != 1 || len(result.Distilled) != 1 {
		t.Fatalf("result attach/accept/reject/distill = %d/%d/%d/%d, want 1/1/1/1", len(result.Attached), len(result.Accepted), len(result.Rejected), len(result.Distilled))
	}
	if len(result.Failures) != 0 {
		t.Fatalf("unexpected failures: %+v", result.Failures)
	}
}

// TestApplyInboxReviewDecisions_FailureIsRecordedNotPropagated guarantees
// the runner keeps applying later decisions even when an earlier one
// fails — the post-quit summary is the operator's only feedback for
// queued work, and short-circuiting would silently drop everything past
// the first conflict.
func TestApplyInboxReviewDecisions_FailureIsRecordedNotPropagated(t *testing.T) {
	t.Parallel()
	candidate1 := buildReviewCandidate(t, "id-1", "fact 1")
	candidate2 := buildReviewCandidate(t, "id-2", "fact 2")
	stub := &reviewWriterStub{
		acceptErr:     errors.New("conflict"),
		rejectDetails: candidate2,
	}
	decisions := []reviewDecision{
		{kind: reviewDecisionAccept, memoryID: candidate1.Summary().MemoryID()},
		{kind: reviewDecisionReject, memoryID: candidate2.Summary().MemoryID()},
	}
	items := []apptypes.MemoryDetails{candidate1, candidate2}

	result, err := applyInboxReviewDecisions(context.Background(), stub, decisions, items)
	if err != nil {
		t.Fatalf("applyInboxReviewDecisions returned error %v; failures should be in result instead", err)
	}
	if len(result.Failures) != 1 || result.Failures[0].ID != "id-1" {
		t.Fatalf("expected one failure for id-1, got %+v", result.Failures)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Summary().MemoryID().String() != "id-2" {
		t.Fatalf("reject for id-2 must still land after id-1 failure, got %+v", result.Rejected)
	}
}

func TestApplyInboxReviewDecisions_SkipsDependentAcceptAfterAttachFailure(t *testing.T) {
	t.Parallel()
	candidate := buildReviewCandidateWithOptions(t, reviewCandidateOptions{id: "id-attach-fail", fact: "fact", noEvidence: true})
	attachRef := mustReviewEvidenceRef(t, domtypes.EvidenceRefKindEvent, "evt-fail")
	stub := &reviewWriterStub{
		attachErr: errors.New("attach conflict"),
	}
	decisions := []reviewDecision{
		{kind: reviewDecisionAttach, memoryID: candidate.Summary().MemoryID(), evidenceRefs: []domtypes.EvidenceRef{attachRef}},
		{kind: reviewDecisionAccept, memoryID: candidate.Summary().MemoryID()},
	}

	result, err := applyInboxReviewDecisions(context.Background(), stub, decisions, []apptypes.MemoryDetails{candidate})
	if err != nil {
		t.Fatalf("applyInboxReviewDecisions returned error %v; failures should be in result instead", err)
	}
	if stub.attachCalls != 1 {
		t.Fatalf("attachCalls = %d, want 1", stub.attachCalls)
	}
	if stub.acceptCalls != 0 {
		t.Fatalf("acceptCalls = %d, want 0 after attach failure", stub.acceptCalls)
	}
	if len(result.Failures) != 2 {
		t.Fatalf("failures = %+v, want attach failure and dependent accept skip", result.Failures)
	}
	if !strings.Contains(result.Failures[1].Error, "skipped after evidence attach failed") {
		t.Fatalf("dependent failure = %+v, want skipped-after-attach message", result.Failures[1])
	}
}

// reviewWriterStub records calls into the inboxReviewWriter surface so
// applyInboxReviewDecisions tests can pin which usecase methods fire
// without bringing in the full memoryUsecase interface.
type reviewWriterStub struct {
	acceptDetails       apptypes.MemoryDetails
	acceptErr           error
	rejectDetails       apptypes.MemoryDetails
	rejectErr           error
	distillResult       apptypes.MemoryDistillResult
	distillErr          error
	attachDetails       apptypes.MemoryDetails
	attachErr           error
	acceptCalls         int
	rejectCalls         int
	distillCalls        int
	attachCalls         int
	calls               []string
	lastAttachEvidence  []domtypes.EvidenceRef
	lastAttachArtifact  []domtypes.ArtifactRef
	lastDistillCriteria apptypes.MemoryDistillCriteria
}

func (s *reviewWriterStub) Accept(_ context.Context, _ domtypes.MemoryID, _ domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error) {
	s.acceptCalls++
	s.calls = append(s.calls, "accept")
	return s.acceptDetails, s.acceptErr
}

func (s *reviewWriterStub) Reject(_ context.Context, _ domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	s.rejectCalls++
	s.calls = append(s.calls, "reject")
	return s.rejectDetails, s.rejectErr
}

func (s *reviewWriterStub) Distill(_ context.Context, criteria apptypes.MemoryDistillCriteria) (apptypes.MemoryDistillResult, error) {
	s.distillCalls++
	s.calls = append(s.calls, "distill")
	s.lastDistillCriteria = criteria
	return s.distillResult, s.distillErr
}

func (s *reviewWriterStub) AttachCandidateRefs(_ context.Context, _ domtypes.MemoryID, evidenceRefs []domtypes.EvidenceRef, artifactRefs []domtypes.ArtifactRef) (apptypes.MemoryDetails, error) {
	s.attachCalls++
	s.calls = append(s.calls, "attach")
	s.lastAttachEvidence = append([]domtypes.EvidenceRef(nil), evidenceRefs...)
	s.lastAttachArtifact = append([]domtypes.ArtifactRef(nil), artifactRefs...)
	return s.attachDetails, s.attachErr
}
