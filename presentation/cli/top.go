package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

// init pins go-runewidth's East-Asian-ambiguous handling to "narrow"
// so column widths stay deterministic across host locales. Without
// this, characters in the Unicode "ambiguous" category (notably the
// horizontal ellipsis "…") are 1 column on a Posix locale and 2 in
// a CJK locale; that drift would make snapshot golden tests
// environment-dependent and let production output overflow on the
// other locale. We choose narrow because Traceary's output ships
// in markdown / monospace contexts where most fonts render
// ambiguous characters as 1 column.
func init() {
	runewidth.DefaultCondition.EastAsianWidth = false
}

const defaultTopLimit = 500
const shortTopSessionIDLength = 12

var topNowFunc = time.Now

// topSnapshotProfile values control the JSON projection for
// `sessions --snapshot --json` / `top --snapshot --json`.
const (
	topSnapshotProfileOperator = "operator"
	topSnapshotProfileAI       = "ai"
)

// AI-safe pane caps keep the agent-resume envelope small. The operator
// snapshot keeps the larger section limits.
const (
	topAIPaneFailureLimit       = 5
	topAIPaneRecentCommandLimit = 5
	topAIPaneCandidateLimit     = 0 // counts only; no candidate facts
	topAIPaneStaleMemoryLimit   = 0
	topAISessionSampleLimit     = 20
)

type topCommandOptions struct {
	dbPath     string
	workspace  string
	client     string
	agent      string
	idle       time.Duration
	snapshot   bool
	asJSON     bool
	profile    string
	limit      int
	staleAfter time.Duration
	allowStale bool
}

