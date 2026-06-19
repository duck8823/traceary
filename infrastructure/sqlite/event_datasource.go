package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/insert_event.sql
var insertEventQuery string

//go:embed sql/insert_command_audit.sql
var insertCommandAuditQuery string

//go:embed sql/select_recent_events.sql
var selectRecentEventsQuery string

//go:embed sql/select_recent_events_by_source_hook.sql
var selectRecentEventsBySourceHookQuery string

//go:embed sql/select_recent_events_by_source_hook_with_legacy.sql
var selectRecentEventsBySourceHookWithLegacyQuery string

//go:embed sql/search_events.sql
var searchEventsQuery string

//go:embed sql/get_context_events.sql
var getContextEventsQuery string

//go:embed sql/get_event_details.sql
var getEventDetailsQuery string

//go:embed sql/list_timeline_blocks.sql
var listTimelineBlocksQuery string

const duplicateHookCommandAuditWindow = 2 * time.Second

// duplicateHookContentEventWindow bounds the duplicate-suppression window for
// hook-originated content events (prompt / transcript). Hosts occasionally
// re-fire these hooks in immediate succession (~1ms apart), producing identical
// rows that double the noise in context retrieval, memory extraction, and
// operator review. It is intentionally a separate constant from
// duplicateHookCommandAuditWindow: command audits keep their own semantics
// (command re-runs are meaningful), so the two windows must be able to diverge.
const duplicateHookContentEventWindow = 2 * time.Second

// EventDatasource is the SQLite-backed implementation of the event
// repository and event query service.
type EventDatasource struct {
	db                *Database
	onListWindowBatch func(batchIndex, batchSize int)
}

// NewEventDatasource creates a new EventDatasource bound to the given
// database.
func NewEventDatasource(db *Database) *EventDatasource {
	return &EventDatasource{db: db}
}

// Compile-time interface assertions.
var (
	_ model.EventRepository          = (*EventDatasource)(nil)
	_ queryservice.EventQueryService = (*EventDatasource)(nil)
)

// Save persists an event.
func (d *EventDatasource) Save(ctx context.Context, event *model.Event) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for event save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	if isDedupEligibleHookContentEvent(event) {
		return d.saveDedupedHookContentEvent(ctx, db, event)
	}

	if _, err := db.ExecContext(
		ctx,
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	return nil
}

// isDedupEligibleHookContentEvent reports whether an event should pass through
// the duplicate-window guard. Only hook-originated prompt/transcript content is
// eligible: direct CLI / MCP writes (non-"hook" client) and other kinds
// (note, session boundaries, compact_summary, reviewed, command_executed) keep
// the unconditional insert path. Command audits are handled separately by
// SaveWithAudit and are deliberately excluded here.
func isDedupEligibleHookContentEvent(event *model.Event) bool {
	if event.Client().String() != "hook" {
		return false
	}
	switch event.Kind() {
	case types.EventKindPrompt, types.EventKindTranscript:
		return true
	default:
		return false
	}
}

// saveDedupedHookContentEvent inserts a hook-originated content event unless an
// identical one already exists within duplicateHookContentEventWindow. The
// duplicate check and the insert run in a single transaction, mirroring
// SaveWithAudit, so the guard and the write are atomic.
func (d *EventDatasource) saveDedupedHookContentEvent(ctx context.Context, db *sql.DB, event *model.Event) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin hook content event transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	duplicate, err := hookContentEventDuplicateExists(ctx, tx, event)
	if err != nil {
		return err
	}
	if duplicate {
		slog.Debug(
			"suppressed duplicate hook content event within window",
			"kind", event.Kind().String(),
			"client", event.Client().String(),
			"agent", event.Agent().String(),
			"session_id", event.SessionID().String(),
			"workspace", event.Workspace().String(),
			"source_hook", event.SourceHook(),
			"window", duplicateHookContentEventWindow.String(),
		)
		return nil
	}

	if _, err := tx.ExecContext(
		ctx,
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit hook content event transaction: %w", err)
	}

	return nil
}

