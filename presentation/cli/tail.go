package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	defaultTailInitialLimit = 20
	defaultTailBatchSize    = 100
	defaultTailPollInterval = time.Second
)

type tailTicker interface {
	C() <-chan time.Time
	Stop()
}

type realTailTicker struct {
	ticker *time.Ticker
}

func newRealTailTicker(interval time.Duration) tailTicker {
	return realTailTicker{ticker: time.NewTicker(interval)}
}

func (t realTailTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realTailTicker) Stop() {
	t.ticker.Stop()
}

func (i tailCommandInput) resolvedNowFunc() func() time.Time {
	if i.nowFunc != nil {
		return i.nowFunc
	}
	return time.Now
}

func (i tailCommandInput) resolvedTickerFactory() func(time.Duration) tailTicker {
	if i.tickerFactory != nil {
		return i.tickerFactory
	}
	return newRealTailTicker
}

type tailCursor struct {
	timestamp time.Time
	seenIDs   map[string]struct{}
}

func newTailCursor(start time.Time) tailCursor {
	return tailCursor{
		timestamp: start,
		seenIDs:   make(map[string]struct{}),
	}
}

func (c *tailCursor) isNew(event *model.Event) bool {
	if event == nil {
		return false
	}
	if event.CreatedAt().After(c.timestamp) {
		return true
	}
	if !event.CreatedAt().Equal(c.timestamp) {
		return false
	}
	_, seen := c.seenIDs[event.EventID().String()]
	return !seen
}

func (c *tailCursor) Advance(events []*model.Event) {
	if len(events) == 0 {
		return
	}

	maxTimestamp := c.timestamp
	for _, event := range events {
		if event != nil && event.CreatedAt().After(maxTimestamp) {
			maxTimestamp = event.CreatedAt()
		}
	}

	if maxTimestamp.After(c.timestamp) {
		c.timestamp = maxTimestamp
		c.seenIDs = make(map[string]struct{})
	}

	for _, event := range events {
		if event != nil && event.CreatedAt().Equal(c.timestamp) {
			c.seenIDs[event.EventID().String()] = struct{}{}
		}
	}
}

type tailEventWriter struct {
	output      io.Writer
	asJSON      bool
	textOpts    eventTextFormatOptions
	extrasFor   compactExtrasResolver
	headerWrote bool
}

func newTailEventWriter(output io.Writer, asJSON bool, textOpts eventTextFormatOptions, extrasFor compactExtrasResolver) *tailEventWriter {
	return &tailEventWriter{
		output:    output,
		asJSON:    asJSON,
		textOpts:  textOpts,
		extrasFor: extrasFor,
	}
}

func (w *tailEventWriter) EnsureReady() error {
	if w.asJSON || w.headerWrote {
		return nil
	}
	// compact mode is header-less; wide mode keeps the classic banner.
	if !w.textOpts.wide {
		w.headerWrote = true
		return nil
	}
	if _, err := fmt.Fprintln(w.output, formatEventWideHeader()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print list header", "一覧ヘッダーの出力に失敗しました"), err)
	}
	w.headerWrote = true
	return nil
}

func (w *tailEventWriter) Write(events []*model.Event) error {
	if err := w.EnsureReady(); err != nil {
		return err
	}
	for _, event := range events {
		if w.asJSON {
			if err := writeEventNDJSON(w.output, event); err != nil {
				return err
			}
			continue
		}
		var row string
		if w.textOpts.wide {
			row = formatEventWideRow(event, w.textOpts)
		} else {
			extras := compactRowExtras{}
			if w.extrasFor != nil {
				extras = w.extrasFor(event)
			}
			row = formatEventCompactRow(event, w.textOpts, extras)
		}
		if _, err := fmt.Fprintln(w.output, row); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print event row", "イベント一覧行の出力に失敗しました"), err)
		}
	}

	return nil
}

