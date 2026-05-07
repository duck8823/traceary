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
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L2")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{evidence}, nil)
}

func newReviewTestModel(items ...apptypes.MemoryDetails) reviewModel {
	return newReviewModel(items, tui.DefaultKeyMap(), tui.DefaultStyles())
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
// (q, ctrl+c, esc) all map to quit per the shared keymap.
func TestReviewModel_QuitProducesTeaQuit(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel(buildReviewCandidate(t, "id-1", "fact"))
	cases := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
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

// TestReviewModel_EmptyInboxRendersGuidanceAndQuits pins that an empty
// inbox does not crash the model and surfaces a guidance line plus a
// working quit key.
func TestReviewModel_EmptyInboxRendersGuidanceAndQuits(t *testing.T) {
	t.Parallel()
	model := newReviewTestModel()
	view := model.View()
	if !strings.Contains(view, "candidate") {
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
	all := []key.Binding{keys.Accept, keys.Reject, keys.Skip, keys.Edit, keys.View, keys.Confirm, keys.Cancel}
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
	for _, must := range []string{"memory inbox list", "memory inbox accept", "memory inbox reject"} {
		if !strings.Contains(msg, must) {
			t.Fatalf("non-TTY guidance missing %q; got:\n%s", must, msg)
		}
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
	stub := &reviewWriterStub{
		acceptDetails: candidate1,
		rejectDetails: candidate2,
		distillResult: apptypes.MemoryDistillResultOf(candidate3, nil, apptypes.MemoryDistillReplaceSupersede),
	}

	decisions := []reviewDecision{
		{kind: reviewDecisionAccept, memoryID: candidate1.Summary().MemoryID()},
		{kind: reviewDecisionReject, memoryID: candidate2.Summary().MemoryID()},
		{kind: reviewDecisionDistill, memoryID: candidate3.Summary().MemoryID(), fact: "operator wrote this"},
	}
	items := []apptypes.MemoryDetails{candidate1, candidate2, candidate3}

	result, err := applyInboxReviewDecisions(context.Background(), stub, decisions, items)
	if err != nil {
		t.Fatalf("applyInboxReviewDecisions: %v", err)
	}
	if stub.acceptCalls != 1 || stub.rejectCalls != 1 || stub.distillCalls != 1 {
		t.Fatalf("usecase call counts (accept=%d reject=%d distill=%d) want all 1", stub.acceptCalls, stub.rejectCalls, stub.distillCalls)
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
	if len(result.Accepted) != 1 || len(result.Rejected) != 1 || len(result.Distilled) != 1 {
		t.Fatalf("result accept/reject/distill = %d/%d/%d, want 1/1/1", len(result.Accepted), len(result.Rejected), len(result.Distilled))
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
	acceptCalls         int
	rejectCalls         int
	distillCalls        int
	lastDistillCriteria apptypes.MemoryDistillCriteria
}

func (s *reviewWriterStub) Accept(_ context.Context, _ domtypes.MemoryID, _ domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error) {
	s.acceptCalls++
	return s.acceptDetails, s.acceptErr
}

func (s *reviewWriterStub) Reject(_ context.Context, _ domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	s.rejectCalls++
	return s.rejectDetails, s.rejectErr
}

func (s *reviewWriterStub) Distill(_ context.Context, criteria apptypes.MemoryDistillCriteria) (apptypes.MemoryDistillResult, error) {
	s.distillCalls++
	s.lastDistillCriteria = criteria
	return s.distillResult, s.distillErr
}