// hookContentEventDuplicateExists reports whether an identical hook content
// event already exists within duplicateHookContentEventWindow of the new
// event's created_at. The identity tuple matches the audit guard's shape minus
// the command_audits join: kind, client, agent, session_id, workspace,
// source_hook, and body. kind is bound to the event's own kind, so a prompt
// never deduplicates a transcript.
//
// events.created_at is stored as variable-width RFC3339Nano (see
// formatTimestamp), which is NOT lexically ordered the same as real time: e.g.
// "…00.5Z" sorts before "…00Z". A plain TEXT range over created_at can thus
// miss a genuine duplicate or over-suppress near fractional-second boundaries.
// We therefore coarsely pre-filter on the fixed-width whole-second prefix
// (substr 1..19 is always "YYYY-MM-DDTHH:MM:SS" for the UTC timestamps
// formatTimestamp emits), widened by an extra second so it is a guaranteed
// superset of the true window, then compare parsed timestamps exactly in Go.
func hookContentEventDuplicateExists(ctx context.Context, tx *sql.Tx, event *model.Event) (bool, error) {
	const secondPrefixLayout = "2006-01-02T15:04:05"
	coarse := duplicateHookContentEventWindow + time.Second
	lowerPrefix := event.CreatedAt().Add(-coarse).UTC().Format(secondPrefixLayout)
	upperPrefix := event.CreatedAt().Add(coarse).UTC().Format(secondPrefixLayout)

	rows, err := tx.QueryContext(
		ctx,
		`SELECT e.created_at
		   FROM events e
		  WHERE e.kind = ?
		    AND e.client = ?
		    AND e.agent = ?
		    AND e.session_id = ?
		    AND e.workspace = ?
		    AND COALESCE(e.source_hook, '') = ?
		    AND e.body = ?
		    AND substr(e.created_at, 1, 19) >= ?
		    AND substr(e.created_at, 1, 19) <= ?`,
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.SourceHook(),
		event.Body(),
		lowerPrefix,
		upperPrefix,
	)
	if err != nil {
		return false, xerrors.Errorf("failed to query duplicate hook content event candidates: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	for rows.Next() {
		var createdAtText string
		if err := rows.Scan(&createdAtText); err != nil {
			return false, xerrors.Errorf("failed to scan duplicate hook content event candidate: %w", err)
		}
		candidateAt, err := time.Parse(time.RFC3339Nano, createdAtText)
		if err != nil {
			// A malformed historical timestamp must not block the write; skip it
			// and keep checking the remaining candidates.
			slog.Debug("skipping unparseable candidate timestamp", "created_at", createdAtText, "error", err)
			continue
		}
		if event.CreatedAt().Sub(candidateAt).Abs() <= duplicateHookContentEventWindow {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, xerrors.Errorf("failed to iterate duplicate hook content event candidates: %w", err)
	}
	return false, nil
}

// SaveWithAudit persists an event together with its command audit.
func (d *EventDatasource) SaveWithAudit(
	ctx context.Context,
	event *model.Event,
	audit *model.CommandAudit,
) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}
	if audit == nil {
		return xerrors.Errorf("command audit must not be nil")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for command audit save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin command audit transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	duplicate, err := hookCommandAuditDuplicateExists(ctx, tx, event, audit)
	if err != nil {
		return err
	}
	if duplicate {
		return nil
	}

	if _, err := tx.ExecContext(
		ctx,
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	var exitCodeSQL *int
	if exitCode, ok := audit.ExitCode().Value(); ok {
		exitCodeSQL = &exitCode
	}
	if _, err := tx.ExecContext(
		ctx,
		insertCommandAuditQuery,
		audit.EventID().String(),
		audit.Command(),
		audit.Input(),
		audit.Output(),
		audit.InputTruncated(),
		audit.OutputTruncated(),
		audit.InputOriginalBytes(),
		audit.OutputOriginalBytes(),
		exitCodeSQL,
		audit.Failed(),
	); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit command audit transaction: %w", err)
	}

	return nil
}

func hookCommandAuditDuplicateExists(
	ctx context.Context,
	tx *sql.Tx,
	event *model.Event,
	audit *model.CommandAudit,
) (bool, error) {
	if event.Client().String() != "hook" {
		return false, nil
	}

	var exitCodeSQL *int
	hasExitCode := 0
	if exitCode, ok := audit.ExitCode().Value(); ok {
		exitCodeSQL = &exitCode
		hasExitCode = 1
	}
	from := formatTimestamp(event.CreatedAt().Add(-duplicateHookCommandAuditWindow))
	to := formatTimestamp(event.CreatedAt().Add(duplicateHookCommandAuditWindow))

	var value int
	err := tx.QueryRowContext(
		ctx,
		`SELECT 1
		   FROM events e
		   JOIN command_audits ca ON ca.event_id = e.id
		  WHERE e.kind = ?
		    AND e.client = ?
		    AND e.agent = ?
		    AND e.session_id = ?
		    AND e.workspace = ?
		    AND COALESCE(e.source_hook, '') = ?
		    AND ca.command_text = ?
		    AND ca.input_text = ?
		    AND ca.output_text = ?
		    AND ca.input_truncated = ?
		    AND ca.output_truncated = ?
		    AND ca.input_original_bytes = ?
		    AND ca.output_original_bytes = ?
		    AND ((? = 0 AND ca.exit_code IS NULL) OR (? = 1 AND ca.exit_code = ?))
		    AND ca.failed = ?
		    AND e.created_at >= ?
		    AND e.created_at <= ?
		  LIMIT 1`,
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.SourceHook(),
		audit.Command(),
		audit.Input(),
		audit.Output(),
		audit.InputTruncated(),
		audit.OutputTruncated(),
		audit.InputOriginalBytes(),
		audit.OutputOriginalBytes(),
		hasExitCode,
		hasExitCode,
		exitCodeSQL,
		audit.Failed(),
		from,
		to,
	).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check duplicate hook command audit: %w", err)
}

// ListRecent returns events in descending time order.
func (d *EventDatasource) ListRecent(
	ctx context.Context,
	limit, offset int,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresOnly bool,
	from, to time.Time,
	sourceHook string,
) ([]*model.Event, error) {
	if limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	fromValue := ""
	if !from.IsZero() {
		fromValue = formatTimestamp(from)
	}
	toValue := ""
	if !to.IsZero() {
		toValue = formatTimestamp(to)
	}

	rows, err := queryRecentEvents(
		ctx, db, sourceHook,
		kind, client, agent, sessionID, workspace,
		failuresOnly,
		fromValue, toValue,
		limit, offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query recent events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	events := make([]*model.Event, 0, limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore event row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate recent event rows: %w", err)
	}

	return events, nil
}

// ListWindow returns every event matching the criteria whose created_at falls
// in [From, To). The entire paged scan runs inside a single read transaction
// so SQLite snapshot isolation protects it from concurrent writers shifting
// later pages — which would otherwise cause OFFSET-based pagination to drop
// rows. Callers provide the per-page size via criteria.Limit(); offset is
// ignored.
func (d *EventDatasource) ListWindow(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
) ([]*model.Event, error) {
	batch := criteria.Limit()
	if batch <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() != 0 {
		return nil, xerrors.Errorf("offset must be zero for ListWindow (paging is handled internally)")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event window listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, xerrors.Errorf("failed to begin event window read transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	fromValue := ""
	if !criteria.From().IsZero() {
		fromValue = formatTimestamp(criteria.From())
	}
	toValue := ""
	if !criteria.To().IsZero() {
		toValue = formatTimestamp(criteria.To())
	}

	kind := criteria.Kind()
	client := criteria.Client()
	agent := criteria.Agent()
	sessionID := criteria.SessionID()
	workspace := criteria.Workspace()
	failuresFlag := boolToInt(criteria.FailuresOnly())
	sourceHook := criteria.SourceHook()

	events := make([]*model.Event, 0, batch)
	offset := 0
	for {
		rows, err := queryRecentEventsTx(
			ctx, tx, sourceHook,
			kind, client, agent, sessionID, workspace,
			failuresFlag,
			fromValue, toValue,
			batch, offset,
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to query event window page: %w", err)
		}

		pageCount := 0
		for rows.Next() {
			event, err := scanEvent(rows)
			if err != nil {
				if closeErr := rows.Close(); closeErr != nil {
					slog.Debug("failed to close resource", "error", closeErr)
				}
				return nil, xerrors.Errorf("failed to restore event window row: %w", err)
			}
			events = append(events, event)
			pageCount++
		}
		if err := rows.Err(); err != nil {
			if closeErr := rows.Close(); closeErr != nil {
				slog.Debug("failed to close resource", "error", closeErr)
			}
			return nil, xerrors.Errorf("failed to iterate event window rows: %w", err)
		}
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}

		if d.onListWindowBatch != nil {
			d.onListWindowBatch(offset/batch, pageCount)
		}
		if pageCount < batch {
			break
		}
		offset += pageCount
	}

	return events, nil
}

// Search returns matching events in descending time order.
func (d *EventDatasource) Search(
	ctx context.Context,
	query string, workspace types.Workspace, sessionID types.SessionID, client types.Client, agent types.Agent, kind types.EventKind,
	from, to time.Time,
	limit, offset int,
	failuresOnly bool,
) ([]*model.Event, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event search: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	queryValue := strings.TrimSpace(query)
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	fromValue := ""
	if !from.IsZero() {
		fromValue = formatTimestamp(from)
	}
	toValue := ""
	if !to.IsZero() {
		toValue = formatTimestamp(to)
	}

	rows, err := db.QueryContext(
		ctx,
		searchEventsQuery,
		queryValue,
		likeQuery,
		likeQuery,
		likeQuery,
		likeQuery,
		workspace.String(),
		workspace.String(),
		sessionID.String(),
		sessionID.String(),
		client.String(),
		client.String(),
		agent.String(),
		agent.String(),
		kind.String(),
		kind.String(),
		fromValue,
		fromValue,
		toValue,
		toValue,
		boolToInt(failuresOnly),
		limit,
		offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	events := make([]*model.Event, 0, limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore search result row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate search result rows: %w", err)
	}

	return events, nil
}

// GetContext returns events matching the requested context in descending
// time order.
func (d *EventDatasource) GetContext(
	ctx context.Context,
	workspace types.Workspace, sessionID types.SessionID,
	limit int,
) ([]*model.Event, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for context lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	trimmedWorkspace := strings.TrimSpace(workspace.String())
	trimmedSessionID := strings.TrimSpace(sessionID.String())
	rows, err := db.QueryContext(
		ctx,
		getContextEventsQuery,
		trimmedWorkspace,
		trimmedWorkspace,
		trimmedSessionID,
		trimmedSessionID,
		limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query context events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	events := make([]*model.Event, 0, limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore context event row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate context event rows: %w", err)
	}

	return events, nil
}

// GetDetails returns the details for the given event ID.
func (d *EventDatasource) GetDetails(
	ctx context.Context,
	eventID types.EventID,
) (apptypes.EventDetails, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.EventDetails{}, xerrors.Errorf("failed to open DB for event details lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	row := db.QueryRowContext(
		ctx,
		getEventDetailsQuery,
		eventID.String(),
	)

	var (
		commandTextValue     sql.NullString
		inputTextValue       sql.NullString
		outputTextValue      sql.NullString
		inputTruncatedValue  sql.NullBool
		outputTruncatedValue sql.NullBool
		inputOriginalBytes   sql.NullInt64
		outputOriginalBytes  sql.NullInt64
		exitCodeValue        sql.NullInt64
		failedValue          sql.NullBool
	)

	event, err := scanEventWithAudit(
		row,
		&commandTextValue,
		&inputTextValue,
		&outputTextValue,
		&inputTruncatedValue,
		&outputTruncatedValue,
		&inputOriginalBytes,
		&outputOriginalBytes,
		&exitCodeValue,
		&failedValue,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apptypes.EventDetails{}, xerrors.Errorf("event not found: %s", eventID)
		}
		return apptypes.EventDetails{}, xerrors.Errorf("failed to restore event details row: %w", err)
	}

	commandAuditOpt := types.None[*model.CommandAudit]()
	if commandTextValue.Valid {
		exitCode := types.None[int]()
		if exitCodeValue.Valid {
			exitCode = types.Some(int(exitCodeValue.Int64))
		}
		commandAudit := model.CommandAuditOf(
			eventID,
			commandTextValue.String,
			inputTextValue.String,
			outputTextValue.String,
			inputTruncatedValue.Bool,
			outputTruncatedValue.Bool,
			exitCode,
			failedValue.Bool,
		)
		commandAudit.SetOriginalPayloadBytes(int(inputOriginalBytes.Int64), int(outputOriginalBytes.Int64))
		commandAuditOpt = types.Some(commandAudit)
	}

	eventDetails, err := apptypes.EventDetailsOf(event, commandAuditOpt)
	if err != nil {
		return apptypes.EventDetails{}, xerrors.Errorf("failed to build event details: %w", err)
	}

	return eventDetails, nil
}

// ListTimelineBlocks returns work blocks separated by idle gaps.
func (d *EventDatasource) ListTimelineBlocks(
	ctx context.Context,
	workspace types.Workspace,
	from, to time.Time,
	gapSeconds, limit int,
) ([]apptypes.TimelineBlock, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for timeline listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	fromValue := ""
	if !from.IsZero() {
		fromValue = formatTimestamp(from)
	}
	toValue := ""
	if !to.IsZero() {
		toValue = formatTimestamp(to)
	}

	rows, err := db.QueryContext(
		ctx,
		listTimelineBlocksQuery,
		workspace.String(), workspace.String(),
		fromValue, fromValue,
		toValue, toValue,
		gapSeconds,
		limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query timeline blocks: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close rows", "error", err)
		}
	}()

	// The query returns one row per (block, workspace). We preserve the
	// insertion order of both block_num values (ordered DESC by block_start
	// in SQL) and their per-workspace rows (ordered DESC by ws_event_count,
	// then by workspace name) so the Go slice mirrors the SQL ORDER BY.
	type blockAccumulator struct {
		blockStart time.Time
		blockEnd   time.Time
		eventCount int
		agents     []string
		breakdown  []apptypes.TimelineWorkspaceBreakdown
	}

	var blockOrder []int
	blockMap := make(map[int]*blockAccumulator)

	for rows.Next() {
		var (
			blockNum            int
			blockStart          string
			blockEnd            string
			blockEventCount     int
			agents              string
			workspace           string
			wsEventCount        int
			kinds               string
			wsAgents            string
			firstPromptBody     string
			compactSummaryBody  string
			firstTranscriptBody string
		)
		if err := rows.Scan(
			&blockNum,
			&blockStart,
			&blockEnd,
			&blockEventCount,
			&agents,
			&workspace,
			&wsEventCount,
			&kinds,
			&wsAgents,
			&firstPromptBody,
			&compactSummaryBody,
			&firstTranscriptBody,
		); err != nil {
			return nil, xerrors.Errorf("failed to scan timeline block: %w", err)
		}

		accum, ok := blockMap[blockNum]
		if !ok {
			parsedStart, err := time.Parse(time.RFC3339Nano, blockStart)
			if err != nil {
				return nil, xerrors.Errorf("failed to parse block start: %w", err)
			}
			parsedEnd, err := time.Parse(time.RFC3339Nano, blockEnd)
			if err != nil {
				return nil, xerrors.Errorf("failed to parse block end: %w", err)
			}
			accum = &blockAccumulator{
				blockStart: parsedStart,
				blockEnd:   parsedEnd,
				eventCount: blockEventCount,
				agents:     splitNonEmpty(agents, ","),
			}
			blockMap[blockNum] = accum
			blockOrder = append(blockOrder, blockNum)
		}

		kindList := splitNonEmpty(kinds, "|")
		wsAgentList := splitNonEmpty(wsAgents, ",")
		summary, source := resolveWorkspaceSummary(compactSummaryBody, firstPromptBody, firstTranscriptBody)
		accum.breakdown = append(accum.breakdown, apptypes.TimelineWorkspaceBreakdownOf(
			workspace,
			wsEventCount,
			kindList,
			wsAgentList,
			summary,
			source,
		))
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate timeline blocks: %w", err)
	}

	blocks := make([]apptypes.TimelineBlock, 0, len(blockOrder))
	for _, num := range blockOrder {
		accum := blockMap[num]
		blocks = append(blocks, apptypes.TimelineBlockOf(
			accum.blockStart,
			accum.blockEnd,
			accum.eventCount,
			accum.agents,
			accum.breakdown,
		))
	}

	return blocks, nil
}

// resolveWorkspaceSummary applies the fallback chain
// compact_summary → prompt → transcript → kind_counts. Whitespace-only
// candidates are treated as absent so blank rows cannot override a later
// non-blank candidate (SQLite TRIM only strips spaces, not tabs/newlines,
// so this defense is enforced in Go rather than in SQL).
func resolveWorkspaceSummary(compactSummaryBody, firstPromptBody, firstTranscriptBody string) (string, apptypes.TimelineWorkspaceBreakdownSummarySource) {
	if strings.TrimSpace(compactSummaryBody) != "" {
		return compactSummaryBody, apptypes.TimelineSummarySourceCompactSummary
	}
	if strings.TrimSpace(firstPromptBody) != "" {
		return firstPromptBody, apptypes.TimelineSummarySourcePrompt
	}
	if strings.TrimSpace(firstTranscriptBody) != "" {
		return firstTranscriptBody, apptypes.TimelineSummarySourceTranscript
	}
	return "", apptypes.TimelineSummarySourceKindCounts
}

// scanEvent reads a single event row into a model.Event.
func scanEvent(rowScanner interface {
	Scan(dest ...any) error
}) (*model.Event, error) {
	var (
		eventIDValue    string
		eventKindValue  string
		clientValue     string
		agentValue      string
		sessionIDValue  string
		repoValue       string
		bodyValue       string
		sourceHookValue sql.NullString
		createdAtValue  string
	)

	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&repoValue,
		&bodyValue,
		&sourceHookValue,
		&createdAtValue,
	); err != nil {
		return nil, xerrors.Errorf("failed to scan event row: %w", err)
	}

	return restoreEvent(
		eventIDValue,
		eventKindValue,
		clientValue,
		agentValue,
		sessionIDValue,
		repoValue,
		bodyValue,
		sourceHookValue.String,
		createdAtValue,
	)
}

// scanEventWithAudit reads an event row joined with its command audit
// columns.
func scanEventWithAudit(
	rowScanner interface {
		Scan(dest ...any) error
	},
	commandTextValue *sql.NullString,
	inputTextValue *sql.NullString,
	outputTextValue *sql.NullString,
	inputTruncatedValue *sql.NullBool,
	outputTruncatedValue *sql.NullBool,
	inputOriginalBytes *sql.NullInt64,
	outputOriginalBytes *sql.NullInt64,
	exitCodeValue *sql.NullInt64,
	failedValue *sql.NullBool,
) (*model.Event, error) {
	var (
		eventIDValue    string
		eventKindValue  string
		clientValue     string
		agentValue      string
		sessionIDValue  string
		repoValue       string
		bodyValue       string
		sourceHookValue sql.NullString
		createdAtValue  string
	)

	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&repoValue,
		&bodyValue,
		&sourceHookValue,
		&createdAtValue,
		commandTextValue,
		inputTextValue,
		outputTextValue,
		inputTruncatedValue,
		outputTruncatedValue,
		inputOriginalBytes,
		outputOriginalBytes,
		exitCodeValue,
		failedValue,
	); err != nil {
		return nil, xerrors.Errorf("failed to scan event details row: %w", err)
	}

	return restoreEvent(
		eventIDValue,
		eventKindValue,
		clientValue,
		agentValue,
		sessionIDValue,
		repoValue,
		bodyValue,
		sourceHookValue.String,
		createdAtValue,
	)
}

func restoreEvent(
	eventIDValue string,
	eventKindValue string,
	clientValue string,
	agentValue string,
	sessionIDValue string,
	repoValue string,
	bodyValue string,
	sourceHookValue string,
	createdAtValue string,
) (*model.Event, error) {
	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore event ID: %w", err)
	}
	eventKind, err := types.EventKindFrom(eventKindValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore event kind: %w", err)
	}
	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore agent: %w", err)
	}
	sessionID, err := types.SessionIDFrom(sessionIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore session ID: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore created_at: %w", err)
	}

	return model.EventOfWithSourceHook(
		eventID,
		eventKind,
		types.Client(clientValue),
		agent,
		sessionID,
		types.Workspace(repoValue),
		bodyValue,
		createdAt,
		sourceHookValue,
	), nil
}

