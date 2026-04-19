package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionTreeCommand() *cobra.Command {
	var (
		dbPath       string
		repo         string
		limit        int
		asJSON       bool
		rootID       string
		ongoingOnly  bool
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

			criteria := apptypes.NewSessionListCriteriaBuilder(limit).
				Workspace(types.Workspace(resolvedRepo)).
				Build()
			summaries, err := c.session.List(ctx, criteria)
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

// keepOngoingLineages prunes every subtree whose sessions are all ended so
// --ongoing-only surfaces active work without hiding the static lineage
// around it. A parent is retained when either the parent itself is active
// or any descendant is.
func keepOngoingLineages(roots []*sessionNode) []*sessionNode {
	filtered := make([]*sessionNode, 0, len(roots))
	for _, root := range roots {
		if pruneEndedLineages(root) {
			filtered = append(filtered, root)
		}
	}
	return filtered
}

func pruneEndedLineages(node *sessionNode) bool {
	keptChildren := node.children[:0]
	for _, child := range node.children {
		if pruneEndedLineages(child) {
			keptChildren = append(keptChildren, child)
		}
	}
	node.children = keptChildren
	if isSessionActive(node.summary) {
		return true
	}
	return len(node.children) > 0
}

func isSessionActive(summary apptypes.SessionSummary) bool {
	if summary.Status() == "active" {
		return true
	}
	_, ended := summary.EndedAt().Value()
	return !ended
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
	s := node.summary
	jn := &sessionTreeNode{
		SessionID:       string(s.SessionID()),
		ParentSessionID: s.ParentSessionID().String(),
		Depth:           depth,
		Workspace:       string(s.Workspace()),
		Label:           s.Label(),
		Summary:         s.Summary(),
		StartedAt:       s.StartedAt().UTC().Format(time.RFC3339),
		Status:          s.Status(),
		TotalEvents:     s.TotalEvents(),
		CommandCount:    s.CommandCount(),
		Agents:          s.Agents(),
		SubagentType:    extractSubagentType(s.Agents()),
		Children:        make([]*sessionTreeNode, 0, len(node.children)),
	}
	if endedAt, ok := s.EndedAt().Value(); ok {
		endStr := endedAt.UTC().Format(time.RFC3339)
		jn.EndedAt = &endStr
		dur := endedAt.Sub(s.StartedAt())
		secs := dur.Seconds()
		ms := dur.Milliseconds()
		jn.DurationSec = &secs
		jn.DurationMs = &ms
	}
	for _, child := range node.children {
		jn.Children = append(jn.Children, sessionNodeToOutput(child, depth+1))
	}
	return jn
}

// extractSubagentType returns the most specific subagent role the session
// participated in. The captured agents slice stores values like "claude" or
// "claude:explore"; tree consumers want a single string to colour or filter
// on, so the helper picks the first entry that carries a `:role` suffix
// and falls back to the first agent when none do. The helper always
// returns an empty string for no-agent sessions so the JSON field is
// omitted via `omitempty`.
func extractSubagentType(agents []string) string {
	if len(agents) == 0 {
		return ""
	}
	for _, agent := range agents {
		if strings.Contains(agent, ":") {
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

	line := fmt.Sprintf("%s%s%s [%s]%s %s (%s, %d cmds/%d events)%s\n",
		prefix, connector,
		s.SessionID(),
		s.Status(),
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

	for i, child := range node.children {
		if err := printNode(output, child, childPrefix, i == len(node.children)-1); err != nil {
			return err
		}
	}

	return nil
}
