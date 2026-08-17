package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/sensitivepath"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newListCommand() *cobra.Command {
	var (
		dbPath        string
		limit         int
		offset        int
		kind          string
		client        string
		agent         string
		sessionID     string
		repo          string
		from          string
		since         string
		to            string
		until         string
		timezone      string
		failuresOnly  bool
		sensitiveOnly bool
		sourceHook    string
		asJSON        bool
		wide          bool
		utc           bool
		fields        []string
		preset        string
		color         string
		follow        bool
		followSession string
		blocks        bool
		gap           int
	)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: Localize("List recent events", "直近のログを一覧表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runList(cmd.Context(), cmd.ErrOrStderr(), cmd.OutOrStdout(), listCommandInput{
				dbPath:           dbPath,
				limit:            limit,
				offset:           offset,
				kind:             kind,
				client:           client,
				agent:            agent,
				sessionID:        sessionID,
				repo:             repo,
				from:             from,
				since:            since,
				to:               to,
				until:            until,
				timezone:         timezone,
				failuresOnly:     failuresOnly,
				sensitiveOnly:    sensitiveOnly,
				sourceHook:       sourceHook,
				sourceHookSet:    cmd.Flags().Changed("source-hook"),
				asJSON:           asJSON,
				wide:             wide,
				utc:              utc,
				fields:           fields,
				fieldsSet:        cmd.Flags().Changed("fields"),
				preset:           preset,
				presetSet:        cmd.Flags().Changed("preset"),
				kindSet:          cmd.Flags().Changed("kind"),
				clientSet:        cmd.Flags().Changed("client"),
				agentSet:         cmd.Flags().Changed("agent"),
				sessionIDSet:     cmd.Flags().Changed("session-id"),
				repoSet:          cmd.Flags().Changed("workspace"),
				failuresOnlySet:  cmd.Flags().Changed("failures"),
				color:            color,
				colorSet:         cmd.Flags().Changed("color"),
				follow:           follow,
				followSession:    followSession,
				followSessionSet: cmd.Flags().Changed("follow-session"),
				timezoneSet:      cmd.Flags().Changed("timezone"),
				blocks:           blocks,
				gap:              gap,
				gapSet:           cmd.Flags().Changed("gap"),
			})
		},
	}
	listCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	listCmd.Flags().IntVar(&limit, "limit", 20, Localize("number of events to display; with --follow, 0 prints only new events; with --blocks, maximum blocks", "表示件数。--follow 時は 0 で新規イベントのみ。--blocks 時はブロック数上限"))
	listCmd.Flags().IntVar(&offset, "offset", 0, Localize("number of events to skip before listing", "一覧表示前にスキップする件数"))
	listCmd.Flags().BoolVar(&sensitiveOnly, "sensitive", false, Localize("list only command audits that match sensitive-path patterns (compute-on-read; not redaction)", "sensitive-path パターンに一致した command audit のみ一覧する（compute-on-read。redaction とは別）"))
	listCmd.Flags().StringVar(&kind, "kind", "", Localize("filter by event kind (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt, transcript; alias: audit)", "イベント種別で絞り込む (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt, transcript; alias: audit)"))
	listCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "記録経路で絞り込む"))
	listCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "作業主体で絞り込む"))
	listCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("filter by session ID", "session ID で絞り込む"))
	listCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by auxiliary workspace identifier", "補助的な workspace 識別子で絞り込む"))
	listCmd.Flags().StringVar(&from, "from", "", Localize("start date (`YYYY-MM-DD` or RFC3339; alias: `--since`)", "開始日 (`YYYY-MM-DD` または RFC3339; 別名: `--since`)"))
	listCmd.Flags().StringVar(&since, "since", "", Localize("start date (`YYYY-MM-DD` or RFC3339) (alias for `--from`)", "開始日 (`YYYY-MM-DD` または RFC3339) (`--from` の別名)"))
	listCmd.Flags().StringVar(&to, "to", "", Localize("end date (`YYYY-MM-DD` or RFC3339; alias: `--until`)", "終了日 (`YYYY-MM-DD` または RFC3339; 別名: `--until`)"))
	listCmd.Flags().StringVar(&until, "until", "", Localize("end date (`YYYY-MM-DD` or RFC3339) (alias for `--to`)", "終了日 (`YYYY-MM-DD` または RFC3339) (`--to` の別名)"))
	listCmd.Flags().StringVar(&timezone, "timezone", "UTC", Localize("IANA timezone for date-only bounds (default: UTC)", "日付のみの境界に使う IANA タイムゾーン（既定: UTC）"))
	listCmd.Flags().BoolVar(&failuresOnly, "failures", false, Localize("show only failed commands", "失敗したコマンドのみ表示"))
	listCmd.Flags().StringVar(&sourceHook, "source-hook", "", Localize("filter by hook identifier that produced the event (e.g. stop, subagent_stop, pre_compact, post_compact, session_start, session_end, user_prompt_submit, post_tool_use, after_agent, after_tool)", "イベントを生成した hook 識別子で絞り込む (例: stop, subagent_stop, pre_compact, post_compact, session_start, session_end, user_prompt_submit, post_tool_use, after_agent, after_tool)"))
	listCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	listCmd.Flags().BoolVar(&wide, "wide", false, Localize("use the legacy tab-separated format", "従来のタブ区切り形式で出力する"))
	listCmd.Flags().BoolVar(&utc, "utc", false, Localize("print text timestamps in UTC instead of local time", "テキスト出力のタイムスタンプを現地時刻ではなく UTC で出力する"))
	listCmd.Flags().StringSliceVar(&fields, "fields", nil, readFieldsFlagUsage())
	listCmd.Flags().StringVar(&preset, "preset", "", readPresetsFlagUsage())
	listCmd.Flags().StringVar(&color, "color", "", readColorFlagUsage())
	listCmd.Flags().BoolVar(&follow, "follow", false, Localize("keep printing new matching events as they arrive", "新しい一致イベントを到着順に追跡表示する"))
	listCmd.Flags().StringVar(&followSession, "follow-session", "", Localize(
		"with --follow, tail events only from the given session id (prefix match, minimum 8 runes)",
		"--follow 時に指定した session id のイベントだけを追跡する (先頭一致、最低 8 文字)",
	))
	listCmd.Flags().BoolVar(&blocks, "blocks", false, Localize("print gap-detected work blocks instead of event rows", "イベント行の代わりにギャップ検出した作業ブロックを表示する"))
	listCmd.Flags().IntVar(&gap, "gap", defaultGapMinutes, Localize("with --blocks, idle gap threshold in minutes", "--blocks 時のアイドル判定閾値（分）"))

	return listCmd
}