// sourceHookHasLegacyPrefix reports whether a source_hook value maps
// to a pre-#672 body-prefix marker that the reader must still match
// (only subagent_stop and pre_compact have legacy rows to worry
// about; every other hook name was stamped from day one).
func sourceHookHasLegacyPrefix(sourceHook string) bool {
	return sourceHook == "subagent_stop" || sourceHook == "pre_compact"
}

// queryRecentEvents dispatches between three SQL queries:
//   - sourceHook == "": no filter — use the plain query that planners
//     already serve via idx_events_created_at.
//   - sourceHook in {subagent_stop, pre_compact}: UNION ALL form that
//     includes a legacy-body-prefix branch so pre-#672 rows stay
//     reachable during the migration window.
//   - other sourceHook: primary-only query covered by the compound
//     partial index idx_events_source_hook_time.
//
// The legacy UNION ALL branch is NOT used for other hook names because
// SQLite would still scan the right branch via idx_events_created_at
// even when its WHERE filters returned zero rows, negating the index
// gain from #683.
func queryRecentEvents(
	ctx context.Context,
	db *sql.DB,
	sourceHook string,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresOnly bool,
	fromValue, toValue string,
	limit, offset int,
) (*sql.Rows, error) {
	failuresFlag := boolToInt(failuresOnly)
	if sourceHook == "" {
		rows, err := db.QueryContext(
			ctx,
			selectRecentEventsQuery,
			kind.String(), kind.String(),
			client.String(), client.String(),
			agent.String(), agent.String(),
			sessionID.String(), sessionID.String(),
			workspace.String(), workspace.String(),
			failuresFlag,
			fromValue, fromValue,
			toValue, toValue,
			limit,
			offset,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent events: %w", err)
		}
		return rows, nil
	}
	if sourceHookHasLegacyPrefix(sourceHook) {
		rows, err := db.QueryContext(
			ctx,
			selectRecentEventsBySourceHookWithLegacyQuery,
			sourceHookLegacyQueryArgs(sourceHook, kind, client, agent, sessionID, workspace, failuresFlag, fromValue, toValue, limit, offset)...,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent events by source_hook with legacy: %w", err)
		}
		return rows, nil
	}
	rows, err := db.QueryContext(
		ctx,
		selectRecentEventsBySourceHookQuery,
		sourceHookPrimaryQueryArgs(sourceHook, kind, client, agent, sessionID, workspace, failuresFlag, fromValue, toValue, limit, offset)...,
	)
	if err != nil {
		return nil, xerrors.Errorf("query recent events by source_hook: %w", err)
	}
	return rows, nil
}