func (c *RootCLI) newTopCommand() *cobra.Command {
	var opts topCommandOptions

	cmd := &cobra.Command{
		Use:   "top",
		Short: Localize("Deprecated compatibility alias for `traceary sessions`; removed in v0.35.", "`traceary sessions` の非推奨互換 alias（v0.35 で削除）"),
		Long: Localize(
			"`traceary top` is a deprecated compatibility alias for `traceary sessions` and will be removed in v0.35. Prefer `traceary sessions` or `traceary sessions --snapshot [--json]`. Bare `top` prints the same text snapshot as `sessions --snapshot`.",
			"`traceary top` は `traceary sessions` の非推奨互換 alias で、v0.35 で削除されます。`traceary sessions` または `traceary sessions --snapshot [--json]` を優先してください。bare の `top` は `sessions --snapshot` と同じ text snapshot を出力します。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runTop(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	applyCommandDeprecation(cmd, "traceary sessions", "v0.35")

	bindTopFlags(cmd, &opts)

	return cmd
}

func (c *RootCLI) newSessionsCommand() *cobra.Command {
	var opts topCommandOptions

	cmd := &cobra.Command{
		Use:   "sessions",
		Short: Localize("Print a one-shot sessions snapshot (active sessions, failures, commands, memory health)", "sessions snapshot を一回出力（active sessions、失敗、コマンド、メモリ状態）"),
		Long: Localize(
			"Print a one-shot Sessions snapshot for active sessions, failures, commands, memory review, and health. Bare `traceary sessions` is byte-identical to `traceary sessions --snapshot`. Use `--snapshot --json` for a JSON envelope with latest-event metadata; `--json` without `--snapshot` is an error.",
			"active session、失敗、コマンド、メモリ確認、状態をまとめた Sessions snapshot を一回出力します。bare の `traceary sessions` は `traceary sessions --snapshot` とバイト単位で同一です。`--snapshot --json` で latest event metadata を含む JSON envelope を出力します。`--json` を `--snapshot` なしで指定するとエラーになります。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessions(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	bindTopFlags(cmd, &opts)

	return cmd
}

func bindTopFlags(cmd *cobra.Command, opts *topCommandOptions) {
	cmd.Flags().StringVar(&opts.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&opts.workspace, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().StringVar(&opts.client, "client", "", Localize("filter by client", "記録経路でフィルタ"))
	cmd.Flags().StringVar(&opts.agent, "agent", "", Localize("filter by agent", "エージェントでフィルタ"))
	cmd.Flags().DurationVar(&opts.idle, "idle", 10*time.Minute, Localize("dim sessions whose latest activity is older than this duration", "最新 activity がこの duration より古い session を dim 表示する"))
	cmd.Flags().BoolVar(&opts.snapshot, "snapshot", false, Localize("print one snapshot and exit", "一回限りの snapshot を出力して終了する"))
	cmd.Flags().BoolVar(&opts.asJSON, "json", false, Localize("print JSON output with --snapshot", "--snapshot と併用して JSON 形式で出力する"))
	cmd.Flags().StringVar(
		&opts.profile,
		"profile",
		topSnapshotProfileOperator,
		Localize(
			"snapshot JSON profile: operator (default full snapshot envelope) or ai (bounded agent-resume envelope)",
			"snapshot JSON の profile: operator（既定のフル snapshot envelope）または ai（AI resume 向けに bound した envelope）",
		),
	)
	cmd.Flags().IntVar(&opts.limit, "limit", defaultTopLimit, Localize("maximum number of sessions to load", "読み込む最大セッション数"))
	cmd.Flags().DurationVar(
		&opts.staleAfter,
		"stale-after",
		defaultActiveSessionStaleAfter,
		Localize(
			"treat unended sessions older than this duration as stale (excluded unless --allow-stale is set)",
			"この duration を超える未終了 session は stale とみなす (--allow-stale を指定しない限り除外)",
		),
	)
	cmd.Flags().BoolVar(
		&opts.allowStale,
		"allow-stale",
		false,
		Localize(
			"include stale active sessions and emit is_stale metadata in JSON snapshots",
			"stale な active session も含めて表示し、JSON snapshot に is_stale メタデータを出力する",
		),
	)
}

// normalizeTopSnapshotProfile returns the canonical profile name or an error
// when the value is not supported.
func normalizeTopSnapshotProfile(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", topSnapshotProfileOperator:
		return topSnapshotProfileOperator, nil
	case topSnapshotProfileAI:
		return topSnapshotProfileAI, nil
	default:
		return "", xerrors.Errorf(
			"%s",
			Localize(
				"--profile must be operator or ai",
				"--profile は operator または ai でなければなりません",
			),
		)
	}
}

func (c *RootCLI) runTop(ctx context.Context, output io.Writer, opts topCommandOptions) error {
	return c.runTopNamed(ctx, output, opts)
}

func (c *RootCLI) runSessions(ctx context.Context, output io.Writer, opts topCommandOptions) error {
	return c.runTopNamed(ctx, output, opts)
}

func (c *RootCLI) runTopNamed(ctx context.Context, output io.Writer, opts topCommandOptions) error {
	resolvedDBPath, err := resolveDBPath(opts.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}
	if opts.limit <= 0 {
		return xerrors.Errorf("%s", Localize("limit must be >= 1", "limit は 1 以上でなければなりません"))
	}
	if opts.idle < 0 {
		return xerrors.Errorf("%s", Localize("idle must be >= 0", "idle は 0 以上でなければなりません"))
	}

	profile, err := normalizeTopSnapshotProfile(opts.profile)
	if err != nil {
		return err
	}
	opts.profile = profile

	// Bare `sessions` / `top` is a plain text command and is byte-identical to
	// `--snapshot`. JSON and the ai profile still require the explicit flag
	// combination so callers cannot accidentally change envelope shape.
	if !opts.snapshot {
		if opts.asJSON {
			return xerrors.Errorf("%s", Localize("--json requires --snapshot", "--json には --snapshot が必要です"))
		}
		if opts.profile == topSnapshotProfileAI {
			return xerrors.Errorf(
				"%s",
				Localize(
					"--profile ai requires --snapshot --json",
					"--profile ai には --snapshot --json が必要です",
				),
			)
		}
	}

	snap, err := c.loadTopSnapshot(ctx, opts)
	if err != nil {
		return err
	}
	if opts.asJSON {
		return writeTopSnapshotJSON(output, snap, opts.profile)
	}
	if opts.profile == topSnapshotProfileAI {
		return xerrors.Errorf(
			"%s",
			Localize(
				"--profile ai requires --snapshot --json",
				"--profile ai には --snapshot --json が必要です",
			),
		)
	}
	return writeTopSnapshotText(output, snap, opts.idle, snap.Now)
}

// loadTopSnapshot fetches the data slices the sessions snapshot surfaces.
// The session section reuses the operator-controlled --limit flag; the
// secondary sections intentionally use the small section caps so the
// script-friendly snapshot does not balloon under a noisy workspace. The ai
// profile uses tighter caps so agent-resume payloads stay bounded.
func (c *RootCLI) loadTopSnapshot(ctx context.Context, opts topCommandOptions) (topDataSnapshot, error) {
	sessionLimit := opts.limit
	failureLimit := topPaneFailureLimit
	commandLimit := topPaneRecentCommandLimit
	candidateLimit := topPaneCandidateLimit
	staleLimit := topPaneStaleMemoryLimit
	if opts.profile == topSnapshotProfileAI {
		if sessionLimit > topAISessionSampleLimit {
			sessionLimit = topAISessionSampleLimit
		}
		failureLimit = topAIPaneFailureLimit
		commandLimit = topAIPaneRecentCommandLimit
		candidateLimit = topAIPaneCandidateLimit
		staleLimit = topAIPaneStaleMemoryLimit
	}
	criteria := topDataCriteria{
		Workspace:          opts.workspace,
		Client:             opts.client,
		Agent:              opts.agent,
		SessionLimit:       sessionLimit,
		FailureLimit:       failureLimit,
		RecentCommandLimit: commandLimit,
		CandidateLimit:     candidateLimit,
		StaleMemoryLimit:   staleLimit,
		StaleAfter:         opts.staleAfter,
		AllowStale:         opts.allowStale,
		Now:                topNowFunc(),
	}
	return c.newTopDataLoader().loadSnapshot(ctx, criteria)
}

// newTopDataLoader builds a topDataLoader bound to the RootCLI's
// configured usecases. Subcommands route their data fetching through
// the loader so the application-layer wiring stays in a single place.
func (c *RootCLI) newTopDataLoader() *topDataLoader {
	return newTopDataLoader(c.session, c.event, c.memory)
}

func buildActiveSessionTreeWithOptions(summaries []apptypes.SessionSummary, allowStale bool, staleAfter time.Duration, now time.Time) []*sessionNode {
	if len(summaries) == 0 {
		return nil
	}
	nodeMap := make(map[string]*sessionNode, len(summaries))
	for _, summary := range summaries {
		nodeMap[summary.SessionID().String()] = &sessionNode{summary: summary}
	}
	roots := make([]*sessionNode, 0)
	for _, summary := range summaries {
		node := nodeMap[summary.SessionID().String()]
		if parentID := summary.ParentSessionID().String(); parentID != "" {
			if parent, ok := nodeMap[parentID]; ok {
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	sortSessionNodes(roots)
	// A non-positive stale threshold disables top's stale filtering. Treat
	// already status=stale rows as allowed here while leaving the generic
	// keepOngoingLineages() default strict for non-top --ongoing-only paths.
	staleAllowed := allowStale || staleAfter <= 0
	return keepOngoingLineagesWithOptions(roots, staleLineageOptions{
		allowStale: staleAllowed,
		staleAfter: staleAfter,
		now:        now,
	})
}

func filterTopSessionTree(roots []*sessionNode, opts topCommandOptions) []*sessionNode {
	if strings.TrimSpace(opts.workspace) == "" && strings.TrimSpace(opts.client) == "" && strings.TrimSpace(opts.agent) == "" {
		return roots
	}
	filtered := make([]*sessionNode, 0, len(roots))
	for _, root := range roots {
		if topLineageMatches(root, opts) {
			filtered = append(filtered, root)
		}
	}
	return filtered
}

func topLineageMatches(node *sessionNode, opts topCommandOptions) bool {
	if topNodeMatches(node, opts) {
		return true
	}
	for _, child := range node.children {
		if topLineageMatches(child, opts) {
			return true
		}
	}
	return false
}

func topNodeMatches(node *sessionNode, opts topCommandOptions) bool {
	s := node.summary
	if workspace := strings.TrimSpace(opts.workspace); workspace != "" && s.Workspace().String() != workspace {
		return false
	}
	if client := strings.TrimSpace(opts.client); client != "" && s.Client().String() != client {
		return false
	}
	if agent := strings.TrimSpace(opts.agent); agent != "" && !sessionSummaryHasAgent(s, agent) {
		return false
	}
	return true
}

func sessionSummaryHasAgent(summary apptypes.SessionSummary, agent string) bool {
	for _, candidate := range summary.Agents() {
		if candidate == agent {
			return true
		}
	}
	return extractSubagentType(summary.Agents()) == agent
}

func writeTopSnapshotText(output io.Writer, snap topDataSnapshot, idle time.Duration, now time.Time) error {
	if err := writeTopSnapshotTextReliability(output, snap.Reliability); err != nil {
		return err
	}
	if err := writeTopSnapshotTextSessions(output, snap.Sessions, idle, now); err != nil {
		return err
	}
	if err := writeTopSnapshotTextEvents(output, "RECENT FAILURES", snap.Failures, now.Location()); err != nil {
		return err
	}
	if err := writeTopSnapshotTextEvents(output, "RECENT COMMANDS", snap.RecentCommands, now.Location()); err != nil {
		return err
	}
	if err := writeTopSnapshotTextCandidates(output, snap.Candidates, snap.RememberIntentCandidateCount); err != nil {
		return err
	}
	return writeTopSnapshotTextStaleMemories(output, snap.StaleMemories)
}

func writeTopSnapshotTextReliability(output io.Writer, metrics topReliabilityMetrics) error {
	if _, err := fmt.Fprintln(output, "RELIABILITY:"); err != nil {
		return xerrors.Errorf("failed to print reliability header: %w", err)
	}
	if _, err := fmt.Fprintf(
		output,
		"- stale_active_sessions=%d",
		metrics.StaleActiveSessionCount,
	); err != nil {
		return xerrors.Errorf("failed to print stale session reliability metric: %w", err)
	}
	if metrics.StaleActiveSessionCount > 0 {
		if _, err := fmt.Fprint(output, " hint=\"run `traceary session gc --stale-after 24h --dry-run`, then drop --dry-run after confirming\""); err != nil {
			return xerrors.Errorf("failed to print stale session reliability hint: %w", err)
		}
	} else if _, err := fmt.Fprint(output, " hint=\"ok\""); err != nil {
		return xerrors.Errorf("failed to print stale session reliability hint: %w", err)
	}
	if _, err := fmt.Fprintln(output); err != nil {
		return xerrors.Errorf("failed to finish stale session reliability metric: %w", err)
	}

	acceptedRatio := "-"
	totalMemories := metrics.AcceptedMemoryCount + metrics.CandidateMemoryCount
	if totalMemories > 0 {
		acceptedRatio = fmt.Sprintf("%.0f%%", float64(metrics.AcceptedMemoryCount)*100/float64(totalMemories))
	}
	limitNote := ""
	if metrics.MemoryScanLimited {
		limitNote = fmt.Sprintf(" scan_limit_reached=%d", metrics.MemoryScanLimit)
	}
	if _, err := fmt.Fprintf(
		output,
		"- memory_counts accepted=%d candidate=%d accepted_ratio=%s%s hint=\"review memory candidates with `traceary memory inbox review` and cleanup old candidates with `traceary memory inbox cleanup --dry-run`\"\n",
		metrics.AcceptedMemoryCount,
		metrics.CandidateMemoryCount,
		acceptedRatio,
		limitNote,
	); err != nil {
		return xerrors.Errorf("failed to print memory reliability metric: %w", err)
	}

	if metrics.CandidateAge.Count == 0 {
		if _, err := fmt.Fprintln(output, "- candidate_age count=0 hint=\"ok\""); err != nil {
			return xerrors.Errorf("failed to print empty candidate age reliability metric: %w", err)
		}
	} else if _, err := fmt.Fprintf(
		output,
		"- candidate_age count=%d oldest=%s newest=%s avg_age=%s hint=\"prioritize older memory candidates first\"\n",
		metrics.CandidateAge.Count,
		formatJSONTime(metrics.CandidateAge.Oldest),
		formatJSONTime(metrics.CandidateAge.Newest),
		formatDuration(metrics.CandidateAge.AverageAge),
	); err != nil {
		return xerrors.Errorf("failed to print candidate age reliability metric: %w", err)
	}

	if _, err := fmt.Fprintf(
		output,
		"- large_payloads count=%d recent_commands=%d recent_failures=%d sampled=%d body_limit=%d hint=\"inspect full payloads with `traceary show <event_id>`; keep command output concise for handoff/top surfaces\"\n\n",
		metrics.LargePayloads.Count,
		metrics.LargePayloads.RecentCommandCount,
		metrics.LargePayloads.RecentFailureCount,
		metrics.LargePayloads.SampledEventCount,
		metrics.LargePayloads.BodyLimitRunes,
	); err != nil {
		return xerrors.Errorf("failed to print large payload reliability metric: %w", err)
	}
	return nil
}

func writeTopSnapshotTextSessions(output io.Writer, roots []*sessionNode, idle time.Duration, now time.Time) error {
	if _, err := fmt.Fprintln(output, "ACTIVE SESSIONS:"); err != nil {
		return xerrors.Errorf("failed to print active sessions header: %w", err)
	}
	if len(roots) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No active sessions found.", "active session が見つかりません")); err != nil {
			return xerrors.Errorf("failed to print empty active sessions message: %w", err)
		}
		return nil
	}
	for _, root := range roots {
		if err := printTopNode(output, root, "", true, idle, now, false); err != nil {
			return err
		}
	}
	return nil
}

func writeTopSnapshotTextEvents(output io.Writer, header string, events []*model.Event, loc *time.Location) error {
	if _, err := fmt.Fprintf(output, "\n%s:\n", header); err != nil {
		return xerrors.Errorf("failed to print %s header: %w", header, err)
	}
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No matching records.", "一致する記録はありません")); err != nil {
			return xerrors.Errorf("failed to print empty %s message: %w", header, err)
		}
		return nil
	}
	for _, ev := range events {
		ts := ev.CreatedAt().In(loc).Format(eventCompactTimeLayout)
		if _, err := fmt.Fprintf(output, "%s %s %s\n", ts, ev.Kind(), truncateMessage(eventBodyForDisplay(ev))); err != nil {
			return xerrors.Errorf("failed to print %s row: %w", header, err)
		}
	}
	return nil
}

func writeTopSnapshotTextCandidates(output io.Writer, candidates []apptypes.MemorySummary, rememberIntentCount int) error {
	// This script-facing text header is intentionally stable. Do not rename it
	// without a sessions --snapshot contract migration.
	if _, err := fmt.Fprintf(output, "\nCANDIDATE MEMORIES (count=%d remember_intent=%d):\n", len(candidates), rememberIntentCount); err != nil {
		return xerrors.Errorf("failed to print candidates header: %w", err)
	}
	if len(candidates) == 0 {
		if _, err := fmt.Fprintln(output, memoryReviewEmptyQueueMessage()); err != nil {
			return xerrors.Errorf("failed to print empty candidates message: %w", err)
		}
		return nil
	}
	for _, candidate := range candidates {
		if _, err := fmt.Fprintf(output, "%s %s %s\n", candidate.MemoryID(), candidate.MemoryType(), truncateMessage(candidate.Fact())); err != nil {
			return xerrors.Errorf("failed to print candidate row: %w", err)
		}
	}
	return nil
}

func writeTopSnapshotTextStaleMemories(output io.Writer, stale apptypes.StaleMemoryListResult) error {
	if _, err := fmt.Fprintf(output, "\nSTALE MEMORIES (count=%d):\n", stale.Count()); err != nil {
		return xerrors.Errorf("failed to print stale memories header: %w", err)
	}
	items := stale.Items()
	if len(items) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No stale memories.", "stale な memory はありません。")); err != nil {
			return xerrors.Errorf("failed to print empty stale memories message: %w", err)
		}
		return nil
	}
	for _, row := range items {
		summary := row.Summary()
		if _, err := fmt.Fprintf(
			output,
			"%s %s %s %s %s\n",
			summary.MemoryID(),
			summary.MemoryType(),
			formatMemoryScope(summary.Scope()),
			row.Reason(),
			truncateMessage(summary.Fact()),
		); err != nil {
			return xerrors.Errorf("failed to print stale memory row: %w", err)
		}
	}
	return nil
}

func printTopNode(output io.Writer, node *sessionNode, prefix string, isLast bool, idle time.Duration, now time.Time, hasParent bool) error {
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if !hasParent {
		connector = ""
	}
	line := formatTopNodeLine(node, prefix+connector, idle, now)
	if _, err := fmt.Fprintln(output, line); err != nil {
		return xerrors.Errorf("failed to print top node: %w", err)
	}
	childPrefix := prefix
	if hasParent {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	for i, child := range node.children {
		if err := printTopNode(output, child, childPrefix, i == len(node.children)-1, idle, now, true); err != nil {
			return err
		}
	}
	return nil
}

func formatTopNodeLine(node *sessionNode, prefix string, idle time.Duration, now time.Time) string {
	return formatTopNodeLineIn(node, prefix, idle, now, time.Local)
}

// formatTopNodeLineIn renders the row in the supplied location so
// tests can assert against a deterministic timezone without mutating
// the global time.Local. Production callers go through
// formatTopNodeLine which pins it to time.Local.
func formatTopNodeLineIn(node *sessionNode, prefix string, idle time.Duration, now time.Time, loc *time.Location) string {
	s := node.summary
	latest := s.LatestEventAt()
	idleFor := now.Sub(latest)
	idleMarker := ""
	if idle > 0 && idleFor >= idle {
		idleMarker = " idle"
	}
	agent := extractSubagentType(s.Agents())
	if agent == "" {
		agent = "-"
	}
	client := s.Client().String()
	if client == "" {
		client = "-"
	}
	return fmt.Sprintf("%s%s name=%q workspace=%s agent=%s client=%s started=%s latest=%s events=%d last=%s%s",
		prefix,
		s.SessionID(),
		formatTopSessionDisplayName(s),
		compactTopWorkspace(s.Workspace().String()),
		agent,
		client,
		s.StartedAt().In(loc).Format("15:04:05"),
		latest.In(loc).Format("15:04:05"),
		s.TotalEvents(),
		formatTopLatestEvent(s),
		idleMarker,
	)
}

func formatTopSessionDisplayName(s apptypes.SessionSummary) string {
	for _, candidate := range []string{s.Label(), s.Summary()} {
		if name := truncateTopDisplayName(candidate); name != "" {
			return name
		}
	}

	workspace := compactTopWorkspace(s.Workspace().String())
	agent := extractSubagentType(s.Agents())
	if agent == "" {
		agent = strings.TrimSpace(s.SubagentKind())
	}
	switch {
	case workspace != "-" && agent != "":
		return truncateTopDisplayName(workspace + " · " + agent)
	case workspace != "-":
		return truncateTopDisplayName(workspace)
	case agent != "":
		return truncateTopDisplayName(agent)
	default:
		return shortTopSessionID(s.SessionID().String())
	}
}

func truncateTopDisplayName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return truncateMessage(value)
}

func shortTopSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "-"
	}
	runes := []rune(id)
	if len(runes) <= shortTopSessionIDLength {
		return id
	}
	return string(runes[:shortTopSessionIDLength])
}

