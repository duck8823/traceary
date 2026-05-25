package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

// reviewExitCodeNotInteractive is surfaced by `memory inbox review` when the
// command is invoked without a TTY. The dedicated exit code lets shell
// callers branch (run the batch alternatives) without having to grep
// stderr, mirroring the doctor command's exit-code contract.
const reviewExitCodeNotInteractive = 2

const (
	memoryReviewRefPreviewLimit    = 5
	memoryReviewRefDisplayMaxRunes = 160
	memoryReviewTruncatedRefSuffix = "…"
)

// inboxReviewExitError carries a process exit code through cli.run() so the
// non-TTY refusal returns 2 instead of the default 1. The pattern matches
// doctorExitError; we keep them separate types so a future change to the
// doctor flow does not silently affect the review flow.
type inboxReviewExitError struct {
	message  string
	exitCode int
}

func (e inboxReviewExitError) Error() string { return e.message }
func (e inboxReviewExitError) ExitCode() int { return e.exitCode }

func (c *RootCLI) newMemoryInboxReviewCommand() *cobra.Command {
	input := memoryInboxReviewCommandInput{}
	cmd := &cobra.Command{
		Use:   "review",
		Short: Localize("Review memory review queue candidates interactively", "メモリ候補を対話的に確認する"),
		Long: Localize(
			"Walk the memory review queue in an interactive TTY. Accept / reject decisions reuse the same application use cases as `memory inbox accept|reject`; edit prompts you to type a new operator-authored fact and runs through `memory store distill`. Non-TTY shells should use `memory inbox list` plus `memory inbox accept|reject` instead.",
			"メモリ候補の確認キューを対話的に巡回します。Accept / reject は `memory inbox accept|reject` と同じ application usecase を呼び出し、edit は operator が手で書き起こした fact を入力させて `memory store distill` を経由します。非対話シェルでは `memory inbox list` と `memory inbox accept|reject` を使ってください。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryInboxReview(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("filter by workspace scope (defaults to env/detected workspace)", "workspace scope で絞り込む (未指定時は env/検出 workspace)"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("filter by agent scope", "agent scope で絞り込む"))
	cmd.Flags().StringVar(&input.sessionFamily, "session-family", "", Localize("filter by session-family scope", "session-family scope で絞り込む"))
	cmd.Flags().StringSliceVar(&input.memoryTypes, "type", nil, Localize("filter by memory type", "memory type で絞り込む"))
	cmd.Flags().StringSliceVar(&input.sources, "source", nil, Localize("filter by memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported)", "memory source (manual / extracted / extracted-hidden / remember-intent / compact-summary / imported) で絞り込む"))
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden memory candidates (low-quality auto-extractions kept for audit)", "extracted-hidden のメモリ候補も含める (audit 用に保存された低品質自動抽出)"))
	cmd.Flags().IntVar(&input.limit, "limit", defaultMemoryInboxLimit, Localize("maximum number of memory candidates to load into the memory review queue", "メモリ候補の確認キューに読み込む最大件数"))
	return cmd
}

// memoryInboxReviewCommandInput is the resolved input to `traceary memory
// inbox review`. The filter set mirrors `memory inbox list` so reviewers
// can pivot between the snapshot view and the interactive walk-through
// without re-tuning flags.
type memoryInboxReviewCommandInput struct {
	dbPath        string
	workspace     string
	agent         string
	sessionFamily string
	memoryTypes   []string
	sources       []string
	includeHidden bool
	limit         int
}

func (c *RootCLI) runMemoryInboxReview(ctx context.Context, output io.Writer, input memoryInboxReviewCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	stdin, stdout := inboxReviewIO(output)
	if !tui.Interactive(stdin, stdout) {
		return newInboxReviewNonInteractiveError()
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	items, err := c.loadInboxReviewItems(ctx, input)
	if err != nil {
		return err
	}

	model := newReviewModel(items, tui.DefaultKeyMap(), tui.DefaultStyles())
	finalModel, runErr := tui.RunModel(model, tui.RunOptions{Input: stdin, Output: stdout})
	if runErr != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run interactive review", "対話的レビューの実行に失敗しました"), runErr)
	}

	final, ok := finalModel.(reviewModel)
	if !ok {
		return xerrors.Errorf("%s", Localize("review model returned unexpected final state", "review model が想定外の最終状態を返しました"))
	}

	return finishMemoryInboxReview(ctx, output, c.memory, final, items)
}

func finishMemoryInboxReview(ctx context.Context, output io.Writer, writer inboxReviewWriter, final reviewModel, items []apptypes.MemoryDetails) error {
	result, applyErr := applyInboxReviewDecisions(ctx, writer, final.Decisions(), items)
	if applyErr != nil {
		return applyErr
	}
	return writeMemoryInboxReviewSummary(output, result)
}

// loadInboxReviewItems resolves the inbox-list filter set into the same
// candidate page `memory inbox list` would render, then hydrates each row
// into MemoryDetails so the review UI can display evidence and artifact
// counts without an extra round trip.
func (c *RootCLI) loadInboxReviewItems(ctx context.Context, input memoryInboxReviewCommandInput) ([]apptypes.MemoryDetails, error) {
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
	sources = applyExtractedHiddenDefault(sources, input.includeHidden)

	criteria := apptypes.NewMemoryListCriteriaBuilder(input.limit).
		Scopes(scopes).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}).
		MemoryTypes(memoryTypes).
		Sources(sources).
		RememberIntentPriority(true).
		Build()
	summaries, err := c.memory.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list memory review queue candidates", "メモリ候補の確認キューの一覧取得に失敗しました"), err)
	}
	items := make([]apptypes.MemoryDetails, 0, len(summaries))
	for _, summary := range summaries {
		details, err := c.memory.Show(ctx, summary.MemoryID())
		if err != nil {
			return nil, xerrors.Errorf("failed to load memory %s: %w", summary.MemoryID().String(), err)
		}
		items = append(items, details)
	}
	return items, nil
}