func queryRecentEventsTx(
	ctx context.Context,
	tx *sql.Tx,
	sourceHook string,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresFlag int,
	fromValue, toValue string,
	batch, offset int,
) (*sql.Rows, error) {
	if sourceHook == "" {
		rows, err := tx.QueryContext(
			ctx,
			selectRecentEventsQuery,
			kind.String(), kind.String(),
			client.String(), client.String(),
			agent.String(), agent.String(),
			sessionID.String(), sessionID.String(),
			workspace.String(), workspace.String(),
			failuresFlag,
			fromValue, fromValue,
			toValue, toValue,
			batch,
			offset,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent events tx: %w", err)
		}
		return rows, nil
	}
	if sourceHookHasLegacyPrefix(sourceHook) {
		rows, err := tx.QueryContext(
			ctx,
			selectRecentEventsBySourceHookWithLegacyQuery,
			sourceHookLegacyQueryArgs(sourceHook, kind, client, agent, sessionID, workspace, failuresFlag, fromValue, toValue, batch, offset)...,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent events by source_hook with legacy tx: %w", err)
		}
		return rows, nil
	}
	rows, err := tx.QueryContext(
		ctx,
		selectRecentEventsBySourceHookQuery,
		sourceHookPrimaryQueryArgs(sourceHook, kind, client, agent, sessionID, workspace, failuresFlag, fromValue, toValue, batch, offset)...,
	)
	if err != nil {
		return nil, xerrors.Errorf("query recent events by source_hook tx: %w", err)
	}
	return rows, nil
}

