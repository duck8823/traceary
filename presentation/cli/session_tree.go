package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionTreeCommand() *cobra.Command {
	var (
		dbPath      string
		repo        string
		limit       int
		asJSON      bool
		rootID      string
		ongoingOnly bool
	)

	cmd := &cobra.Command{
		Use:   "tree",
		Short: Localize("Display session parent-child hierarchy", "セッションの親子階層を表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			output := cmd.OutOrStdout()

			resolvedDBPath, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}
			c.applyDatabasePath(resolvedDBPath)
			if err := c.storeManagement.Initialize(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			resolvedRepo := resolveWorkspaceValue(ctx, repo)

			summaries, err := c.session.Tree(ctx, types.Workspace(resolvedRepo), types.SessionID(strings.TrimSpace(rootID)), limit)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
			}

			return writeSessionTreeFiltered(output, summaries, sessionTreeFilter{
				root:        rootID,
				ongoingOnly: ongoingOnly,
				asJSON:      asJSON,
			})
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().IntVar(&limit, "limit", 50, Localize("maximum number of sessions to load", "読み込む最大セッション数"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	cmd.Flags().StringVar(&rootID, "root", "", Localize(
		"focus on the subtree rooted at the given session id (printed as the tree root when provided)",
		"指定 session id を root とするサブツリーだけを表示する",
	))
	cmd.Flags().BoolVar(&ongoingOnly, "ongoing-only", false, Localize(
		"keep only lineages that contain at least one active session",
		"active session を少なくとも1つ含む lineage だけに絞る",
	))

	return cmd
}

func (c *RootCLI) newSessionLineageCommand() *cobra.Command {
	var (
		dbPath string
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "lineage <session-id>",
		Short: Localize("Display the full lineage containing a session", "指定セッションを含む lineage 全体を表示する"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			output := cmd.OutOrStdout()

			resolvedDBPath, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}
			c.applyDatabasePath(resolvedDBPath)
			if err := c.storeManagement.Initialize(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			summaries, err := c.session.Lineage(ctx, types.SessionID(strings.TrimSpace(args[0])))
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to get session lineage", "セッション lineage の取得に失敗しました"), err)
			}

			return writeSessionTreeFiltered(output, summaries, sessionTreeFilter{asJSON: asJSON})
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return cmd
}

type sessionTreeFilter struct {
	root        string
	ongoingOnly bool
	asJSON      bool
}

type sessionNode struct {
	summary  apptypes.SessionSummary
	children []*sessionNode
}

func writeSessionTreeFiltered(output io.Writer, summaries []apptypes.SessionSummary, filter sessionTreeFilter) error {
	if len(summaries) == 0 {
		return writeSessionTreeEmpty(output, filter.asJSON)
	}

	// Build tree
	nodeMap := make(map[string]*sessionNode, len(summaries))
	var roots []*sessionNode

	for _, s := range summaries {
		node := &sessionNode{summary: s}
		nodeMap[s.SessionID().String()] = node
	}

	for _, s := range summaries {
		node := nodeMap[s.SessionID().String()]
		if s.ParentSessionID().String() != "" {
			if parent, ok := nodeMap[s.ParentSessionID().String()]; ok {
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	sortSessionNodes(roots)

	if filter.root != "" {
		if rootNode, ok := nodeMap[filter.root]; ok {
			roots = []*sessionNode{rootNode}
		} else {
			// Requested root not in the loaded window; treat as empty
			// result rather than silently printing every root.
			return writeSessionTreeEmpty(output, filter.asJSON)
		}
	}

	if filter.ongoingOnly {
		roots = keepOngoingLineages(roots)
		if len(roots) == 0 {
			return writeSessionTreeEmpty(output, filter.asJSON)
		}
	}

	if filter.asJSON {
		return writeSessionTreeJSON(output, roots)
	}

	for _, root := range roots {
		if err := printNode(output, root, "", true); err != nil {
			return err
		}
	}

	return nil
}

func sortSessionNodes(nodes []*sessionNode) {
	sortSessionNodesWithVisited(nodes, map[string]bool{})
}

func sortSessionNodesWithVisited(nodes []*sessionNode, visited map[string]bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return sessionNodeLess(nodes[i], nodes[j])
	})
	for _, node := range nodes {
		sessionID := node.summary.SessionID().String()
		if visited[sessionID] {
			continue
		}
		visited[sessionID] = true
		sortSessionNodesWithVisited(node.children, visited)
		delete(visited, sessionID)
	}
}

func sessionNodeLess(left, right *sessionNode) bool {
	leftOrder, leftHasOrder := left.summary.SpawnOrder().Value()
	rightOrder, rightHasOrder := right.summary.SpawnOrder().Value()
	if leftHasOrder && rightHasOrder && leftOrder != rightOrder {
		return leftOrder < rightOrder
	}
	if leftHasOrder != rightHasOrder {
		return leftHasOrder
	}
	if !left.summary.StartedAt().Equal(right.summary.StartedAt()) {
		return left.summary.StartedAt().Before(right.summary.StartedAt())
	}
	return false
}

// keepOngoingLineages prunes every subtree whose sessions are all ended so
// --ongoing-only surfaces active work without hiding the static lineage
// around it. A parent is retained when either the parent itself is active
// or any descendant is.
func keepOngoingLineages(roots []*sessionNode) []*sessionNode {
	return keepOngoingLineagesWithOptions(roots, staleLineageOptions{})
}

type staleLineageOptions struct {
	allowStale bool
	staleAfter time.Duration
	now        time.Time
}

func keepOngoingLineagesWithOptions(roots []*sessionNode, opts staleLineageOptions) []*sessionNode {
	filtered := make([]*sessionNode, 0, len(roots))
	for _, root := range roots {
		if pruneEndedLineages(root, opts) {
			filtered = append(filtered, root)
		}
	}
	return filtered
}

func pruneEndedLineages(node *sessionNode, opts staleLineageOptions) bool {
	keptChildren := node.children[:0]
	for _, child := range node.children {
		if pruneEndedLineages(child, opts) {
			keptChildren = append(keptChildren, child)
		}
	}
	node.children = keptChildren
	return isSessionActive(node.summary) || isSessionStaleAllowed(node.summary, opts) || len(node.children) > 0
}

// isSessionActive treats active and ended_with_late_events sessions as live.
// A session that has not received an end event but has aged past the store
// timeout is already marked status=stale by the SQLite datasource, and
// --ongoing-only explicitly promises "at least one active session" — so
// stale lineages must not resurface as ongoing work. ended_with_late_events
// counts as live because events arrived after the end marker, meaning the
// session kept going; dropping it would reproduce the #1172 empty snapshot.
func isSessionActive(summary apptypes.SessionSummary) bool {
	switch types.SessionStatus(summary.Status()) {
	case types.SessionStatusActive, types.SessionStatusEndedWithLateEvents:
		return true
	default:
		return false
	}
}

func isSessionStale(summary apptypes.SessionSummary) bool {
	return types.SessionStatus(summary.Status()) == types.SessionStatusStale
}

func isSessionStaleAllowed(summary apptypes.SessionSummary, opts staleLineageOptions) bool {
	if !isSessionStale(summary) {
		return false
	}
	if opts.allowStale {
		return true
	}
	if opts.staleAfter <= 0 {
		return false
	}
	now := opts.now
	if now.IsZero() {
		now = time.Now()
	}
	return !topDataSummaryIsStale(summary, opts.staleAfter, now)
}

func writeSessionTreeEmpty(output io.Writer, asJSON bool) error {
	if asJSON {
		if _, err := fmt.Fprintln(output, "[]"); err != nil {
			return xerrors.Errorf("failed to print empty JSON array: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintln(output, Localize("No sessions found.", "セッションが見つかりません")); err != nil {
		return xerrors.Errorf("failed to print empty message: %w", err)
	}
	return nil
}

func sessionNodeToOutput(node *sessionNode, depth int) *sessionTreeNode {
	return sessionNodeToOutputWithVisited(node, depth, map[string]bool{})
}

func sessionNodeToOutputWithVisited(node *sessionNode, depth int, visited map[string]bool) *sessionTreeNode {
	s := node.summary
	jn := &sessionTreeNode{
		SessionID:       string(s.SessionID()),
		ParentSessionID: s.ParentSessionID().String(),
		SpawnEventID:    s.SpawnEventID().String(),
		SubagentKind:    s.SubagentKind(),
		Depth:           depth,
		Workspace:       string(s.Workspace()),
		Label:           s.Label(),
		Summary:         s.Summary(),
		StartedAt:       formatJSONTime(s.StartedAt()),
		Status:          s.Status(),
		TotalEvents:     s.TotalEvents(),
		CommandCount:    s.CommandCount(),
		Agents:          s.Agents(),
		SubagentType:    extractSubagentType(s.Agents()),
		Children:        make([]*sessionTreeNode, 0, len(node.children)),
	}
	if spawnOrder, ok := s.SpawnOrder().Value(); ok {
		jn.SpawnOrder = &spawnOrder
	}
	if endedAt, ok := s.EndedAt().Value(); ok {
		endStr := formatJSONTime(endedAt)
		jn.EndedAt = &endStr
		dur := endedAt.Sub(s.StartedAt())
		secs := dur.Seconds()
		jn.DurationSec = &secs
	}
	if visited[s.SessionID().String()] {
		jn.Status = "cycle-detected"
		return jn
	}
	visited[s.SessionID().String()] = true
	for _, child := range node.children {
		jn.Children = append(jn.Children, sessionNodeToOutputWithVisited(child, depth+1, visited))
	}
	delete(visited, s.SessionID().String())
	return jn
}

// extractSubagentType returns the most specific subagent role the session
// participated in. Hook capture writes hierarchical agent names as
// `<client>/<role>` (for example `claude/Explore` or `claude/planner`), so
// tree consumers want a single string to colour or filter on; the helper
// picks the first entry that carries a `/role` suffix and falls back to
// the first agent when none do. The helper always returns an empty string
// for no-agent sessions so the JSON field is omitted via `omitempty`.
func extractSubagentType(agents []string) string {
	if len(agents) == 0 {
		return ""
	}
	for _, agent := range agents {
		if strings.Contains(agent, "/") {
			return agent
		}
	}
	return agents[0]
}

func writeSessionTreeJSON(output io.Writer, roots []*sessionNode) error {
	items := make([]*sessionTreeNode, 0, len(roots))
	for _, root := range roots {
		items = append(items, sessionNodeToOutput(root, 0))
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		return xerrors.Errorf("failed to encode JSON: %w", err)
	}
	return nil
}

func printNode(output io.Writer, node *sessionNode, prefix string, isLast bool) error {
	return printNodeWithVisited(output, node, prefix, isLast, map[string]bool{})
}

func printNodeWithVisited(output io.Writer, node *sessionNode, prefix string, isLast bool, visited map[string]bool) error {
	s := node.summary

	connector := "\u251c\u2500\u2500 "
	if isLast {
		connector = "\u2514\u2500\u2500 "
	}
	if prefix == "" {
		connector = ""
	}

	duration := "-"
	if endedAt, ok := s.EndedAt().Value(); ok {
		duration = formatDuration(endedAt.Sub(s.StartedAt()))
	}

	label := ""
	if s.Label() != "" {
		label = fmt.Sprintf(" [%s]", s.Label())
	}

	subagent := extractSubagentType(s.Agents())
	subagentFragment := ""
	if subagent != "" {
		subagentFragment = " " + subagent
	}

	status := s.Status()
	cycleDetected := visited[s.SessionID().String()]
	if cycleDetected {
		status = "cycle-detected"
	}

	line := fmt.Sprintf("%s%s%s [%s]%s %s (%s, %d cmds/%d events)%s\n",
		prefix, connector,
		s.SessionID(),
		status,
		subagentFragment,
		formatOptionalColumn(s.Workspace().String()),
		duration,
		s.CommandCount(),
		s.TotalEvents(),
		label,
	)

	if _, err := fmt.Fprint(output, line); err != nil {
		return xerrors.Errorf("failed to print tree node: %w", err)
	}

	childPrefix := prefix
	if prefix != "" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "\u2502   "
		}
	}

	if cycleDetected {
		return nil
	}
	visited[s.SessionID().String()] = true
	for i, child := range node.children {
		if err := printNodeWithVisited(output, child, childPrefix, i == len(node.children)-1, visited); err != nil {
			return err
		}
	}
	delete(visited, s.SessionID().String())

	return nil
}