func (c *RootCLI) runListFollow(ctx context.Context, warnWriter io.Writer, output io.Writer, input listCommandInput) error {
	if input.limit < 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 0", "limit は 0 以上である必要があります"))
	}
	if input.offset != 0 {
		return xerrors.New(Localize("--follow cannot be combined with --offset", "--follow は --offset と同時に使えません"))
	}
	if strings.TrimSpace(input.from) != "" || strings.TrimSpace(input.since) != "" ||
		strings.TrimSpace(input.to) != "" || strings.TrimSpace(input.until) != "" {
		return xerrors.New(Localize("--follow cannot be combined with --from/--since/--to/--until", "--follow は --from/--since/--to/--until と同時に使えません"))
	}
	if input.sensitiveOnly {
		return xerrors.New(Localize("--follow cannot be combined with --sensitive", "--follow は --sensitive と同時に使えません"))
	}
	if strings.TrimSpace(input.sourceHook) != "" {
		return xerrors.New(Localize("--follow cannot be combined with --source-hook", "--follow は --source-hook と同時に使えません"))
	}
	return c.runTail(ctx, warnWriter, output, tailCommandInput{
		dbPath:           input.dbPath,
		limit:            input.limit,
		kind:             input.kind,
		client:           input.client,
		agent:            input.agent,
		sessionID:        input.sessionID,
		repo:             input.repo,
		failuresOnly:     input.failuresOnly,
		asJSON:           input.asJSON,
		wide:             input.wide,
		utc:              input.utc,
		location:         input.location,
		fields:           input.fields,
		fieldsSet:        input.fieldsSet,
		preset:           input.preset,
		presetSet:        input.presetSet,
		kindSet:          input.kindSet,
		clientSet:        input.clientSet,
		agentSet:         input.agentSet,
		sessionIDSet:     input.sessionIDSet,
		repoSet:          input.repoSet,
		failuresOnlySet:  input.failuresOnlySet,
		color:            input.color,
		colorSet:         input.colorSet,
		followSession:    input.followSession,
		followSessionSet: input.followSessionSet,
	})
}

