package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const defaultTopLimit = 500

type topCommandOptions struct {
	dbPath    string
	workspace string
	client    string
	agent     string
	idle      time.Duration
	snapshot  bool
	asJSON    bool
	limit     int
}

func (c *RootCLI) newTopCommand() *cobra.Command {
	var opts topCommandOptions

	cmd := &cobra.Command{
		Use:   "top",
		Short: Localize("Live active session tree dashboard", "active session tree のライブダッシュボードを表示する"),
		Long: Localize(
			"Show a live, auto-refreshing tree of active sessions grouped by root session. Press q or Ctrl-C to quit. Use --snapshot --json for a one-shot JSON tree that shares the session tree JSON contract.",
			"active session を root session ごとにまとめたライブ自動更新 tree を表示します。q または Ctrl-C で終了します。--snapshot --json で session tree と同じ JSON 契約の一回限りの tree を出力します。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runTop(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&opts.workspace, "workspace", "", Localize("filter by workspace", "ワークスペースでフィルタ"))
	cmd.Flags().StringVar(&opts.client, "client", "", Localize("filter by client", "記録経路でフィルタ"))
	cmd.Flags().StringVar(&opts.agent, "agent", "", Localize("filter by agent", "エージェントでフィルタ"))
	cmd.Flags().DurationVar(&opts.idle, "idle", 10*time.Minute, Localize("dim sessions whose latest activity is older than this duration", "最新 activity がこの duration より古い session を dim 表示する"))
	cmd.Flags().BoolVar(&opts.snapshot, "snapshot", false, Localize("print one snapshot and exit", "一回限りの snapshot を出力して終了する"))
	cmd.Flags().BoolVar(&opts.asJSON, "json", false, Localize("print JSON output with --snapshot", "--snapshot と併用して JSON 形式で出力する"))
	cmd.Flags().IntVar(&opts.limit, "limit", defaultTopLimit, Localize("maximum number of sessions to load", "読み込む最大セッション数"))

	return cmd
}

func (c *RootCLI) runTop(ctx context.Context, output io.Writer, opts topCommandOptions) error {
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

	if opts.snapshot {
		roots, err := c.loadTopSessionTree(ctx, opts)
		if err != nil {
			return err
		}
		if opts.asJSON {
			return writeSessionTreeJSON(output, roots)
		}
		return writeTopSnapshotText(output, roots, opts.idle, time.Now())
	}
	if opts.asJSON {
		return xerrors.Errorf("%s", Localize("--json requires --snapshot", "--json には --snapshot が必要です"))
	}
	return c.runTopTUI(ctx, opts)
}

func (c *RootCLI) loadTopSessionTree(ctx context.Context, opts topCommandOptions) ([]*sessionNode, error) {
	criteria := apptypes.NewSessionListCriteriaBuilder(opts.limit).
		Workspace(types.Workspace(strings.TrimSpace(opts.workspace))).
		Client(types.Client(strings.TrimSpace(opts.client))).
		Agent(types.Agent(strings.TrimSpace(opts.agent))).
		ActiveOnly(true).
		Build()
	summaries, err := c.session.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
	}
	summaries, err = c.expandTopSessionLineages(ctx, summaries)
	if err != nil {
		return nil, err
	}
	return filterTopSessionTree(buildActiveSessionTree(summaries), opts), nil
}

func (c *RootCLI) expandTopSessionLineages(ctx context.Context, summaries []apptypes.SessionSummary) ([]apptypes.SessionSummary, error) {
	if len(summaries) == 0 {
		return nil, nil
	}
	merged := make([]apptypes.SessionSummary, 0, len(summaries))
	seen := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		lineage, err := c.session.Lineage(ctx, summary.SessionID())
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to load session lineage", "セッション lineage の取得に失敗しました"), err)
		}
		if len(lineage) == 0 {
			lineage = []apptypes.SessionSummary{summary}
		}
		for _, lineageSummary := range lineage {
			key := lineageSummary.SessionID().String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, lineageSummary)
		}
	}
	return merged, nil
}

