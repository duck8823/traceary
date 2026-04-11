package sqlite

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/list_timeline_blocks.sql
var listTimelineBlocksQuery string

// ListTimelineBlocks returns work blocks separated by idle gaps.
func (d *Datasource) ListTimelineBlocks(
	ctx context.Context,
	workspace types.Workspace,
	from, to time.Time,
	gapSeconds, limit int,
) ([]*queryservice.TimelineBlock, error) {
	db, err := d.openDB(ctx)
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

	var blocks []*queryservice.TimelineBlock
	for rows.Next() {
		var (
			blockNum   int // scanned but unused (SQL internal grouping key)
			blockStart string
			blockEnd   string
			eventCount int
			workspaces string
			agents     string
			kinds      string
		)
		if err := rows.Scan(&blockNum, &blockStart, &blockEnd, &eventCount, &workspaces, &agents, &kinds); err != nil {
			return nil, xerrors.Errorf("failed to scan timeline block: %w", err)
		}

		parsedStart, err := time.Parse(time.RFC3339Nano, blockStart)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse block start: %w", err)
		}
		parsedEnd, err := time.Parse(time.RFC3339Nano, blockEnd)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse block end: %w", err)
		}

		blocks = append(blocks, &queryservice.TimelineBlock{
			BlockStart: parsedStart,
			BlockEnd:   parsedEnd,
			EventCount: eventCount,
			Workspaces: splitNonEmpty(workspaces, ","),
			Agents:     splitNonEmpty(agents, ","),
			Kinds:      splitNonEmpty(kinds, "|"),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate timeline blocks: %w", err)
	}

	return blocks, nil
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