func (c *RootCLI) runListBlocks(ctx context.Context, output io.Writer, input listCommandInput) error {
	if input.limit < 1 {
		return xerrors.New(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset != 0 {
		return xerrors.New(Localize("--blocks cannot be combined with --offset", "--blocks は --offset と同時に使えません"))
	}
	if input.followSessionSet || strings.TrimSpace(input.followSession) != "" {
		return xerrors.New(Localize("--blocks cannot be combined with --follow-session", "--blocks は --follow-session と同時に使えません"))
	}
	if input.kindSet || strings.TrimSpace(input.kind) != "" {
		return xerrors.New(Localize("--blocks cannot be combined with --kind", "--blocks は --kind と同時に使えません"))
	}
	if input.clientSet || strings.TrimSpace(input.client) != "" {
		return xerrors.New(Localize("--blocks cannot be combined with --client", "--blocks は --client と同時に使えません"))
	}
	if input.agentSet || strings.TrimSpace(input.agent) != "" {
		return xerrors.New(Localize("--blocks cannot be combined with --agent", "--blocks は --agent と同時に使えません"))
	}
	if input.sessionIDSet || strings.TrimSpace(input.sessionID) != "" {
		return xerrors.New(Localize("--blocks cannot be combined with --session-id", "--blocks は --session-id と同時に使えません"))
	}
	if input.failuresOnly {
		return xerrors.New(Localize("--blocks cannot be combined with --failures", "--blocks は --failures と同時に使えません"))
	}
	if input.sensitiveOnly {
		return xerrors.New(Localize("--blocks cannot be combined with --sensitive", "--blocks は --sensitive と同時に使えません"))
	}
	if input.sourceHookSet || strings.TrimSpace(input.sourceHook) != "" {
		return xerrors.New(Localize("--blocks cannot be combined with --source-hook", "--blocks は --source-hook と同時に使えません"))
	}
	if input.wide {
		return xerrors.New(Localize("--blocks cannot be combined with --wide", "--blocks は --wide と同時に使えません"))
	}
	if input.fieldsSet {
		return xerrors.New(Localize("--blocks cannot be combined with --fields", "--blocks は --fields と同時に使えません"))
	}
	if input.presetSet {
		return xerrors.New(Localize("--blocks cannot be combined with --preset", "--blocks は --preset と同時に使えません"))
	}
	if input.colorSet {
		return xerrors.New(Localize("--blocks cannot be combined with --color", "--blocks は --color と同時に使えません"))
	}
	if input.timezoneSet {
		return xerrors.New(Localize("--blocks cannot be combined with --timezone", "--blocks は --timezone と同時に使えません"))
	}
	return c.runTimeline(ctx, output, timelineCommandInput{
		dbPath:    input.dbPath,
		workspace: input.repo,
		from:      input.from,
		since:     input.since,
		to:        input.to,
		until:     input.until,
		gap:       input.gap,
		limit:     input.limit,
		asJSON:    input.asJSON,
		utc:       input.utc,
		location:  input.location,
	})
}

func (c *RootCLI) runList(ctx context.Context, warnWriter io.Writer, output io.Writer, input listCommandInput) error {
	if input.follow && input.blocks {
		return xerrors.New(Localize("--follow cannot be combined with --blocks", "--follow は --blocks と同時に使えません"))
	}
	if input.blocks {
		return c.runListBlocks(ctx, output, input)
	}
	if input.gapSet {
		return xerrors.New(Localize("--gap requires --blocks", "--gap には --blocks が必要です"))
	}
	if input.follow {
		return c.runListFollow(ctx, warnWriter, output, input)
	}
	if input.followSessionSet || strings.TrimSpace(input.followSession) != "" {
		return xerrors.New(Localize("--follow-session requires --follow", "--follow-session には --follow が必要です"))
	}
	if input.limit <= 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}
	if input.offset < 0 {
		return xerrors.New(Localize("offset must be greater than or equal to 0", "offset は 0 以上である必要があります"))
	}
	preset, _, err := resolveReadPreset(input.preset, c.readPresets, warnWriter)
	if err != nil {
		return err
	}
	applyReadPresetToListInput(&input, preset)
	if input.sensitiveOnly && strings.TrimSpace(input.kind) == "" {
		input.kind = "command_executed"
		input.kindSet = true
	}
	resolvedKind, err := resolveListKind(input.kind)
	if err != nil {
		return err
	}

	fromValue, err := resolveSearchDateValue(input.from, input.since, "from", "since")
	if err != nil {
		return err
	}
	toValue, err := resolveSearchDateValue(input.to, input.until, "to", "until")
	if err != nil {
		return err
	}
	interval, err := apptypes.RequestedIntervalFrom(fromValue, toValue, input.timezone, time.Now().UTC())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve time interval", "期間の解決に失敗しました"), err)
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)

	fetchLimit := input.limit
	if input.sensitiveOnly {
		// Over-fetch so compute-on-read filtering still returns up to --limit
		// matches without requiring a SQL-side index for this claim.
		fetchLimit = input.limit * 20
		if fetchLimit < 100 {
			fetchLimit = 100
		}
		if fetchLimit > 2000 {
			fetchLimit = 2000
		}
	}
	criteria := apptypes.NewEventListCriteriaBuilder(fetchLimit).
		Offset(input.offset).
		Kind(types.EventKind(resolvedKind)).
		Client(types.Client(strings.TrimSpace(input.client))).
		Agent(types.Agent(strings.TrimSpace(input.agent))).
		SessionID(types.SessionID(strings.TrimSpace(input.sessionID))).
		Workspace(types.Workspace(resolveWorkspaceValue(ctx, input.repo))).
		FailuresOnly(input.failuresOnly).
		SourceHook(strings.TrimSpace(input.sourceHook)).
		From(interval.EffectiveFromInclusive()).
		To(interval.EffectiveToExclusive()).
		Build()
	resolvedFields, err := c.resolveReadFieldsForCommand(input.fields, input.fieldsSet, input.wide, input.asJSON, preset.fields)
	if err != nil {
		return err
	}
	metadataOnly := !input.sensitiveOnly && !input.wide && !readFieldsContain(resolvedFields, readFieldMessage) && (!input.asJSON || input.fieldsSet)
	if metadataOnly {
		if c.eventMetadata == nil {
			return xerrors.New(Localize("event metadata query service is not configured", "イベントメタデータクエリサービスが設定されていません"))
		}
		if isBoundedTimestampKindList(input, resolvedKind, fromValue, toValue, resolvedFields) {
			entries, err := c.eventMetadata.ListTimestampKinds(ctx, criteria)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to list event timestamp/kind", "イベント時刻・種別一覧の取得に失敗しました"), err)
			}
			for _, entry := range entries {
				if _, err := io.WriteString(output, formatTextTimestamp(entry.CreatedAt(), eventTextFormatOptions{utc: input.utc, location: input.location}, eventCompactTimeLayout)+"  "+string(entry.Kind())+"\n"); err != nil {
					return xerrors.Errorf("write event timestamp/kind: %w", err)
				}
			}
			return nil
		}
		metadata, err := c.eventMetadata.List(ctx, criteria)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to list event metadata", "イベントメタデータ一覧の取得に失敗しました"), err)
		}
		colorMode, err := resolveColorMode(
			input.color,
			input.colorSet,
			c.defaultReadColor,
			input.asJSON,
			func() bool { return isTerminalWriter(output) },
		)
		if err != nil {
			return err
		}
		if err := writeEventMetadataByFormat(output, metadata, input.asJSON, eventTextFormatOptions{
			utc:          input.utc,
			location:     input.location,
			fields:       resolvedFields,
			colorEnabled: colorMode == colorModeOn,
			targetWidth:  terminalWidthOf(output),
		}); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print event list", "一覧出力に失敗しました"), err)
		}
		return nil
	}
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.New(Localize("list events query service is not configured", "イベント一覧クエリサービスが設定されていません"))
	}
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}
	events, err := c.event.List(ctx, criteria)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list events", "イベント一覧の取得に失敗しました"), err)
	}
	if input.sensitiveOnly {
		// Listing joins only fixed-size audit metadata; sensitive classification
		// needs decoded command/input/output (codec-aware via hydrateAuditPayload).
		// Full payload includes command, so skip the command-only path below.
		if err := c.event.HydrateCommandAudits(ctx, events, queryservice.FullCommandAuditPayload()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to hydrate command audit payloads", "command audit ペイロードの復元に失敗しました"), err)
		}
		events = filterSensitiveCommandEvents(events, input.limit)
	} else if err := c.hydrateCommandLinesForDisplay(ctx, events); err != nil {
		// Body-rendering path: restore command_text only (not I/O).
		return err
	}
	colorMode, err := resolveColorMode(
		input.color,
		input.colorSet,
		c.defaultReadColor,
		input.wide || input.asJSON,
		func() bool { return isTerminalWriter(output) },
	)
	if err != nil {
		return err
	}
	colorEnabled := colorMode == colorModeOn
	textOpts := eventTextFormatOptions{
		wide:         input.wide,
		utc:          input.utc,
		location:     input.location,
		fields:       resolvedFields,
		colorEnabled: colorEnabled,
		targetWidth:  terminalWidthOf(output),
	}
	extrasFor := c.makeCompactExtrasResolver(ctx, resolvedFields, colorEnabled)
	if err := writeEventsByFormat(output, events, input.asJSON, input.fieldsSet, textOpts, extrasFor); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print event list", "一覧出力に失敗しました"), err)
	}

	return nil
}

