package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
	"github.com/duck8823/traceary/application/usecase"

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

			_, err := resolveDBPath(dbPath)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
			}
			if err := c.storeMaintenance.Initialize(ctx); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			resolvedRepo := resolveWorkspaceValue(ctx, repo)

			summaries, err := c.session.List(ctx, usecase.SessionListCriteria{
				Limit: limit,
				Workspace: types.Workspace(resolvedRepo),
			})
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
			}

			return writeSessionTree(output, summaries, asJSON)
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by repo", "リポジトリでフィルタ"))
	cmd.Flags().IntVar(&limit, "limit", 50, Localize("maximum number of sessions to load", "読み込む最大セッション数"))
	cmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return cmd
}

type sessionNode struct {
	summary  *usecase.SessionSummary
	children []*sessionNode
}

func writeSessionTree(output io.Writer, summaries []*usecase.SessionSummary, asJSON bool) error {
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
		nodeMap[s.SessionID.String()] = &sessionNode{summary: s}
	}

	for _, s := range summaries {
		node := nodeMap[s.SessionID.String()]
		if s.ParentSessionID.String() != "" {
			if parent, ok := nodeMap[s.ParentSessionID.String()]; ok {
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

type jsonTreeNode struct {
	SessionID    string          `json:"session_id"`
	Workspace string          `json:"repo,omitempty"`
	Label        string          `json:"label,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	StartedAt    string          `json:"started_at"`
	EndedAt      *string         `json:"ended_at,omitempty"`
	Status       string          `json:"status"`
	DurationSec  *float64        `json:"duration_sec,omitempty"`
	TotalEvents  int             `json:"total_events"`
	CommandCount int             `json:"command_count"`
	Agents       []string        `json:"agents"`
	Children     []*jsonTreeNode `json:"children"`
}

func sessionNodeToJSON(node *sessionNode) *jsonTreeNode {
	s := node.summary
	jn := &jsonTreeNode{
		SessionID: string(s.SessionID),
		Workspace: string(s.Workspace),
		Label:        s.Label,
		Summary:      s.Summary,
		StartedAt:    s.StartedAt.UTC().Format(time.RFC3339),
		Status:       s.Status,
		TotalEvents:  s.TotalEvents,
		CommandCount: s.CommandCount,
		Agents:       s.Agents,
		Children:     make([]*jsonTreeNode, 0, len(node.children)),
	}
	if s.EndedAt != nil {
		endStr := s.EndedAt.UTC().Format(time.RFC3339)
		jn.EndedAt = &endStr
		dur := s.EndedAt.Sub(s.StartedAt).Seconds()
		jn.DurationSec = &dur
	}
	for _, child := range node.children {
		jn.Children = append(jn.Children, sessionNodeToJSON(child))
	}
	return jn
}

func writeSessionTreeJSON(output io.Writer, roots []*sessionNode) error {
	items := make([]*jsonTreeNode, 0, len(roots))
	for _, root := range roots {
		items = append(items, sessionNodeToJSON(root))
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

	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if prefix == "" {
		connector = ""
	}

	duration := "-"
	if s.EndedAt != nil {
		duration = formatDuration(s.EndedAt.Sub(s.StartedAt))
	}

	label := ""
	if s.Label != "" {
		label = fmt.Sprintf(" [%s]", s.Label)
	}

	line := fmt.Sprintf("%s%s%s [%s] %s (%s, %d cmds)%s\n",
		prefix, connector,
		s.SessionID,
		s.Status,
		formatOptionalColumn(s.Workspace.String()),
		duration,
		s.CommandCount,
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
			childPrefix += "│   "
		}
	}

	for i, child := range node.children {
		if err := printNode(output, child, childPrefix, i == len(node.children)-1); err != nil {
			return err
		}
	}

	return nil
}
