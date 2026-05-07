package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

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
		Short: Localize("Review candidate durable memories interactively", "candidate durable memory を対話的にレビューする"),
		Long: Localize(
			"Walk the candidate durable-memory inbox in an interactive TTY. Accept / reject decisions reuse the same application use cases as `memory inbox accept|reject`; edit prompts you to type a new operator-authored fact and runs through `memory store distill`. Non-TTY shells should use `memory inbox list` plus `memory inbox accept|reject` instead.",
			"candidate durable memory の inbox を対話的に巡回します。Accept / reject は `memory inbox accept|reject` と同じ application usecase を呼び出し、edit は operator が手で書き起こした fact を入力させて `memory store distill` を経由します。非対話シェルでは `memory inbox list` と `memory inbox accept|reject` を使ってください。",
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
	cmd.Flags().BoolVar(&input.includeHidden, "include-hidden", false, Localize("include extracted-hidden candidates (low-quality auto-extractions kept for audit)", "extracted-hidden の候補も含める (audit 用に保存された低品質自動抽出)"))
	cmd.Flags().IntVar(&input.limit, "limit", defaultMemoryInboxLimit, Localize("maximum number of candidates to load into the review queue", "review キューに読み込む最大件数"))
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
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.Errorf(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
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

	result, applyErr := applyInboxReviewDecisions(ctx, c.memory, final.Decisions(), items)
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
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list candidate memories", "candidate memory の一覧取得に失敗しました"), err)
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
}

// applyInboxReviewDecisions walks the queued decisions in operator order
// and dispatches each one through the existing application use cases
// (memory.Accept / memory.Reject / memory.Distill). The function never
// short-circuits on a single failure: the caller still expects a
// per-id success/failure breakdown so the operator can see exactly which
// transitions landed even when one row collides with another reviewer.
func applyInboxReviewDecisions(ctx context.Context, writer inboxReviewWriter, decisions []reviewDecision, items []apptypes.MemoryDetails) (memoryInboxReviewResult, error) {
	byID := make(map[string]apptypes.MemoryDetails, len(items))
	for _, item := range items {
		byID[item.Summary().MemoryID().String()] = item
	}
	result := memoryInboxReviewResult{}
	for _, decision := range decisions {
		switch decision.kind {
		case reviewDecisionAccept:
			details, err := writer.Accept(ctx, decision.memoryID, domtypes.None[domtypes.Confidence]())
			if err != nil {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: decision.memoryID.String(), Error: err.Error()})
				continue
			}
			result.Accepted = append(result.Accepted, details)
		case reviewDecisionReject:
			details, err := writer.Reject(ctx, decision.memoryID)
			if err != nil {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: decision.memoryID.String(), Error: err.Error()})
				continue
			}
			result.Rejected = append(result.Rejected, details)
		case reviewDecisionDistill:
			source, ok := byID[decision.memoryID.String()]
			if !ok {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: decision.memoryID.String(), Error: Localize("source candidate not found in review queue", "review キューに source candidate が見つかりません")})
				continue
			}
			details, err := distillFromReview(ctx, writer, source, decision.fact)
			if err != nil {
				result.Failures = append(result.Failures, memoryInboxFailure{ID: decision.memoryID.String(), Error: err.Error()})
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
// fact text; replace=supersede ensures the source candidate is marked
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
		"`memory inbox review` is interactive and requires a TTY. Use the batch commands instead:\n  traceary memory inbox list [--workspace ... --type ... --source ... --include-hidden --limit N]\n  traceary memory inbox accept <memory-id> | --ids id1,id2,...\n  traceary memory inbox reject <memory-id> | --ids id1,id2,...",
		"`memory inbox review` は対話的コマンドで TTY が必要です。代わりにバッチ用のコマンドを使ってください:\n  traceary memory inbox list [--workspace ... --type ... --source ... --include-hidden --limit N]\n  traceary memory inbox accept <memory-id> | --ids id1,id2,...\n  traceary memory inbox reject <memory-id> | --ids id1,id2,...",
	)
	return inboxReviewExitError{message: guidance, exitCode: reviewExitCodeNotInteractive}
}

// memoryInboxReviewResult is the post-run breakdown of decisions that
// actually committed. Failures (e.g. another reviewer beat this one to
// the row) are surfaced verbatim so the operator can re-check the inbox
// without losing context.
type memoryInboxReviewResult struct {
	Accepted  []apptypes.MemoryDetails
	Rejected  []apptypes.MemoryDetails
	Distilled []apptypes.MemoryDetails
	Failures  []memoryInboxFailure
}

