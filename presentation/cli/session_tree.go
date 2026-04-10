package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

func (c *RootCLI) newSessionTreeCommand() *cobra.Command {
	var (
		dbPath string
		repo   string
		limit  int
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
			if err := c.initializeStoreUsecase.Run(ctx, resolvedDBPath); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
			}

			resolvedRepo := resolveRepoValue(ctx, repo)

			summaries, err := c.listSessionsQueryService.Run(ctx, resolvedDBPath, port.ListSessionsInput{
				Limit: limit,
				Repo:  resolvedRepo,
			})
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
			}

			return writeSessionTree(output, summaries)
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&repo, "repo", "", Localize("filter by repo", "リポジトリでフィルタ"))
	cmd.Flags().IntVar(&limit, "limit", 50, Localize("maximum number of sessions to load", "読み込む最大セッション数"))

	return cmd
}

type sessionNode struct {
	summary  *port.SessionSummary
	children []*sessionNode
}

func writeSessionTree(output io.Writer, summaries []*port.SessionSummary) error {
	if len(summaries) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No sessions found.", "セッションが見つかりません")); err != nil {
			return xerrors.Errorf("failed to print empty message: %w", err)
		}
		return nil
	}

	// Build tree
	nodeMap := make(map[string]*sessionNode, len(summaries))
	var roots []*sessionNode

	for _, s := range summaries {
		nodeMap[s.SessionID] = &sessionNode{summary: s}
	}

	for _, s := range summaries {
		node := nodeMap[s.SessionID]
		if s.ParentSessionID != "" {
			if parent, ok := nodeMap[s.ParentSessionID]; ok {
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	for _, root := range roots {
		if err := printNode(output, root, "", true); err != nil {
			return err
		}
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
		formatOptionalColumn(s.Repo),
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
