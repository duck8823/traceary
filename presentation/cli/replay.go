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
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed replay_template.html
var replayTemplateHTML string

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
//   - Failure hotspot analytics
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
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

type replayCommandInput struct {
	dbPath           string
	outputPath       string
	sessions         int
	eventsPerSession int
	memories         int
}

func (c *RootCLI) runReplay(ctx context.Context, output io.Writer, input replayCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.session == nil || c.event == nil {
		return xerrors.Errorf(Localize("session/event usecases must be configured", "session/event ユースケースが必要です"))
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

	data, err := c.gatherReplayData(ctx, input)
	if err != nil {
		return err
	}

	if err := writeReplayHTML(input.outputPath, data); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write replay HTML", "replay HTML の書き出しに失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, Localize(
		"Wrote replay HTML: %s (%d sessions, %d events total, %d memories)\n",
		"replay HTML を書き出しました: %s (sessions=%d, events=%d, memories=%d)\n",
	), input.outputPath, len(data.Sessions), totalEventCount(data.Sessions), len(data.Memories)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print replay summary", "replay 概要の出力に失敗しました"), err)
	}
	return nil
}

// replayData is the root view-model the HTML template renders.
type replayData struct {
	GeneratedAt time.Time
	DBPath      string
	Sessions    []replaySession
	Memories    []replayMemory
}

type replaySession struct {
	SessionID string
	Workspace string
	Agent     string
	Client    string
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

func (c *RootCLI) gatherReplayData(ctx context.Context, input replayCommandInput) (replayData, error) {
	data := replayData{GeneratedAt: time.Now().UTC()}
	data.DBPath, _ = resolveDBPath(input.dbPath)

	sessionCriteria := apptypes.NewSessionListCriteriaBuilder(input.sessions).Build()
	sessions, err := c.session.List(ctx, sessionCriteria)
	if err != nil {
		return replayData{}, xerrors.Errorf("failed to list sessions for replay: %w", err)
	}
	for _, s := range sessions {
		events, err := c.eventsForSession(ctx, s.SessionID(), input.eventsPerSession)
		if err != nil {
			return replayData{}, err
		}
		data.Sessions = append(data.Sessions, replaySession{
			SessionID: s.SessionID().String(),
			Workspace: s.Workspace().String(),
			Agent:     strings.Join(s.Agents(), ", "),
			Client:    s.Status(),
			Label:     s.Label(),
			StartedAt: s.StartedAt().UTC(),
			EndedAt:   formatOptionalInstant(s.EndedAt()),
			Events:    events,
		})
	}

	if c.memory != nil && input.memories > 0 {
		memCriteria := apptypes.NewMemoryListCriteriaBuilder(input.memories).
			Statuses([]types.MemoryStatus{types.MemoryStatusAccepted}).
			Build()
		memories, err := c.memory.List(ctx, memCriteria)
		if err != nil {
			return replayData{}, xerrors.Errorf("failed to list memories for replay: %w", err)
		}
		for _, m := range memories {
			data.Memories = append(data.Memories, replayMemory{
				MemoryID:   m.MemoryID().String(),
				Type:       m.MemoryType().String(),
				Scope:      m.Scope().Kind().String() + "=" + m.Scope().Key(),
				Status:     m.Status().String(),
				Confidence: m.Confidence().String(),
				Fact:       m.Fact(),
				CreatedAt:  m.CreatedAt().UTC(),
				UpdatedAt:  m.UpdatedAt().UTC(),
				ValidFrom:  m.ValidFrom().UTC(),
				ValidTo:    formatOptionalInstant(m.ValidTo()),
			})
		}
	}

	return data, nil
}

func (c *RootCLI) eventsForSession(ctx context.Context, sessionID types.SessionID, limit int) ([]replayEvent, error) {
	criteria := apptypes.NewEventListCriteriaBuilder(limit).
		SessionID(sessionID).
		Build()
	events, err := c.event.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list events for session %s: %w", sessionID.String(), err)
	}
	result := make([]replayEvent, 0, len(events))
	for _, e := range events {
		result = append(result, replayEvent{
			EventID:   e.EventID().String(),
			Kind:      e.Kind().String(),
			CreatedAt: e.CreatedAt().UTC(),
			Client:    e.Client().String(),
			Agent:     e.Agent().String(),
			Body:      e.Body(),
		})
	}
	return result, nil
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
		return xerrors.Errorf("failed to resolve output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return xerrors.Errorf("failed to create output directory: %w", err)
	}

	tmpl, err := template.New("replay").Funcs(template.FuncMap{
		"fmtInstant": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Format(time.RFC3339)
		},
		"short": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
	}).Parse(replayTemplateHTML)
	if err != nil {
		return xerrors.Errorf("failed to parse replay template: %w", err)
	}

	file, err := os.Create(absPath) // #nosec G304 -- path supplied by --out flag
	if err != nil {
		return xerrors.Errorf("failed to create replay output file: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := tmpl.Execute(file, data); err != nil {
		return xerrors.Errorf("failed to render replay template: %w", err)
	}
	if err := os.Chmod(absPath, 0o644); err != nil {
		return xerrors.Errorf("failed to set replay file permissions: %w", err)
	}
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

// _ exists so future additions can reference the interface without
// a reflow. Keep for readability — the linter's unused-var check
// does not apply to an `_` identifier.
var _ = (*model.Event)(nil)
