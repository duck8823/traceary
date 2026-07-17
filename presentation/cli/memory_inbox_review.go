package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
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
