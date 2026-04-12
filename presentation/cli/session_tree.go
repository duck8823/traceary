package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionTreeCommand() *cobra.Command {
	var (
		dbPath string
		repo   string
		limit  int
		asJSON bool
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

			return writeSessionTree(output, summaries, asJSON)
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().IntVar(&limit, "limit", 50, Localize("maximum number of sessions to load", "読み込む最大セッション数"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return cmd
}

type sessionNode struct {
	summary  apptypes.SessionSummary
	children []*sessionNode
}

func writeSessionTree(output io.Writer, summaries []apptypes.SessionSummary, asJSON bool) error {
	if len(summaries) == 0 {
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

	if asJSON {
		return writeSessionTreeJSON(output, roots)
	}

	for _, root := range roots {
		if err := printNode(output, root, "", true); err != nil {
			return err
		}
	}

	return nil
}

func sessionNodeToOutput(node *sessionNode) *sessionTreeNode {
	s := node.summary
	jn := &sessionTreeNode{
		SessionID:    string(s.SessionID()),
		Workspace:    string(s.Workspace()),
		Label:        s.Label(),
		Summary:      s.Summary(),
		StartedAt:    s.StartedAt().UTC().Format(time.RFC3339),
		Status:       s.Status(),
		TotalEvents:  s.TotalEvents(),
		CommandCount: s.CommandCount(),
		Agents:       s.Agents(),
		Children:     make([]*sessionTreeNode, 0, len(node.children)),
	}
	if endedAt, ok := s.EndedAt().Get(); ok {
		endStr := endedAt.UTC().Format(time.RFC3339)
		jn.EndedAt = &endStr
		dur := endedAt.Sub(s.StartedAt()).Seconds()
		jn.DurationSec = &dur
	}
	for _, child := range node.children {
		jn.Children = append(jn.Children, sessionNodeToOutput(child))
	}
	return jn
}

func writeSessionTreeJSON(output io.Writer, roots []*sessionNode) error {
	items := make([]*sessionTreeNode, 0, len(roots))
	for _, root := range roots {
		items = append(items, sessionNodeToOutput(root))
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
	if endedAt, ok := s.EndedAt().Get(); ok {
		duration = formatDuration(endedAt.Sub(s.StartedAt()))
	}

	label := ""
	if s.Label() != "" {
		label = fmt.Sprintf(" [%s]", s.Label())
	}

	line := fmt.Sprintf("%s%s%s [%s] %s (%s, %d cmds)%s\n",
		prefix, connector,
		s.SessionID(),
		s.Status(),
		formatOptionalColumn(s.Workspace().String()),
		duration,
		s.CommandCount(),
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
