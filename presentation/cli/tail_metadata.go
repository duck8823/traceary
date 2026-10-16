package cli

import (
	"context"
	"io"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// runMetadataTail keeps explicit body-free JSON requests on the metadata read
// boundary for the entire stream, including command exit codes. In particular,
// it does not construct partial Events or use payload-based extras resolvers.
func (c *RootCLI) runMetadataTail(ctx context.Context, output io.Writer, input tailCommandInput, base apptypes.EventListCriteria, fields []readFieldID, sessionPrefix string) error {
	cursor := newTailCursor(input.resolvedNowFunc()().UTC())
	if input.limit > 0 {
		initial, err := c.eventMetadata.List(ctx, newTailCriteriaBuilder(base, input.limit).Build())
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to list initial tail events", "tail 初期イベントの取得に失敗しました"), err)
		}
		slices.Reverse(initial)
		if len(initial) > 0 {
			cursor = newTailCursor(initial[len(initial)-1].CreatedAt())
		}
		if err := writeTailMetadata(output, initial, fields, sessionPrefix, &cursor); err != nil {
			return err
		}
	}

	ticker := input.resolvedTickerFactory()(defaultTailPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			snapshotTo := input.resolvedNowFunc()().UTC()
			rows, err := c.pollTailMetadata(ctx, base, cursor, snapshotTo)
			if err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to poll tail events", "tail イベントのポーリングに失敗しました"), err)
			}
			if err := writeTailMetadata(output, rows, fields, sessionPrefix, &cursor); err != nil {
				return err
			}
		}
	}
}

func writeTailMetadata(output io.Writer, rows []apptypes.EventMetadata, fields []readFieldID, sessionPrefix string, cursor *tailCursor) error {
	for _, row := range rows {
		// Hidden sessions still advance the cursor, including boundary seen IDs.
		cursor.advanceAt(row.EventID(), row.CreatedAt())
		if !strings.HasPrefix(row.SessionID().String(), sessionPrefix) {
			continue
		}
		if err := writeTailJSONValue(output, newEventMetadataFieldsOutput(row, fields)); err != nil {
			return err
		}
	}
	return nil
}

func (c *RootCLI) pollTailMetadata(ctx context.Context, base apptypes.EventListCriteria, cursor tailCursor, snapshotTo time.Time) ([]apptypes.EventMetadata, error) {
	if snapshotTo.IsZero() {
		snapshotTo = time.Now().UTC()
	}
	criteria := newTailCriteriaBuilder(base, defaultTailBatchSize).From(cursor.timestamp).To(snapshotTo).Build()
	rows, err := c.eventMetadata.ListWindow(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list tail window: %w", err)
	}
	filtered := make([]apptypes.EventMetadata, 0, len(rows))
	for _, row := range rows {
		if cursor.isNewAt(row.EventID(), row.CreatedAt()) {
			filtered = append(filtered, row)
		}
	}
	slices.Reverse(filtered)
	return filtered, nil
}