// inboxReviewWriter is the narrow slice of the memory usecase the
// post-quit runner needs. Splitting the surface keeps the dispatcher
// test-only stub small and pins exactly which usecase methods the
// review screen relies on. The full MemoryUsecase satisfies this
// interface structurally, so production callers pass c.memory directly.
type inboxReviewWriter interface {
	Accept(ctx context.Context, memoryID domtypes.MemoryID, confidence domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error)
	Reject(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)
	Distill(ctx context.Context, criteria apptypes.MemoryDistillCriteria) (apptypes.MemoryDistillResult, error)
	AttachCandidateRefs(ctx context.Context, memoryID domtypes.MemoryID, evidenceRefs []domtypes.EvidenceRef, artifactRefs []domtypes.ArtifactRef) (apptypes.MemoryDetails, error)
}

// applyInboxReviewDecisions walks the queued decisions in operator order
// and dispatches each one through the existing application use cases
// (memory.Accept / memory.Reject / memory.Distill). The function never
// short-circuits unrelated rows: the caller still expects a per-id
// success/failure breakdown so the operator can see exactly which transitions
// landed even when one row collides with another reviewer. A failed attach is
// the only per-id dependency that suppresses a later accept/distill for the
// same memory because those operations rely on the queued evidence.
func applyInboxReviewDecisions(ctx context.Context, writer inboxReviewWriter, decisions []reviewDecision, items []apptypes.MemoryDetails) (memoryInboxReviewResult, error) {
	byID := make(map[string]apptypes.MemoryDetails, len(items))
	for _, item := range items {
		byID[item.Summary().MemoryID().String()] = item
	}
	result := memoryInboxReviewResult{}
	failedAttach := map[string]struct{}{}
	for _, decision := range decisions {
		id := decision.memoryID.String()
		if _, ok := failedAttach[id]; ok && decision.requiresSuccessfulAttach() {
			result.Failures = append(result.Failures, memoryInboxFailure{
				ID:    id,
				Error: Localize("skipped after evidence attach failed", "evidence 追加の失敗後のため skip しました"),
			})
			continue
		}
		switch decision.kind {
		case reviewDecisionAttach:
			details, err := writer.AttachCandidateRefs(ctx, decision.memoryID, decision.evidenceRefs, decision.artifactRefs)
			if err != nil {
				failedAttach[id] = struct{}{}
				result.Failures = append(result.Failures, memoryInboxFailure{ID: id, Error: err.Error()})
				continue
			}
			result.Attached = append(result.Attached, details)
		case reviewDecisionAccept:
			details, err := writer.Accept(ctx, decision.memoryID, domtypes.None[domtypes.Confidence]())
			if err != nil {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: id, Error: err.Error()})
				continue
			}
			result.Accepted = append(result.Accepted, details)
		case reviewDecisionReject:
			details, err := writer.Reject(ctx, decision.memoryID)
			if err != nil {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: id, Error: err.Error()})
				continue
			}
			result.Rejected = append(result.Rejected, details)
		case reviewDecisionDistill:
			source, ok := byID[id]
			if !ok {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: id, Error: Localize("source memory candidate not found in memory review queue", "メモリ候補の確認キューに source メモリ候補が見つかりません")})
				continue
			}
			details, err := distillFromReview(ctx, writer, source, decision.fact)
			if err != nil {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: id, Error: err.Error()})
				continue
			}
			result.Distilled = append(result.Distilled, details)
		}
	}
	return result, nil
}