func writeMemoryInboxReviewSummary(output io.Writer, result memoryInboxReviewResult) error {
	if _, err := fmt.Fprintf(output, "review accepted=%d rejected=%d distilled=%d failures=%d\n",
		len(result.Accepted), len(result.Rejected), len(result.Distilled), len(result.Failures)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print review summary", "review サマリの出力に失敗しました"), err)
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
	return nil
}

// reviewDecisionKind enumerates the post-quit operations the CLI dispatches
// after the interactive walk-through ends. Skip is intentionally not part
// of this enum: a skip leaves no work for the runner.
type reviewDecisionKind int

const (
	reviewDecisionAccept reviewDecisionKind = iota
	reviewDecisionReject
	reviewDecisionDistill
)

// reviewDecision records one operator action queued during the walk. The
// model never executes use cases itself so it stays trivially testable;
// the runner walks the queue after Bubble Tea exits.
type reviewDecision struct {
	kind     reviewDecisionKind
	memoryID domtypes.MemoryID
	// fact is only populated for reviewDecisionDistill and is required
	// to be non-empty by the model so the application/use case path can
	// trust the input is operator-authored.
	fact string
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
	// editIndex pins which item the edit buffer maps to so a cursor move
	// during edit cannot retarget the decision.
	editIndex  int
	editBuffer string
	statusMsg  string
}

// reviewActionKeys is the local extension of tui.KeyMap with the actions
// specific to the review screen. Tests can swap these for a stub map but
// production code uses the defaults.
type reviewActionKeys struct {
	Accept  key.Binding
	Reject  key.Binding
	Skip    key.Binding
	Edit    key.Binding
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
	return m.updateBrowse(keyMsg)
}

// updateBrowse handles keys outside of edit mode. Accept / reject /
// distill commit a decision and advance; navigation and toggles never
// touch the decisions queue.
func (m reviewModel) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := defaultReviewActionKeys()
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
	case key.Matches(msg, m.keys.Up):
		if len(m.items) > 0 && m.cursor > 0 {
			m.cursor--
		}
		m.statusMsg = ""
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.cursor+1 < len(m.items) {
			m.cursor++
		}
		m.statusMsg = ""
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.cursor = 0
		m.statusMsg = ""
		return m, nil
	case key.Matches(msg, m.keys.End):
		if len(m.items) > 0 {
			m.cursor = len(m.items) - 1
		}
		m.statusMsg = ""
		return m, nil
	case key.Matches(msg, actions.Accept):
		return m.queueDecision(reviewDecisionAccept, "")
	case key.Matches(msg, actions.Reject):
		return m.queueDecision(reviewDecisionReject, "")
	case key.Matches(msg, actions.Skip):
		if len(m.items) == 0 {
			return m, nil
		}
		m.statusMsg = Localize("skipped", "skip しました")
		m.advanceCursor()
		return m, nil
	case key.Matches(msg, actions.Edit):
		if len(m.items) == 0 {
			return m, nil
		}
		m.mode = reviewModeEdit
		m.editIndex = m.cursor
		m.editBuffer = ""
		m.statusMsg = ""
		return m, nil
	}
	return m, nil
}