func buildActiveSessionTree(summaries []apptypes.SessionSummary) []*sessionNode {
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
	return keepOngoingLineages(roots)
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

func writeTopSnapshotText(output io.Writer, roots []*sessionNode, idle time.Duration, now time.Time) error {
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
	return fmt.Sprintf("%s%s agent=%s client=%s started=%s latest=%s events=%d%s",
		prefix,
		s.SessionID(),
		agent,
		client,
		s.StartedAt().Local().Format("15:04:05"),
		latest.Local().Format("15:04:05"),
		s.TotalEvents(),
		idleMarker,
	)
}

func (c *RootCLI) runTopTUI(ctx context.Context, opts topCommandOptions) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return xerrors.Errorf("failed to create terminal screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return xerrors.Errorf("failed to initialize terminal screen: %w", err)
	}
	defer screen.Fini()

	events := make(chan tcell.Event, 8)
	go func() {
		for {
			events <- screen.PollEvent()
		}
	}()

	draw := func() {
		roots, loadErr := c.loadTopSessionTree(ctx, opts)
		drawTopScreen(screen, roots, opts, loadErr, time.Now())
	}
	draw()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			draw()
		case ev := <-events:
			switch event := ev.(type) {
			case *tcell.EventKey:
				if event.Key() == tcell.KeyCtrlC || event.Rune() == 'q' || event.Rune() == 'Q' {
					return nil
				}
			case *tcell.EventResize:
				screen.Sync()
				draw()
			}
		}
	}
}

func drawTopScreen(screen tcell.Screen, roots []*sessionNode, opts topCommandOptions, loadErr error, now time.Time) {
	screen.Clear()
	width, height := screen.Size()
	if width <= 0 || height <= 0 {
		return
	}
	headerStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Bold(true)
	parentStyle := tcell.StyleDefault.Foreground(tcell.ColorBlue)
	activeStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen)
	idleStyle := tcell.StyleDefault.Foreground(tcell.ColorGray).Dim(true)
	errorStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)

	row := 0
	drawString(screen, 0, row, width, headerStyle, "traceary top — active sessions (q/Ctrl-C to quit)")
	row++
	filterLine := fmt.Sprintf("workspace=%s client=%s agent=%s idle=%s refresh=1s",
		formatFilterValue(opts.workspace), formatFilterValue(opts.client), formatFilterValue(opts.agent), opts.idle)
	drawString(screen, 0, row, width, tcell.StyleDefault.Foreground(tcell.ColorGray), filterLine)
	row += 2
	if loadErr != nil {
		drawString(screen, 0, row, width, errorStyle, loadErr.Error())
		screen.Show()
		return
	}
	if len(roots) == 0 {
		drawString(screen, 0, row, width, tcell.StyleDefault.Foreground(tcell.ColorGray), Localize("No active sessions found.", "active session が見つかりません"))
		screen.Show()
		return
	}
	for _, root := range roots {
		row = drawTopNode(screen, root, "", true, false, row, width, height, opts.idle, now, parentStyle, activeStyle, idleStyle)
		if row >= height {
			break
		}
	}
	screen.Show()
}

func drawTopNode(screen tcell.Screen, node *sessionNode, prefix string, isLast bool, hasParent bool, row, width, height int, idle time.Duration, now time.Time, parentStyle, activeStyle, idleStyle tcell.Style) int {
	if row >= height {
		return row
	}
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if !hasParent {
		connector = ""
	}
	linePrefix := prefix + connector
	style := activeStyle
	if idle > 0 && now.Sub(node.summary.LatestEventAt()) >= idle {
		style = idleStyle
	}
	drawString(screen, 0, row, width, parentStyle, linePrefix)
	drawString(screen, runeWidth(linePrefix), row, width-runeWidth(linePrefix), style, strings.TrimPrefix(formatTopNodeLine(node, linePrefix, idle, now), linePrefix))
	row++
	childPrefix := prefix
	if hasParent {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	for i, child := range node.children {
		row = drawTopNode(screen, child, childPrefix, i == len(node.children)-1, true, row, width, height, idle, now, parentStyle, activeStyle, idleStyle)
		if row >= height {
			break
		}
	}
	return row
}

func drawString(screen tcell.Screen, x, y, maxWidth int, style tcell.Style, text string) {
	if maxWidth <= 0 {
		return
	}
	col := x
	for _, r := range text {
		if col-x >= maxWidth {
			break
		}
		screen.SetContent(col, y, r, nil, style)
		col++
	}
}

func runeWidth(s string) int {
	return utf8.RuneCountInString(s)
}

func formatFilterValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "*"
	}
	return value
}