// distillFromReview builds a MemoryDistillCriteria from the source
// candidate and the operator-typed fact. Type, scope, and source
// inherit from the candidate so the operator only has to supply the
// fact text; replace=supersede ensures the source memory candidate is marked
// superseded rather than left orphaned. The fact must be operator-authored
// — the model rejects empty edits so this path can trust decision.fact
// is non-empty.
func distillFromReview(ctx context.Context, writer inboxReviewWriter, source apptypes.MemoryDetails, fact string) (apptypes.MemoryDetails, error) {
	summary := source.Summary()
	criteria := apptypes.MemoryDistillCriteriaOf(
		[]domtypes.MemoryID{summary.MemoryID()},
		summary.MemoryType(),
		summary.Scope(),
		fact,
		domtypes.None[domtypes.Confidence](),
		domtypes.MemorySourceManual,
		apptypes.MemoryDistillReplaceSupersede,
	)
	result, err := writer.Distill(ctx, criteria)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("%s: %w", Localize("failed to distill durable memory", "durable memory の distill に失敗しました"), err)
	}
	return result.Distilled(), nil
}

func newInboxReviewNonInteractiveError() error {
	guidance := Localize(
		"`memory inbox review` is interactive and requires a TTY. Use the batch commands instead:\n  traceary memory inbox list [--workspace ... --type ... --source ... --include-hidden --limit N]\n  traceary memory inbox attach <memory-id> --evidence kind:value\n  traceary memory inbox accept <memory-id> | --ids id1,id2,...\n  traceary memory inbox reject <memory-id> | --ids id1,id2,...",
		"`memory inbox review` は対話的コマンドで TTY が必要です。代わりにバッチ用のコマンドを使ってください:\n  traceary memory inbox list [--workspace ... --type ... --source ... --include-hidden --limit N]\n  traceary memory inbox attach <memory-id> --evidence kind:value\n  traceary memory inbox accept <memory-id> | --ids id1,id2,...\n  traceary memory inbox reject <memory-id> | --ids id1,id2,...",
	)
	return inboxReviewExitError{message: guidance, exitCode: reviewExitCodeNotInteractive}
}

// memoryInboxReviewResult is the post-run breakdown of decisions that
// actually committed. Failures (e.g. another reviewer beat this one to
// the row) are surfaced verbatim so the operator can re-check the inbox
// without losing context.
type memoryInboxReviewResult struct {
	Attached  []apptypes.MemoryDetails
	Accepted  []apptypes.MemoryDetails
	Rejected  []apptypes.MemoryDetails
	Distilled []apptypes.MemoryDetails
	Failures  []memoryInboxFailure
}

