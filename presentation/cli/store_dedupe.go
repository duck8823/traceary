package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// storeDedupeClientCodex / storeDedupeClientAll are the accepted `--client`
// values. Hook duplicates are written with events.client="hook", so the selector
// maps to an events.agent filter: "codex" scopes to agent="codex", "all" scopes
// to every agent. The two-value set keeps today's surface small while the
// store-side filter already accepts any agent, so adding more clients later is a
// CLI-only change.
const (
	storeDedupeClientCodex = "codex"
	storeDedupeClientAll   = "all"
)

func (c *RootCLI) newStoreDedupeCommand() *cobra.Command {
	dedupeCmd := &cobra.Command{
		Use:   "dedupe",
		Short: Localize("Reversible store maintenance for duplicate rows", "重複行に対する可逆的なストアメンテナンス"),
	}
	dedupeCmd.AddCommand(c.newStoreDedupeContentEventsCommand())
	return dedupeCmd
}

type storeDedupeContentEventsInput struct {
	dbPath  string
	apply   bool
	restore string
	client  string
	strict  bool
	asJSON  bool
}

func (c *RootCLI) newStoreDedupeContentEventsCommand() *cobra.Command {
	input := storeDedupeContentEventsInput{}

	cmd := &cobra.Command{
		Use:   "content-events",
		Short: Localize("Quarantine historical hook prompt/transcript duplicates (reversible)", "履歴上の hook prompt/transcript 重複を隔離する (可逆)"),
		Long: Localize(
			"Audit and, with --apply, quarantine historical hook-originated prompt/transcript duplicate rows. "+
				"The default is a dry-run that mutates nothing. Duplicates are moved into a restore-capable quarantine "+
				"archive rather than hard-deleted; reverse a run with --restore <run-id>. Command audits are never touched.",
			"履歴上の hook 由来 prompt/transcript 重複行を監査し、--apply で隔離します。"+
				"既定は何も変更しない dry-run です。重複は hard delete せず復元可能な quarantine archive へ移動し、"+
				"--restore <run-id> で取り消せます。command audit は対象外です。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreDedupeContentEvents(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&input.apply, "apply", false, Localize("quarantine duplicates (default is a dry-run that changes nothing)", "重複を隔離する (既定は何も変更しない dry-run)"))
	cmd.Flags().StringVar(&input.restore, "restore", "", Localize("restore the rows quarantined by the given dedupe run id", "指定した dedupe run id で隔離された行を復元する"))
	cmd.Flags().StringVar(&input.client, "client", storeDedupeClientCodex, Localize("agent scope to target (codex | all)", "対象とする agent スコープ (codex | all)"))
	cmd.Flags().BoolVar(&input.strict, "strict", false, Localize("report every exact duplicate group regardless of time gap", "時間差に関係なく完全一致する重複グループをすべて対象にする"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("emit machine-readable JSON", "機械可読な JSON を出力する"))

	return cmd
}

func (c *RootCLI) runStoreDedupeContentEvents(ctx context.Context, output io.Writer, input storeDedupeContentEventsInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if input.apply && strings.TrimSpace(input.restore) != "" {
		return xerrors.New(Localize("--apply and --restore cannot be combined", "--apply と --restore は同時に指定できません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	if strings.TrimSpace(input.restore) != "" {
		return c.runStoreDedupeRestore(ctx, output, strings.TrimSpace(input.restore), input.asJSON)
	}

	agent, err := storeDedupeAgentFilter(input.client)
	if err != nil {
		return err
	}

	result, err := c.storeManagement.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
		Agent:  agent,
		Apply:  input.apply,
		Strict: input.strict,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to dedupe content events", "content event の重複排除に失敗しました"), err)
	}

	if input.asJSON {
		return writeStoreDedupeJSON(output, result)
	}
	return writeStoreDedupeText(output, result)
}

func (c *RootCLI) runStoreDedupeRestore(ctx context.Context, output io.Writer, runID string, asJSON bool) error {
	result, err := c.storeManagement.RestoreContentEventDedupeRun(ctx, runID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to restore dedupe run", "dedupe run の復元に失敗しました"), err)
	}
	if asJSON {
		payload := storeDedupeRestoreJSON{
			RunID:         result.RunID,
			RestoredCount: result.RestoredCount,
		}
		return encodeStoreDedupeJSON(output, payload)
	}
	if _, err := fmt.Fprintf(
		output,
		"%s\n",
		localizef(
			"Restored %d row(s) from dedupe run %s",
			"%d 行を dedupe run %s から復元しました",
			result.RestoredCount, result.RunID,
		),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print restore result", "復元結果の出力に失敗しました"), err)
	}
	return nil
}

// storeDedupeAgentFilter maps the operator-facing --client value to the store-
// side events.agent filter. "all" clears the filter; "codex" scopes to Codex.
func storeDedupeAgentFilter(client string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(client)) {
	case "", storeDedupeClientCodex:
		return "codex", nil
	case storeDedupeClientAll:
		return "", nil
	default:
		return "", xerrors.New(Localize("--client must be one of codex, all", "--client は codex, all のいずれかである必要があります"))
	}
}

type storeDedupeGroupJSON struct {
	KeptEventID       string   `json:"kept_event_id"`
	DuplicateEventIDs []string `json:"duplicate_event_ids"`
	DuplicateCount    int      `json:"duplicate_count"`
	Kind              string   `json:"kind"`
	Agent             string   `json:"agent"`
	SourceHook        string   `json:"source_hook,omitempty"`
	GroupKey          string   `json:"group_key"`
}

type storeDedupeSkipJSON struct {
	GroupKey string   `json:"group_key"`
	EventIDs []string `json:"event_ids"`
	Reason   string   `json:"reason"`
}

type storeDedupeResultJSON struct {
	RunID         string                 `json:"run_id,omitempty"`
	Applied       bool                   `json:"applied"`
	ScannedCount  int                    `json:"scanned_count"`
	GroupCount    int                    `json:"group_count"`
	MovedCount    int                    `json:"moved_count"`
	SkippedCount  int                    `json:"skipped_count"`
	Groups        []storeDedupeGroupJSON `json:"groups"`
	Skipped       []storeDedupeSkipJSON  `json:"skipped"`
}

type storeDedupeRestoreJSON struct {
	RunID         string `json:"run_id"`
	RestoredCount int    `json:"restored_count"`
}

func writeStoreDedupeJSON(output io.Writer, result apptypes.ContentEventDedupeResult) error {
	payload := storeDedupeResultJSON{
		RunID:        result.RunID,
		Applied:      result.Applied,
		ScannedCount: result.ScannedCount,
		GroupCount:   len(result.Groups),
		MovedCount:   result.MovedCount(),
		SkippedCount: len(result.Skipped),
		Groups:       make([]storeDedupeGroupJSON, 0, len(result.Groups)),
		Skipped:      make([]storeDedupeSkipJSON, 0, len(result.Skipped)),
	}
	for _, group := range result.Groups {
		payload.Groups = append(payload.Groups, storeDedupeGroupJSON{
			KeptEventID:       group.KeptEventID,
			DuplicateEventIDs: group.DuplicateEventIDs,
			DuplicateCount:    group.DuplicateCount(),
			Kind:              group.Kind,
			Agent:             group.Agent,
			SourceHook:        group.SourceHook,
			GroupKey:          group.GroupKey,
		})
	}
	for _, skip := range result.Skipped {
		payload.Skipped = append(payload.Skipped, storeDedupeSkipJSON{
			GroupKey: skip.GroupKey,
			EventIDs: skip.EventIDs,
			Reason:   skip.Reason,
		})
	}
	return encodeStoreDedupeJSON(output, payload)
}

func encodeStoreDedupeJSON(output io.Writer, payload any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to encode JSON", "JSON のエンコードに失敗しました"), err)
	}
	return nil
}

func writeStoreDedupeText(output io.Writer, result apptypes.ContentEventDedupeResult) error {
	verb := Localize("would quarantine", "隔離対象")
	if result.Applied {
		verb = Localize("quarantined", "隔離しました")
	}
	header := localizef(
		"Scanned %d hook prompt/transcript event(s); %s %d row(s) across %d duplicate group(s); %d group(s) skipped",
		"%d 件の hook prompt/transcript event を検査しました。%s: %d 行 / %d 重複グループ。%d グループをスキップしました",
		result.ScannedCount, verb, result.MovedCount(), len(result.Groups), len(result.Skipped),
	)
	if _, err := fmt.Fprintln(output, header); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print dedupe summary", "重複排除サマリの出力に失敗しました"), err)
	}

	for _, group := range result.Groups {
		sourceHook := group.SourceHook
		if sourceHook == "" {
			sourceHook = "-"
		}
		if _, err := fmt.Fprintf(
			output,
			"  kind=%s agent=%s source_hook=%s kept=%s duplicates=%s\n",
			group.Kind, group.Agent, sourceHook, group.KeptEventID, strings.Join(group.DuplicateEventIDs, ","),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dedupe group", "重複グループの出力に失敗しました"), err)
		}
	}
	for _, skip := range result.Skipped {
		if _, err := fmt.Fprintf(
			output,
			"  %s event_ids=%s reason=%s\n",
			Localize("SKIPPED", "スキップ"), strings.Join(skip.EventIDs, ","), skip.Reason,
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print skipped group", "スキップグループの出力に失敗しました"), err)
		}
	}

	if !result.Applied {
		if _, err := fmt.Fprintln(output, Localize(
			"No changes were made. Re-run with --apply to quarantine the duplicates.",
			"変更は行われていません。重複を隔離するには --apply を付けて再実行してください。",
		)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dry-run note", "dry-run 注記の出力に失敗しました"), err)
		}
		return nil
	}

	if result.MovedCount() > 0 {
		if _, err := fmt.Fprintln(output, localizef(
			"Restore this run with: traceary store dedupe content-events --restore %s",
			"この run を復元するには: traceary store dedupe content-events --restore %s",
			result.RunID,
		)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print restore hint", "復元ヒントの出力に失敗しました"), err)
		}
	}
	return nil
}