func writeEventNDJSON(output io.Writer, event *model.Event) error {
	encoded, err := json.Marshal(newEventOutput(event))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to encode JSON", "JSON 変換に失敗しました"), err)
	}
	if _, err := output.Write(append(encoded, '\n')); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write JSON", "JSON 出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) newTailCommand() *cobra.Command {
	var (
		dbPath        string
		limit         int
		kind          string
		client        string
		agent         string
		sessionID     string
		repo          string
		failuresOnly  bool
		asJSON        bool
		wide          bool
		utc           bool
		fields        []string
		preset        string
		color         string
		followSession string
	)

	tailCmd := &cobra.Command{
		Use:   "tail",
		Short: Localize("Follow new events as they arrive", "新しいイベントを追跡表示する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runTail(cmd.Context(), cmd.ErrOrStderr(), cmd.OutOrStdout(), tailCommandInput{
				dbPath:           dbPath,
				limit:            limit,
				kind:             kind,
				client:           client,
				agent:            agent,
				sessionID:        sessionID,
				repo:             repo,
				failuresOnly:     failuresOnly,
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
				followSession:    followSession,
				followSessionSet: cmd.Flags().Changed("follow-session"),
			})
		},
	}
	tailCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	tailCmd.Flags().IntVar(&limit, "limit", defaultTailInitialLimit, Localize("number of recent events to print before following (0 prints only new events)", "追跡開始前に表示する直近イベント数 (0 の場合は新規イベントのみ表示)"))
	tailCmd.Flags().StringVar(&kind, "kind", "", Localize("filter by event kind (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt, transcript; alias: audit)", "イベント種別で絞り込む (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt, transcript; alias: audit)"))
	tailCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "記録経路で絞り込む"))
	tailCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "作業主体で絞り込む"))
	tailCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("filter by session ID", "session ID で絞り込む"))
	tailCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by auxiliary workspace identifier", "補助的な workspace 識別子で絞り込む"))
	tailCmd.Flags().BoolVar(&failuresOnly, "failures", false, Localize("show only failed commands", "失敗したコマンドのみ表示"))
	tailCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print NDJSON output", "NDJSON 形式で出力する"))
	tailCmd.Flags().BoolVar(&wide, "wide", false, Localize("use the legacy tab-separated seven-column format", "従来のタブ区切り 7 カラム形式で出力する"))
	tailCmd.Flags().BoolVar(&utc, "utc", false, Localize("print text timestamps in UTC instead of local time", "テキスト出力のタイムスタンプを現地時刻ではなく UTC で出力する"))
	tailCmd.Flags().StringSliceVar(&fields, "fields", nil, readFieldsFlagUsage())
	tailCmd.Flags().StringVar(&preset, "preset", "", readPresetsFlagUsage())
	tailCmd.Flags().StringVar(&color, "color", "", readColorFlagUsage())
	tailCmd.Flags().StringVar(&followSession, "follow-session", "", Localize(
		"tail events only from the given session id (prefix match, minimum 8 runes)",
		"指定した session id のイベントだけを追跡する (先頭一致、最低 8 文字)",
	))

	return tailCmd
}