func writeMemoryInboxReviewSummary(output io.Writer, result memoryInboxReviewResult) error {
	if _, err := fmt.Fprintf(output, "review accepted=%d rejected=%d distilled=%d failures=%d attached=%d\n",
		len(result.Accepted), len(result.Rejected), len(result.Distilled), len(result.Failures), len(result.Attached)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print review summary", "review サマリの出力に失敗しました"), err)
	}
	for _, details := range result.Attached {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "ATTACH\t%s\t%s\tevidence_refs=%d\n", summary.MemoryID(), summary.Status(), len(details.EvidenceRefs())); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print review attach row", "review attach 行の出力に失敗しました"), err)
		}
	}
	for _, details := range result.Accepted {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "ACCEPT\t%s\t%s\n", summary.MemoryID(), summary.Status()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print review accept row", "review accept 行の出力に失敗しました"), err)
		}
	}
	for _, details := range result.Rejected {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "REJECT\t%s\t%s\n", summary.MemoryID(), summary.Status()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print review reject row", "review reject 行の出力に失敗しました"), err)
		}
	}
	for _, details := range result.Distilled {
		summary := details.Summary()
		if _, err := fmt.Fprintf(output, "DISTILL\t%s\t%s\n", summary.MemoryID(), summary.Status()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print review distill row", "review distill 行の出力に失敗しました"), err)
		}
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(output, "FAILED\t%s\t%s\n", failure.ID, failure.Error); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print review failure row", "review 失敗行の出力に失敗しました"), err)
		}
	}
	if len(result.Failures) > 0 {
		return memoryReviewFailureError(result)
	}
	return nil
}

func memoryReviewFailureError(result memoryInboxReviewResult) error {
	return xerrors.New(Localizef(
		"memory review failed for %d memory id(s)",
		"メモリ確認が %d 件の memory id で失敗しました",
		len(result.Failures),
	))
}

// reviewDecisionKind enumerates the post-quit operations the CLI dispatches
// after the interactive walk-through ends. Skip is intentionally not part
// of this enum: a skip leaves no work for the runner.
type reviewDecisionKind int

const (
	reviewDecisionAccept reviewDecisionKind = iota
	reviewDecisionReject
	reviewDecisionDistill
	reviewDecisionAttach
)

// reviewDecision records one operator action queued during the walk. The
// model never executes use cases itself so it stays trivially testable;
// the runner walks the queue after Bubble Tea exits.
type reviewDecision struct {
	kind         reviewDecisionKind
	memoryID     domtypes.MemoryID
	evidenceRefs []domtypes.EvidenceRef
	artifactRefs []domtypes.ArtifactRef
	// fact is only populated for reviewDecisionDistill and is required
	// to be non-empty by the model so the application/use case path can
	// trust the input is operator-authored.
	fact string
}

func (d reviewDecision) requiresSuccessfulAttach() bool {
	return d.kind == reviewDecisionAccept || d.kind == reviewDecisionDistill
}

// reviewMode encodes the sub-screen the model is showing. Keeping the
// modes inside one model (rather than separate stacked tea.Models) makes
// the model trivially driveable from tests.
type reviewMode int

const (
	reviewModeBrowse reviewMode = iota
	reviewModeViewEvidence
	reviewModeHelp
	reviewModeEdit
	reviewModeAttach
)

// reviewModel is the testable Bubble Tea model behind `memory inbox
// review`. It owns the candidate queue, the cursor, the queued decisions,
// and a small input buffer for the edit/distill flow. The model never
// touches usecases directly; the runner replays Decisions() after Run
// returns.
type reviewModel struct {
	keys   tui.KeyMap
	styles tui.Styles

	items     []apptypes.MemoryDetails
	cursor    int
	decisions []reviewDecision
	// reviewed[index] reflects the decision queued for items[index]; it is
	// indexed by position to match what the operator sees in the UI. A
	// value of "" means the item is still untouched (skip leaves it
	// untouched too — skip just advances the cursor).
	reviewed []string

	mode reviewMode
	// editIndex / attachIndex pin which item the input buffer maps to so a
	// cursor move during entry cannot retarget the decision.
	editIndex    int
	editBuffer   string
	attachIndex  int
	attachBuffer string
	statusMsg    string

	acceptConfirmID domtypes.MemoryID
}

// reviewActionKeys is the local extension of tui.KeyMap with the actions
// specific to the review screen. Tests can swap these for a stub map but
// production code uses the defaults.
type reviewActionKeys struct {
	Accept  key.Binding
	Reject  key.Binding
	Skip    key.Binding
	Edit    key.Binding
	Attach  key.Binding
	View    key.Binding
	Confirm key.Binding
	Cancel  key.Binding
}

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