// queueDecision records a decision for the current item, marks the row
// reviewed, and advances the cursor to the next untouched candidate.
// Multiple decisions for the same id are not allowed: re-queuing on an
// already-reviewed row replaces the prior entry so the runner only ever
// sees the operator's last word for a given id.
func (m reviewModel) queueDecision(kind reviewDecisionKind, fact string) (tea.Model, tea.Cmd) {
	if len(m.items) == 0 {
		return m, nil
	}
	idx := m.cursor
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	memoryID := m.items[idx].Summary().MemoryID()
	m.removeDecisionFor(memoryID)
	m.decisions = append(m.decisions, reviewDecision{kind: kind, memoryID: memoryID, fact: fact})
	m.reviewed[idx] = decisionLabel(kind)
	m.statusMsg = decisionLabel(kind)
	m.advanceCursor()
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

func decisionLabel(kind reviewDecisionKind) string {
	switch kind {
	case reviewDecisionAccept:
		return "accept"
	case reviewDecisionReject:
		return "reject"
	case reviewDecisionDistill:
		return "distill"
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

// View renders the current screen. The output is intentionally simple:
// the contract is "operator-readable" rather than "pixel-perfect"; rich
// layouts can land in a later refinement once the workflow is exercised
// in dogfood.
func (m reviewModel) View() string {
	if len(m.items) == 0 {
		return m.styles.Title.Render(Localize("inbox review", "inbox review")) +
			"\n\n" +
			m.styles.Subtle.Render(Localize("No candidate durable memories in the inbox.", "inbox に candidate durable memory はありません")) +
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
	}
	return m.renderBrowse()
}

func (m reviewModel) renderBrowse() string {
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(Localize("inbox review", "inbox review")))
	b.WriteString("\n")
	b.WriteString(m.styles.Subtle.Render(Localizef("candidate %d / %d", "candidate %d / %d", m.cursor+1, len(m.items))))
	b.WriteString("\n\n")

	current := m.items[m.cursor]
	summary := current.Summary()
	fmt.Fprintf(&b, "MEMORY_ID: %s\n", summary.MemoryID())
	fmt.Fprintf(&b, "TYPE:      %s\n", summary.MemoryType())
	fmt.Fprintf(&b, "SCOPE:     %s\n", formatMemoryScope(summary.Scope()))
	fmt.Fprintf(&b, "SOURCE:    %s\n", summary.Source())
	fmt.Fprintf(&b, "EVIDENCE:  %d   ARTIFACT: %d\n", len(current.EvidenceRefs()), len(current.ArtifactRefs()))
	b.WriteString("\n")
	b.WriteString(m.styles.Active.Render(Localize("FACT:", "FACT:")))
	b.WriteString("\n")
	b.WriteString(summary.Fact())
	b.WriteString("\n\n")
	if state := m.reviewed[m.cursor]; state != "" {
		b.WriteString(m.styles.Subtle.Render(Localizef("queued action: %s", "予約済みアクション: %s", state)))
		b.WriteString("\n")
	}
	if m.statusMsg != "" {
		b.WriteString(m.styles.Subtle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.styles.Help.Render(Localize(
		"a accept · x reject · s skip · e edit · v evidence · ↑/↓ navigate · ? help · q quit",
		"a accept · x reject · s skip · e edit · v evidence · ↑/↓ 移動 · ? help · q quit",
	)))
	return b.String()
}

func (m reviewModel) renderEvidence() string {
	current := m.items[m.cursor]
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(Localize("evidence", "evidence")))
	b.WriteString("\n\n")
	b.WriteString(Localize("EVIDENCE_REFS:", "EVIDENCE_REFS:"))
	b.WriteString("\n")
	if len(current.EvidenceRefs()) == 0 {
		b.WriteString("- -\n")
	} else {
		for _, ref := range current.EvidenceRefs() {
			fmt.Fprintf(&b, "- %s:%s\n", ref.Kind(), ref.Value())
		}
	}
	b.WriteString("\n")
	b.WriteString(Localize("ARTIFACT_REFS:", "ARTIFACT_REFS:"))
	b.WriteString("\n")
	if len(current.ArtifactRefs()) == 0 {
		b.WriteString("- -\n")
	} else {
		for _, ref := range current.ArtifactRefs() {
			fmt.Fprintf(&b, "- %s:%s\n", ref.Kind(), ref.Value())
		}
	}
	b.WriteString("\n")
	b.WriteString(m.styles.Help.Render(Localize("v / esc back · q quit", "v / esc 戻る · q quit")))
	return b.String()
}

func (m reviewModel) renderHelp() string {
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(Localize("inbox review · help", "inbox review · ヘルプ")))
	b.WriteString("\n\n")
	b.WriteString(Localize("Actions:\n", "アクション:\n"))
	b.WriteString("  a    " + Localize("accept the current candidate", "現在の候補を accept") + "\n")
	b.WriteString("  x    " + Localize("reject the current candidate", "現在の候補を reject") + "\n")
	b.WriteString("  s    " + Localize("skip and move to the next candidate", "skip して次の候補へ") + "\n")
	b.WriteString("  e    " + Localize("edit / distill: type a new operator-authored fact (Enter to commit)", "edit / distill: operator 自身で fact を入力 (Enter で確定)") + "\n")
	b.WriteString("  v    " + Localize("view evidence and artifact refs", "evidence と artifact refs を表示") + "\n")
	b.WriteString("  ?    " + Localize("toggle this help", "このヘルプの表示を切り替え") + "\n")
	b.WriteString("  q    " + Localize("quit and apply queued decisions", "終了して保留中のアクションを実行") + "\n")
	b.WriteString("\n")
	b.WriteString(Localize(
		"Edit / distill never auto-accepts the candidate's fact: the operator must type the durable fact, which is then run through `memory store distill --replace=supersede`.",
		"edit / distill では candidate の fact を自動採用しません。operator が新しい fact を入力した上で `memory store distill --replace=supersede` 経由で記録します。",
	))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Help.Render(Localize("? close help · q quit", "? ヘルプを閉じる · q quit")))
	return b.String()
}

func (m reviewModel) renderEdit() string {
	current := m.items[m.editIndex]
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(Localize("edit · type a new operator-authored fact", "edit · operator が書き起こした fact を入力")))
	b.WriteString("\n\n")
	b.WriteString(m.styles.Subtle.Render(Localize("source candidate fact:", "元の candidate の fact:")))
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

// Decisions returns the queued decisions in operator order so the
// runner can apply them after Bubble Tea exits.
func (m reviewModel) Decisions() []reviewDecision {
	out := make([]reviewDecision, len(m.decisions))
	copy(out, m.decisions)
	return out
}

// inboxReviewIO resolves the stdin/stdout pair the review TUI should drive.
// Tests pass a non-file writer (e.g. *bytes.Buffer) into cobra, which
// makes the type assertion fail and `tui.Interactive` then refuses the
// run — exactly the behavior the non-TTY contract requires.
func inboxReviewIO(output io.Writer) (*os.File, *os.File) {
	stdout, _ := output.(*os.File)
	return os.Stdin, stdout
}
