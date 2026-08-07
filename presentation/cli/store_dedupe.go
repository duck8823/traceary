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

// The accepted `--client` values. Hook duplicates are written with
// events.client="hook", so the selector maps to an events.agent filter: "codex"
// and "kimi" scope to that agent, "all" scopes to every agent. The store-side
// filter already accepts any agent, so naming another client is a CLI-only
// change. "kimi" is named because it is the host that actually needs the repair:
// its measured repeat ratio is 23.0x against codex 1.8x and antigravity 1.3x.
const (
	storeDedupeClientCodex = "codex"
	storeDedupeClientKimi  = "kimi"
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
	dbPath   string
	apply    bool
	restore  string
	purge    string
	listRuns bool
	client   string
	strict   bool
	asJSON   bool
}

func (c *RootCLI) newStoreDedupeContentEventsCommand() *cobra.Command {
	input := storeDedupeContentEventsInput{}

	cmd := &cobra.Command{
		Use:   "content-events",
		Short: Localize("Quarantine historical hook prompt/transcript duplicates (reversible)", "履歴上の hook prompt/transcript 重複を隔離する (可逆)"),
		Long: Localize(
			"Audit and, with --apply, quarantine historical hook-originated prompt/transcript duplicate rows. "+
				"The default is a dry-run that mutates nothing. Duplicates are moved into a restore-capable quarantine "+
				"archive rather than hard-deleted; reverse a run with --restore <run-id>. Command audits are never touched.\n\n"+
				"An apply commits one duplicate cluster at a time, so interrupting it leaves every cluster either fully "+
				"quarantined or untouched and re-running continues where it stopped. If an apply is interrupted before "+
				"it prints its run id, --list-runs finds it. Quarantined bodies still occupy the store: run "+
				"--purge <run-id> to end the rollback window and reclaim them, then VACUUM to return the pages to the "+
				"filesystem.",
			"履歴上の hook 由来 prompt/transcript 重複行を監査し、--apply で隔離します。"+
				"既定は何も変更しない dry-run です。重複は hard delete せず復元可能な quarantine archive へ移動し、"+
				"--restore <run-id> で取り消せます。command audit は対象外です。\n\n"+
				"--apply は重複クラスタ単位で commit するため、中断しても各クラスタは「完全に隔離済み」か「未着手」のどちらかになり、"+
				"再実行で続きから進みます。run id が表示される前に中断した場合は --list-runs で見つけられます。"+
				"隔離された本文はまだストアを占有します。--purge <run-id> で復元可能期間を終了して回収し、"+
				"VACUUM でページをファイルシステムへ返してください。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreDedupeContentEvents(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&input.apply, "apply", false, Localize("quarantine duplicates (default is a dry-run that changes nothing)", "重複を隔離する (既定は何も変更しない dry-run)"))
	cmd.Flags().StringVar(&input.restore, "restore", "", Localize("restore the rows quarantined by the given dedupe run id", "指定した dedupe run id で隔離された行を復元する"))
	cmd.Flags().StringVar(&input.purge, "purge", "", Localize("drop the rows quarantined by the given dedupe run id, ending its rollback window", "指定した dedupe run id で隔離された行を破棄し、その復元可能期間を終了する"))
	cmd.Flags().BoolVar(&input.listRuns, "list-runs", false, Localize("list the quarantine runs still held in the archive", "archive に残っている quarantine run を一覧する"))
	cmd.Flags().StringVar(&input.client, "client", storeDedupeClientCodex, Localize("agent scope to target (codex | kimi | all)", "対象とする agent スコープ (codex | kimi | all)"))
	cmd.Flags().BoolVar(&input.strict, "strict", false, Localize("report every exact duplicate group regardless of time gap", "時間差に関係なく完全一致する重複グループをすべて対象にする"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("emit machine-readable JSON", "機械可読な JSON を出力する"))

	return cmd
}

func (c *RootCLI) runStoreDedupeContentEvents(ctx context.Context, output io.Writer, input storeDedupeContentEventsInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if err := validateStoreDedupeMode(input); err != nil {
		return err
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	if input.listRuns {
		return c.runStoreDedupeListRuns(ctx, output, input.asJSON)
	}
	if restore := strings.TrimSpace(input.restore); restore != "" {
		return c.runStoreDedupeRestore(ctx, output, restore, input.asJSON)
	}
	if purge := strings.TrimSpace(input.purge); purge != "" {
		return c.runStoreDedupePurge(ctx, output, purge, input.asJSON)
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

// validateStoreDedupeMode rejects flag combinations that would ask for more than
// one of the three mutually exclusive modes: quarantine, restore, and purge.
func validateStoreDedupeMode(input storeDedupeContentEventsInput) error {
	restore := strings.TrimSpace(input.restore) != ""
	purge := strings.TrimSpace(input.purge) != ""
	switch {
	case input.listRuns && (input.apply || restore || purge):
		return xerrors.New(Localize(
			"--list-runs cannot be combined with --apply, --restore, or --purge",
			"--list-runs は --apply, --restore, --purge と同時に指定できません",
		))
	case input.apply && restore:
		return xerrors.New(Localize("--apply and --restore cannot be combined", "--apply と --restore は同時に指定できません"))
	case input.apply && purge:
		return xerrors.New(Localize("--apply and --purge cannot be combined", "--apply と --purge は同時に指定できません"))
	case restore && purge:
		return xerrors.New(Localize("--restore and --purge cannot be combined", "--restore と --purge は同時に指定できません"))
	}
	return nil
}

func (c *RootCLI) runStoreDedupeListRuns(ctx context.Context, output io.Writer, asJSON bool) error {
	runs, err := c.storeManagement.ListContentEventDedupeRuns(ctx)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list dedupe runs", "dedupe run の一覧取得に失敗しました"), err)
	}

	if asJSON {
		payload := storeDedupeRunListJSON{Runs: make([]storeDedupeRunJSON, 0, len(runs))}
		for _, run := range runs {
			payload.Runs = append(payload.Runs, storeDedupeRunJSON{
				RunID:           run.RunID,
				ArchivedAt:      run.ArchivedAt,
				QuarantinedRows: run.QuarantinedRows,
				BodyBytes:       run.BodyBytes,
			})
		}
		return encodeStoreDedupeJSON(output, payload)
	}

	if len(runs) == 0 {
		if _, err := fmt.Fprintln(output, Localize(
			"No quarantine runs are held in the archive.",
			"archive に残っている quarantine run はありません。",
		)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dedupe run list", "dedupe run 一覧の出力に失敗しました"), err)
		}
		return nil
	}
	for _, run := range runs {
		if _, err := fmt.Fprintf(
			output,
			"  run_id=%s archived_at=%s rows=%d body_bytes=%d\n",
			run.RunID, run.ArchivedAt, run.QuarantinedRows, run.BodyBytes,
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dedupe run list", "dedupe run 一覧の出力に失敗しました"), err)
		}
	}
	return nil
}

func (c *RootCLI) runStoreDedupePurge(ctx context.Context, output io.Writer, runID string, asJSON bool) error {
	result, err := c.storeManagement.PurgeContentEventDedupeRun(ctx, runID)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to purge dedupe run", "dedupe run の破棄に失敗しました"), err)
	}
	if asJSON {
		return encodeStoreDedupeJSON(output, storeDedupePurgeJSON{
			RunID:             result.RunID,
			PurgedCount:       result.PurgedCount,
			ReleasedBodyBytes: result.ReleasedBody,
		})
	}
	if _, err := fmt.Fprintf(
		output,
		"%s\n",
		localizef(
			"Purged %d quarantined row(s) from dedupe run %s (%d body byte(s) released). Run VACUUM to return the pages to the filesystem.",
			"dedupe run %[2]s から %[1]d 行を破棄しました (本文 %[3]d バイトを解放)。ページをファイルシステムへ返すには VACUUM を実行してください。",
			result.PurgedCount, result.RunID, result.ReleasedBody,
		),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print purge result", "破棄結果の出力に失敗しました"), err)
	}
	return nil
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
	switch trimmed := strings.TrimSpace(strings.ToLower(client)); trimmed {
	case "":
		return storeDedupeClientCodex, nil
	case storeDedupeClientCodex, storeDedupeClientKimi:
		return trimmed, nil
	case storeDedupeClientAll:
		return "", nil
	default:
		return "", xerrors.New(Localize("--client must be one of codex, kimi, all", "--client は codex, kimi, all のいずれかである必要があります"))
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
	RunID        string                  `json:"run_id,omitempty"`
	Applied      bool                    `json:"applied"`
	ScannedCount int                     `json:"scanned_count"`
	GroupCount   int                     `json:"group_count"`
	MovedCount   int                     `json:"moved_count"`
	SkippedCount int                     `json:"skipped_count"`
	Groups       []storeDedupeGroupJSON  `json:"groups"`
	Skipped      []storeDedupeSkipJSON   `json:"skipped"`
	Sources      []storeDedupeSourceJSON `json:"sources"`
}

type storeDedupeSourceJSON struct {
	Agent          string  `json:"agent"`
	SourceHook     string  `json:"source_hook"`
	ScannedCount   int     `json:"scanned_count"`
	GroupCount     int     `json:"group_count"`
	CandidateCount int     `json:"candidate_count"`
	CandidateRate  float64 `json:"candidate_rate"`
}

type storeDedupeRestoreJSON struct {
	RunID         string `json:"run_id"`
	RestoredCount int    `json:"restored_count"`
}

type storeDedupeRunJSON struct {
	RunID           string `json:"run_id"`
	ArchivedAt      string `json:"archived_at"`
	QuarantinedRows int    `json:"quarantined_rows"`
	BodyBytes       int64  `json:"body_bytes"`
}

type storeDedupeRunListJSON struct {
	Runs []storeDedupeRunJSON `json:"runs"`
}

type storeDedupePurgeJSON struct {
	RunID             string `json:"run_id"`
	PurgedCount       int    `json:"purged_count"`
	ReleasedBodyBytes int64  `json:"released_body_bytes"`
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
		Sources:      make([]storeDedupeSourceJSON, 0, len(result.Sources)),
	}
	for _, source := range result.Sources {
		payload.Sources = append(payload.Sources, storeDedupeSourceJSON{
			Agent: source.Agent, SourceHook: source.SourceHook,
			ScannedCount: source.ScannedCount, GroupCount: source.GroupCount,
			CandidateCount: source.CandidateCount, CandidateRate: source.CandidateRate,
		})
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
	for _, source := range result.Sources {
		hook := source.SourceHook
		if hook == "" {
			hook = "-"
		}
		if _, err := fmt.Fprintf(output, "  heuristic agent=%s source_hook=%s scanned=%d groups=%d candidates=%d rate=%.4f\n",
			source.Agent, hook, source.ScannedCount, source.GroupCount, source.CandidateCount, source.CandidateRate); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print dedupe source statistics", "重複候補の source 統計を出力できませんでした"), err)
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