func (c *RootCLI) runTail(ctx context.Context, warnWriter io.Writer, output io.Writer, input tailCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.New(Localize("list events query service is not configured", "イベント一覧クエリサービスが設定されていません"))
	}
	if input.limit < 0 {
		return xerrors.New(Localize("limit must be greater than or equal to 0", "limit は 0 以上である必要があります"))
	}
	followSessionPrefix, err := validateFollowSessionPrefix(input.followSession)
	if err != nil {
		return err
	}
	preset, _, err := resolveReadPreset(input.preset, c.readPresets, warnWriter)
	if err != nil {
		return err
	}
	applyReadPresetToTailInput(&input, preset)
	resolvedKind, err := resolveListKind(input.kind)
	if err != nil {
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

	baseCriteria := apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).
		Kind(types.EventKind(resolvedKind)).
		Client(types.Client(strings.TrimSpace(input.client))).
		Agent(types.Agent(strings.TrimSpace(input.agent))).
		SessionID(types.SessionID(strings.TrimSpace(input.sessionID))).
		Workspace(types.Workspace(resolveWorkspaceValue(ctx, input.repo))).
		FailuresOnly(input.failuresOnly)

	resolvedFields, err := c.resolveReadFieldsForCommand(input.fields, input.fieldsSet, input.wide, input.asJSON, preset.fields)
	if err != nil {
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
	writer := newTailEventWriter(output, input.asJSON, textOpts, extrasFor)
	cursor := newTailCursor(input.resolvedNowFunc()().UTC())
	if input.limit > 0 {
		initialCriteria := apptypes.NewEventListCriteriaBuilder(input.limit).
			Kind(baseCriteria.Build().Kind()).
			Client(baseCriteria.Build().Client()).
			Agent(baseCriteria.Build().Agent()).
			SessionID(baseCriteria.Build().SessionID()).
			Workspace(baseCriteria.Build().Workspace()).
			FailuresOnly(baseCriteria.Build().FailuresOnly()).
			Build()
		initialEvents, err := c.event.List(ctx, initialCriteria)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to list initial tail events", "tail 初期イベントの取得に失敗しました"), err)
		}
		slices.Reverse(initialEvents)
		// Advance the cursor over the full (unfiltered) initial window so the
		// polling loop below resumes after the last observed event even when
		// --follow-session hides every row.
		if len(initialEvents) > 0 {
			cursor = newTailCursor(initialEvents[len(initialEvents)-1].CreatedAt())
			cursor.Advance(initialEvents)
		}
		filteredInitial := filterEventsBySessionPrefix(initialEvents, followSessionPrefix)
		if err := writer.Write(filteredInitial); err != nil {
			return err
		}
	} else if err := writer.EnsureReady(); err != nil {
		return err
	}

	ticker := input.resolvedTickerFactory()(defaultTailPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			pollSnapshotTo := input.resolvedNowFunc()().UTC()
			newEvents, err := c.pollTailEvents(ctx, baseCriteria.Build(), cursor, pollSnapshotTo)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to poll tail events", "tail イベントのポーリングに失敗しました"), err)
			}
			// Advance over the full poll batch before --follow-session drops
			// rows so the next poll window starts after the last observed
			// event even when none of them match the prefix filter.
			cursor.Advance(newEvents)
			filteredEvents := filterEventsBySessionPrefix(newEvents, followSessionPrefix)
			if len(filteredEvents) == 0 {
				continue
			}
			if err := writer.Write(filteredEvents); err != nil {
				return err
			}
		}
	}
}

// followSessionMinRunes is the minimum length accepted by --follow-session.
// Anything shorter would risk matching far too many sessions to be useful
// as a filter and the error steers the operator to paste the full id
// prefix from session list output.
const followSessionMinRunes = 8

// validateFollowSessionPrefix checks the --follow-session value. Empty
// input is valid (filter is off). Otherwise the prefix must be at least 8
// runes long so a stray short input does not silently match the entire
// store.
func validateFollowSessionPrefix(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if runeLen(trimmed) < followSessionMinRunes {
		return "", xerrors.New(Localizef(
			"--follow-session requires at least %d runes",
			"--follow-session には最低 %d 文字必要です",
			followSessionMinRunes,
		))
	}
	return trimmed, nil
}

// filterEventsBySessionPrefix returns the subset of events whose session id
// starts with prefix. An empty prefix is a no-op so callers do not have to
// branch.
func filterEventsBySessionPrefix(events []*model.Event, prefix string) []*model.Event {
	if prefix == "" {
		return events
	}
	filtered := make([]*model.Event, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		if strings.HasPrefix(event.SessionID().String(), prefix) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// pollTailEvents fetches every event in [cursor.timestamp, snapshotTo) via a
// single ListWindow call so the paged scan runs under the query service's
// stable read snapshot. Events already emitted at the From boundary are
// filtered out via the cursor's seenIDs set, and the result is reversed into
// oldest-first order for tail output.
func (c *RootCLI) pollTailEvents(
	ctx context.Context,
	base apptypes.EventListCriteria,
	cursor tailCursor,
	snapshotTo time.Time,
) ([]*model.Event, error) {
	if snapshotTo.IsZero() {
		snapshotTo = time.Now().UTC()
	}

	criteria := apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).
		Kind(base.Kind()).
		Client(base.Client()).
		Agent(base.Agent()).
		SessionID(base.SessionID()).
		Workspace(base.Workspace()).
		FailuresOnly(base.FailuresOnly()).
		From(cursor.timestamp).
		To(snapshotTo).
		Build()

	events, err := c.event.ListWindow(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list tail window: %w", err)
	}

	filtered := make([]*model.Event, 0, len(events))
	for _, event := range events {
		if cursor.isNew(event) {
			filtered = append(filtered, event)
		}
	}
	slices.Reverse(filtered)
	return filtered, nil
}
