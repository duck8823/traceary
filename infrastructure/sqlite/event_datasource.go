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

//go:embed sql/search_events.sql
var searchEventsQuery string

//go:embed sql/get_context_events.sql
var getContextEventsQuery string

//go:embed sql/get_event_details.sql
var getEventDetailsQuery string

//go:embed sql/list_timeline_blocks.sql
var listTimelineBlocksQuery string

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
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	return nil
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
		exitCodeSQL,
	); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit command audit transaction: %w", err)
	}

	return nil
}

// ListRecent returns events in descending time order.
func (d *EventDatasource) ListRecent(
	ctx context.Context,
	limit, offset int,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresOnly bool,
	from, to time.Time,
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

	rows, err := db.QueryContext(
		ctx,
		selectRecentEventsQuery,
		kind.String(), kind.String(),
		client.String(), client.String(),
		agent.String(), agent.String(),
		sessionID.String(), sessionID.String(),
		workspace.String(), workspace.String(),
		boolToInt(failuresOnly),
		fromValue, fromValue,
		toValue, toValue,
		limit,
		offset,
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

	events := make([]*model.Event, 0, batch)
	offset := 0
	for {
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
		exitCodeValue        sql.NullInt64
	)

	event, err := scanEventWithAudit(
		row,
		&commandTextValue,
		&inputTextValue,
		&outputTextValue,
		&inputTruncatedValue,
		&outputTruncatedValue,
		&exitCodeValue,
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
		)
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
			blockNum             int
			blockStart           string
			blockEnd             string
			blockEventCount      int
			agents               string
			workspace            string
			wsEventCount         int
			kinds                string
			wsAgents             string
			firstPromptBody      string
			compactSummaryBody   string
			firstTranscriptBody  string
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
		eventIDValue   string
		eventKindValue string
		clientValue    string
		agentValue     string
		sessionIDValue string
		repoValue      string
		bodyValue      string
		createdAtValue string
	)

	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&repoValue,
		&bodyValue,
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
	exitCodeValue *sql.NullInt64,
) (*model.Event, error) {
	var (
		eventIDValue   string
		eventKindValue string
		clientValue    string
		agentValue     string
		sessionIDValue string
		repoValue      string
		bodyValue      string
		createdAtValue string
	)

	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&repoValue,
		&bodyValue,
		&createdAtValue,
		commandTextValue,
		inputTextValue,
		outputTextValue,
		inputTruncatedValue,
		outputTruncatedValue,
		exitCodeValue,
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
	createdAtValue string,
) (*model.Event, error) {
	eventID, err := types.EventIDOf(eventIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore event ID: %w", err)
	}
	eventKind, err := types.EventKindOf(eventKindValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore event kind: %w", err)
	}
	agent, err := types.AgentOf(agentValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore agent: %w", err)
	}
	sessionID, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore session ID: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore created_at: %w", err)
	}

	return model.EventOf(
		eventID,
		eventKind,
		types.Client(clientValue),
		agent,
		sessionID,
		types.Workspace(repoValue),
		bodyValue,
		createdAt,
	), nil
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