// topWorkspaceMaxWidth is the column budget for the workspace cell
// in `traceary top` rows. The truncate strategy preserves the tail
// (the repo identifier) so that `github.com/owner/repo` paths stay
// readable even when truncated.
const topWorkspaceMaxWidth = 36

// compactTopWorkspace renders a workspace path for the sessions snapshot.
// Unlike compactWorkspace (basename only), the snapshot needs to keep the
// owner/repo qualifier so users can tell parallel sessions apart, so this
// preserves the tail and prepends an ellipsis when the value is wider than
// topWorkspaceMaxWidth columns. The budget is measured in visual columns
// (East Asian Wide characters count as 2) so a CJK-heavy workspace does not
// overflow the cell.
func compactTopWorkspace(workspace string) string {
	normalized := normalizeTabularColumn(workspace)
	if normalized == "" {
		return "-"
	}
	if runewidth.StringWidth(normalized) <= topWorkspaceMaxWidth {
		return normalized
	}
	// Truncate from the head while keeping the tail (repo identifier)
	// readable. Walk runes right-to-left until adding another rune
	// would push us past the column budget. The leading "…" itself
	// claims a variable number of columns (1 in most fonts, 2 under
	// East Asian Ambiguous width); reserve that many columns from
	// the budget.
	const ellipsis = '…'
	ellipsisWidth := runewidth.RuneWidth(ellipsis)
	budget := topWorkspaceMaxWidth - ellipsisWidth
	if budget < 0 {
		budget = 0
	}
	runes := []rune(normalized)
	width := 0
	cut := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		w := runewidth.RuneWidth(runes[i])
		if width+w > budget {
			break
		}
		width += w
		cut = i
	}
	return string(ellipsis) + string(runes[cut:])
}

func formatTopLatestEvent(s apptypes.SessionSummary) string {
	if s.TotalEvents() == 0 || s.LatestEventKind().String() == "" {
		return "-"
	}
	return fmt.Sprintf("%s: %s", s.LatestEventKind(), truncateMessage(s.LatestEventMessage()))
}

// runeWidth returns the visual column width of s, accounting for
// East Asian Wide characters (CJK ideographs / kana / hangul) that
// occupy two terminal cells. This replaces the prior rune-count
// approximation which broke tree-prefix alignment when a workspace
// or message contained wide characters.
func runeWidth(s string) int {
	return runewidth.StringWidth(s)
}
