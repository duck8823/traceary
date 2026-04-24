package cli

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed replay_template.html
var replayTemplateHTML string

// replayTemplateSource is the indirection tests use to inject a
// malformed template without mutating the package-level
// `replayTemplateHTML`. Rewriting the package global under
// `t.Parallel()` made `TestWriteReplayHTML_PreservesExistingOnTemplateError`
// race with the other replay tests; this var lets the render-error
// test swap behavior inside a single test goroutine while the other
// tests keep using the embedded template.
var replayTemplateSource = func() string { return replayTemplateHTML }

// newReplayCommand builds `traceary replay --out <file>`. The command
// assembles a single-file HTML replay of recent sessions, events, and
// durable memories so operators can share or review session history
// without the CLI. Design constraints (from #563):
//
//   - Single output file. Everything inlined, no external assets.
//   - No network access required when viewing; the produced HTML can
//     live on an air-gapped laptop.
//   - Read-only consumption surface: nothing in this command writes to
//     the DB beyond the initialization path every subcommand performs.
//
// Out of scope (tracked as replay follow-ups):
//
//   - Full subagent-lineage tree (the minimal output flattens sessions)
//   - Interactive filters beyond the browser's Find-in-page
func (c *RootCLI) newReplayCommand() *cobra.Command {
	input := replayCommandInput{}
	cmd := &cobra.Command{
		Use:   "replay",
		Short: Localize("Export a single-file HTML replay of recent sessions, events, and memories", "最近のセッション・イベント・durable memory を single-file HTML で書き出す"),
		Long: Localize(
			"Assemble a local replay HTML file operators can open in any browser. The output is one self-contained .html with inlined CSS — no network access, no external assets. Useful for incident reviews, weekly retrospectives, and sharing Traceary session history.",
			"ブラウザで開ける single-file の replay HTML を書き出します。CSS はインライン、外部アセット依存なし。インシデントレビューや週次 retrospective、履歴共有に使えます。",
		),
		Example: strings.Join([]string{
			"  traceary replay --out /tmp/replay.html",
			"  traceary replay --sessions 10 --events-per-session 30 --memories 20 --out ./replay.html",
		}, "\n"),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runReplay(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.outputPath, "out", "", Localize("destination HTML path (required)", "書き出す HTML のパス (必須)"))
	cmd.Flags().IntVar(&input.sessions, "sessions", 10, Localize("maximum number of recent sessions to include", "含める直近セッション数"))
	cmd.Flags().IntVar(&input.eventsPerSession, "events-per-session", 20, Localize("maximum number of events to include per session", "1 セッションに含める最大イベント数"))
	cmd.Flags().IntVar(&input.memories, "memories", 20, Localize("maximum number of accepted memories to include", "含める accepted memory の最大数"))
	cmd.Flags().IntVar(&input.timelineBlocks, "timeline-blocks", 20, Localize("maximum number of timeline blocks to include; <= 0 skips the timeline panel", "含める timeline block 数。0 以下は timeline パネル自体を省く"))
	cmd.Flags().IntVar(&input.hotspots, "hotspots", 10, Localize("maximum number of failure-hotspot clusters to include; <= 0 skips the hotspot panel", "含める failure hotspot クラスタ数。0 以下は hotspot パネル自体を省く"))
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

type replayCommandInput struct {
	dbPath           string
	outputPath       string
	sessions         int
	eventsPerSession int
	memories         int
	timelineBlocks   int
	hotspots         int
}

func (c *RootCLI) runReplay(ctx context.Context, output io.Writer, input replayCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.replay == nil {
		return xerrors.Errorf(Localize("replay usecase is not configured", "replay ユースケースが設定されていません"))
	}
	if strings.TrimSpace(input.outputPath) == "" {
		return xerrors.Errorf(Localize("--out is required", "--out は必須です"))
	}
	if input.sessions <= 0 || input.eventsPerSession <= 0 {
		return xerrors.Errorf(Localize("--sessions and --events-per-session must be positive", "--sessions と --events-per-session は 1 以上である必要があります"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	bundle, err := c.replay.Bundle(ctx, apptypes.NewReplayCriteriaBuilder(input.sessions, input.eventsPerSession, input.memories).
		TimelineLimit(input.timelineBlocks).
		HotspotLimit(input.hotspots).
		Build())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to assemble replay bundle", "replay バンドルの組立てに失敗しました"), err)
	}
	data := replayDataFromBundle(bundle, input.dbPath)

	if err := writeReplayHTML(input.outputPath, data); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write replay HTML", "replay HTML の書き出しに失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, Localize(
		"Wrote replay HTML: %s (%d sessions, %d events total, %d memories, %d timeline blocks, %d failure hotspots)\n",
		"replay HTML を書き出しました: %s (sessions=%d, events=%d, memories=%d, timeline=%d, hotspots=%d)\n",
	), input.outputPath, len(data.Sessions), totalEventCount(data.Sessions), len(data.Memories), len(data.TimelineBlocks), len(data.FailureHotspots)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print replay summary", "replay 概要の出力に失敗しました"), err)
	}
	return nil
}

// replayData is the root view-model the HTML template renders.
type replayData struct {
	GeneratedAt     time.Time
	DBPath          string
	Sessions        []replaySession
	Memories        []replayMemory
	TimelineBlocks  []replayTimelineBlock
	FailureHotspots []replayFailureHotspot
}

// replayTimelineBlock is the per-block view-model rendered in the
// replay HTML timeline panel. The shape mirrors `traceary timeline`
// text output so operators can cross-reference either rendering.
type replayTimelineBlock struct {
	Start      string
	End        string
	Duration   string
	EventCount int
	Agents     string
	Workspaces []replayTimelineWorkspace
}

// replayTimelineWorkspace carries the per-workspace activity row for a
// single timeline block.
type replayTimelineWorkspace struct {
	Workspace  string
	EventCount int
	Activity   string
}

// replayFailureHotspot is the per-cluster view-model rendered in the
// replay HTML failure-hotspot panel.
type replayFailureHotspot struct {
	Command        string
	Workspace      string
	Count          int
	LastOccurredAt string
}

type replaySession struct {
	SessionID string
	Workspace string
	Agents    string
	Status    string
	Label     string
	StartedAt time.Time
	EndedAt   string
	Events    []replayEvent
}

type replayEvent struct {
	EventID   string
	Kind      string
	CreatedAt time.Time
	Client    string
	Agent     string
	Body      string
}

type replayMemory struct {
	MemoryID   string
	Type       string
	Scope      string
	Status     string
	Confidence string
	Fact       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ValidFrom  time.Time
	ValidTo    string
}

// replayDataFromBundle converts the cross-aggregate bundle the
// ReplayUsecase returned into the HTML template view-model. The
// bundle stays strictly application-layer (domain/model.Event +
// application/types.{SessionSummary,MemorySummary}); the replaySession
// / replayEvent / replayMemory structs remain CLI-only so the
// template can keep its pre-formatted strings.
func replayDataFromBundle(bundle apptypes.ReplayBundle, dbPathFlag string) replayData {
	data := replayData{GeneratedAt: bundle.GeneratedAt()}
	data.DBPath, _ = resolveDBPath(dbPathFlag)

	for _, session := range bundle.Sessions() {
		summary := session.Summary()
		events := session.Events()
		converted := make([]replayEvent, 0, len(events))
		for _, event := range events {
			converted = append(converted, replayEvent{
				EventID:   event.EventID().String(),
				Kind:      event.Kind().String(),
				CreatedAt: event.CreatedAt().UTC(),
				Client:    event.Client().String(),
				Agent:     event.Agent().String(),
				Body:      apptypes.ExtractPlainBody(event.Body()),
			})
		}
		data.Sessions = append(data.Sessions, replaySession{
			SessionID: summary.SessionID().String(),
			Workspace: summary.Workspace().String(),
			Agents:    strings.Join(summary.Agents(), ", "),
			Status:    summary.Status(),
			Label:     summary.Label(),
			StartedAt: summary.StartedAt().UTC(),
			EndedAt:   formatOptionalInstant(summary.EndedAt()),
			Events:    converted,
		})
	}

	for _, memory := range bundle.Memories() {
		data.Memories = append(data.Memories, replayMemory{
			MemoryID:   memory.MemoryID().String(),
			Type:       memory.MemoryType().String(),
			Scope:      memory.Scope().Kind().String() + "=" + memory.Scope().Key(),
			Status:     memory.Status().String(),
			Confidence: memory.Confidence().String(),
			Fact:       memory.Fact(),
			CreatedAt:  memory.CreatedAt().UTC(),
			UpdatedAt:  memory.UpdatedAt().UTC(),
			ValidFrom:  memory.ValidFrom().UTC(),
			ValidTo:    formatOptionalInstant(memory.ValidTo()),
		})
	}

	for _, block := range bundle.TimelineBlocks() {
		start := block.BlockStart().UTC()
		end := block.BlockEnd().UTC()
		workspaces := make([]replayTimelineWorkspace, 0, len(block.WorkspaceBreakdown()))
		for _, ws := range block.WorkspaceBreakdown() {
			activity := strings.TrimSpace(ws.Summary())
			if activity == "" {
				activity = formatKindCounts(computeKindCounts(ws.Kinds()))
			}
			workspaces = append(workspaces, replayTimelineWorkspace{
				Workspace:  ws.Workspace(),
				EventCount: ws.EventCount(),
				Activity:   activity,
			})
		}
		data.TimelineBlocks = append(data.TimelineBlocks, replayTimelineBlock{
			Start:      start.Format(time.RFC3339),
			End:        end.Format(time.RFC3339),
			Duration:   formatDuration(end.Sub(start)),
			EventCount: block.EventCount(),
			Agents:     strings.Join(block.Agents(), ", "),
			Workspaces: workspaces,
		})
	}

	for _, hotspot := range bundle.FailureHotspots() {
		data.FailureHotspots = append(data.FailureHotspots, replayFailureHotspot{
			Command:        hotspot.Command(),
			Workspace:      hotspot.Workspace(),
			Count:          hotspot.Count(),
			LastOccurredAt: hotspot.LastOccurredAt().UTC().Format(time.RFC3339),
		})
	}
	return data
}

func totalEventCount(sessions []replaySession) int {
	n := 0
	for _, s := range sessions {
		n += len(s.Events)
	}
	return n
}

func writeReplayHTML(outputPath string, data replayData) error {
	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve output path", "出力パスの解決に失敗しました"), err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to create output directory", "出力ディレクトリの作成に失敗しました"), err)
	}

	// Refuse symlink targets — the Stop-hook / CLI input does not have
	// enough context to validate where a symlink points, and writing
	// through one could clobber a privileged file the operator did not
	// intend to touch.
	if info, err := os.Lstat(absPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return xerrors.Errorf(Localize(
				"refusing to write replay HTML through a symlink: %s",
				"symlink 経由の書き込みを拒否しました: %s",
			), absPath)
		}
	} else if !os.IsNotExist(err) {
		return xerrors.Errorf("%s: %w", Localize("failed to inspect replay output path", "replay 出力パスの確認に失敗しました"), err)
	}

	tmpl, err := template.New("replay").Funcs(template.FuncMap{
		"fmtInstant": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Format(time.RFC3339)
		},
		"short": func(s string, n int) string {
			// Operate on runes, not bytes, so the truncated output never
			// ends in a half-encoded UTF-8 sequence (assistant reasoning,
			// memory facts, and prompts frequently carry Japanese /
			// emoji content that byte-slicing would corrupt).
			runes := []rune(s)
			if len(runes) <= n {
				return s
			}
			return string(runes[:n]) + "…"
		},
	}).Parse(replayTemplateSource())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse replay template", "replay テンプレートの parse に失敗しました"), err)
	}

	// Write to a sibling temp file and only rename into place on
	// success, so a template render failure cannot truncate or
	// partially overwrite an existing replay file.
	tmpFile, err := os.CreateTemp(filepath.Dir(absPath), ".traceary-replay-*.html.tmp")
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to create replay temp file", "replay 一時ファイルの作成に失敗しました"), err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		_ = tmpFile.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpl.Execute(tmpFile, data); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to render replay template", "replay テンプレートのレンダリングに失敗しました"), err)
	}
	if err := tmpFile.Sync(); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to sync replay temp file", "replay 一時ファイルの fsync に失敗しました"), err)
	}
	if err := tmpFile.Close(); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to close replay temp file", "replay 一時ファイルの close に失敗しました"), err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to set replay file permissions", "replay ファイルの permission 設定に失敗しました"), err)
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to place replay HTML", "replay HTML の配置に失敗しました"), err)
	}
	cleanup = false
	return nil
}

// formatOptionalInstant renders an Optional[time.Time] as RFC3339 or
// "—" when absent. Shared shape for session-end and valid-to columns.
func formatOptionalInstant(value types.Optional[time.Time]) string {
	if t, ok := value.Value(); ok {
		return t.UTC().Format(time.RFC3339)
	}
	return "—"
}
