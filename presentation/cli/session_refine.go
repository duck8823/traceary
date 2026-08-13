package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionRefineCommand() *cobra.Command {
	var (
		dbPath     string
		summary    string
		coversTo   string
		keywords   string
		producedBy string
		asJSON     bool
	)

	refineCmd := &cobra.Command{
		Use:   "refine <session-id>",
		Short: Localize("Store a session refinement summary", "セッション要約（refinement）を保存する"),
		Long: Localize(
			"Store an agent-authored session refinement (L2 summary).\n\nTraceary never composes the summary text: it stores what you hand it and owns generation / coverage bookkeeping only. Replaying the same covers-to range is a no-op.\n\ncovers-from is always derived (session earliest event on first write; kept on supersede). degraded refinements are written only by store gc via the use case, not this CLI.",
			"エージェントが書いたセッション要約（L2 refinement）を保存します。\n\nTraceary は要約テキストを合成しません。渡された内容を保存し、generation / coverage の管理だけを所有します。同じ covers-to 範囲の再実行は no-op です。\n\ncovers-from は常に導出されます（初回はセッション最古イベント、supersede 時は既存の earlier を保持）。degraded 要約は store gc が use case 経由で書くため、この CLI では指定しません。",
		),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runSessionRefine(cmd, cmd.OutOrStdout(), sessionRefineCommandInput{
				dbPath:     dbPath,
				sessionID:  strings.TrimSpace(args[0]),
				summary:    summary,
				coversTo:   strings.TrimSpace(coversTo),
				keywords:   keywords,
				producedBy: strings.TrimSpace(producedBy),
				asJSON:     asJSON,
			})
		},
	}

	refineCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	refineCmd.Flags().StringVar(&summary, "summary", "", Localize("summary text to store (required)", "保存する要約テキスト（必須）"))
	refineCmd.Flags().StringVar(&coversTo, "covers-to", "", Localize("latest event id covered by this summary (required)", "この要約が被覆する最新イベント ID（必須）"))
	refineCmd.Flags().StringVar(&keywords, "keywords", "", Localize("comma-separated keywords", "カンマ区切りのキーワード"))
	refineCmd.Flags().StringVar(&producedBy, "produced-by", "cli", Localize("who authored the summary (default: cli)", "要約の作成者（既定: cli）"))
	refineCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	_ = refineCmd.MarkFlagRequired("summary")
	_ = refineCmd.MarkFlagRequired("covers-to")

	return refineCmd
}

type sessionRefineCommandInput struct {
	dbPath     string
	sessionID  string
	summary    string
	coversTo   string
	keywords   string
	producedBy string
	asJSON     bool
}

func (c *RootCLI) runSessionRefine(cmd *cobra.Command, output io.Writer, input sessionRefineCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.sessionRefinement == nil {
		return xerrors.New(Localize("session refine usecase is not configured", "session refine ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(cmd.Context()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	sessionID, err := types.SessionIDFrom(input.sessionID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("invalid session id", "session id が不正です"), err)
	}
	coversTo, err := types.EventIDFrom(input.coversTo)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("invalid covers-to event id", "covers-to イベント ID が不正です"), err)
	}
	producedBy := input.producedBy
	if producedBy == "" {
		producedBy = "cli"
	}

	result, err := c.sessionRefinement.Refine(cmd.Context(), usecase.SessionRefineInput{
		SessionID:         sessionID,
		Summary:           input.summary,
		Keywords:          input.keywords,
		ProducedBy:        producedBy,
		CoversTo:          coversTo,
		Degraded:          false,
		HasAgentReasoning: true,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to refine session", "session の refine に失敗しました"), err)
	}

	return printSessionRefineResult(output, result, input.asJSON)
}

// sessionRefineJSON is the --json contract for `traceary session refine`.
type sessionRefineJSON struct {
	Outcome           string `json:"outcome"`
	SessionID         string `json:"session_id"`
	Generation        int    `json:"generation"`
	CoversFromEventID string `json:"covers_from_event_id"`
	CoversToEventID   string `json:"covers_to_event_id"`
	Summary           string `json:"summary"`
	Keywords          string `json:"keywords"`
	ProducedBy        string `json:"produced_by"`
	ProducedAt        string `json:"produced_at"`
	Degraded          bool   `json:"degraded"`
}

func printSessionRefineResult(output io.Writer, result model.SessionRefineResult, asJSON bool) error {
	refinement := result.Refinement()
	if asJSON {
		payload := sessionRefineJSON{
			Outcome:           string(result.Outcome()),
			SessionID:         refinement.SessionID().String(),
			Generation:        refinement.Generation(),
			CoversFromEventID: refinement.CoversFromEventID().String(),
			CoversToEventID:   refinement.CoversToEventID().String(),
			Summary:           refinement.Summary(),
			Keywords:          refinement.Keywords(),
			ProducedBy:        refinement.ProducedBy(),
			ProducedAt:        formatJSONTime(refinement.ProducedAt()),
			Degraded:          refinement.Degraded(),
		}
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(payload); err != nil {
			return xerrors.Errorf("failed to print session refine JSON: %w", err)
		}
		return nil
	}

	var verb string
	switch result.Outcome() {
	case model.SessionRefineOutcomeCreated:
		verb = Localize("Created", "作成しました")
	case model.SessionRefineOutcomeSuperseded:
		verb = Localize("Superseded", "更新しました")
	case model.SessionRefineOutcomeUnchanged:
		verb = Localize("Unchanged", "変更なし")
	default:
		verb = string(result.Outcome())
	}
	if _, err := fmt.Fprintf(
		output,
		"%s: session=%s generation=%d covers=%s..%s\n",
		verb,
		refinement.SessionID(),
		refinement.Generation(),
		refinement.CoversFromEventID(),
		refinement.CoversToEventID(),
	); err != nil {
		return xerrors.Errorf("failed to print session refine result: %w", err)
	}
	return nil
}