// sourceHookPrimaryQueryArgs returns the parameter slice for the
// primary-only source_hook query. It mirrors the placeholders in
// select_recent_events_by_source_hook.sql.
func sourceHookPrimaryQueryArgs(
	sourceHook string,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresFlag int,
	fromValue, toValue string,
	limit, offset int,
) []any {
	return []any{
		sourceHook,
		kind.String(), kind.String(),
		client.String(), client.String(),
		agent.String(), agent.String(),
		sessionID.String(), sessionID.String(),
		workspace.String(), workspace.String(),
		failuresFlag,
		fromValue, fromValue,
		toValue, toValue,
		limit, offset,
	}
}

// sourceHookLegacyQueryArgs returns the parameter slice for the
// UNION ALL variant that includes the legacy body-prefix branch.
// The order mirrors the two subselects in
// select_recent_events_by_source_hook_with_legacy.sql before the
// outer LIMIT/OFFSET.
func sourceHookLegacyQueryArgs(
	sourceHook string,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresFlag int,
	fromValue, toValue string,
	limit, offset int,
) []any {
	common := []any{
		kind.String(), kind.String(),
		client.String(), client.String(),
		agent.String(), agent.String(),
		sessionID.String(), sessionID.String(),
		workspace.String(), workspace.String(),
		failuresFlag,
		fromValue, fromValue,
		toValue, toValue,
	}
	args := make([]any, 0, 2+len(common)*2+2)
	args = append(args, sourceHook)
	args = append(args, common...)
	args = append(args, sourceHook, sourceHook)
	args = append(args, common...)
	args = append(args, limit, offset)
	return args
}

func escapeLikeQuery(query string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)

	return replacer.Replace(query)
}

func splitNonEmpty(s string, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