// isBoundedTimestampKindList guards the minimal projection. Every other list
// shape retains the general metadata path and its established filtering
// semantics.
func isBoundedTimestampKindList(input listCommandInput, resolvedKind, fromValue, toValue string, fields []readFieldID) bool {
	return !input.asJSON && input.limit == 1 && input.offset == 0 &&
		resolvedKind == "" && strings.TrimSpace(input.client) == "" &&
		strings.TrimSpace(input.agent) == "" && strings.TrimSpace(input.sessionID) == "" &&
		!input.failuresOnly && strings.TrimSpace(input.sourceHook) == "" &&
		fromValue == "" && toValue == "" && isTimestampKindFields(fields)
}

func isTimestampKindFields(fields []readFieldID) bool {
	return len(fields) == 2 && fields[0] == readFieldTS && fields[1] == readFieldKind
}

// resolveListKind delegates to validateSearchKind so that both list and
// search accept the same kind values and aliases (e.g. "audit").
func resolveListKind(value string) (string, error) {
	return validateSearchKind(value)
}

func filterSensitiveCommandEvents(events []*model.Event, limit int) []*model.Event {
	if limit <= 0 {
		limit = 20
	}
	out := make([]*model.Event, 0, limit)
	for _, event := range events {
		if event == nil || event.Kind() != types.EventKindCommandExecuted {
			continue
		}
		if !classifyEventSensitivePath(event).Matched {
			continue
		}
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// classifyEventSensitivePath classifies from the joined command audit when
// present. Legacy pre-#1675 rows may still carry a composed body and no audit
// attachment in some fixtures; those fall back to the empty classification
// inputs rather than re-splitting a body.
func classifyEventSensitivePath(event *model.Event) sensitivepath.Classification {
	if event == nil {
		return sensitivepath.Classification{}
	}
	if audit, ok := event.CommandAudit().Value(); ok && audit != nil {
		return sensitivepath.Classify(sensitivepath.Input{
			Command:         audit.Command(),
			Input:           audit.Input(),
			Output:          audit.Output(),
			InputTruncated:  audit.InputTruncated(),
			OutputTruncated: audit.OutputTruncated(),
			InputRedacted:   audit.InputRedacted(),
			OutputRedacted:  audit.OutputRedacted(),
		})
	}
	return sensitivepath.Classify(sensitivepath.Input{})
}
