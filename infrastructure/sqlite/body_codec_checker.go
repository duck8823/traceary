package sqlite

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

const bodyCodecSampleLimit = 5

// BodyCodecChecker reads events.body_codec and reports values the running
// binary cannot decode.
type BodyCodecChecker struct{ db *Database }

// NewBodyCodecChecker creates a read-only body codec checker.
func NewBodyCodecChecker(db *Database) *BodyCodecChecker {
	return &BodyCodecChecker{db: db}
}

var _ application.BodyCodecChecker = (*BodyCodecChecker)(nil)

// CheckBodyCodec returns any body_codec values not supported by this binary.
func (c *BodyCodecChecker) CheckBodyCodec(ctx context.Context) (_ application.BodyCodecState, err error) {
	db, err := c.db.openReadOnly(ctx)
	if err != nil {
		return application.BodyCodecState{}, xerrors.Errorf("open store for body codec check: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("close body codec check: %w", closeErr)
		}
	}()

	supported := SupportedBodyCodecs()
	placeholders := strings.Repeat("?,", len(supported))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(supported))
	for i, v := range supported {
		args[i] = v
	}

	rows, err := db.QueryContext(ctx,
		"SELECT body_codec, COUNT(*) FROM events WHERE body_codec NOT IN ("+placeholders+") GROUP BY body_codec ORDER BY body_codec",
		args...)
	if err != nil {
		return application.BodyCodecState{}, xerrors.Errorf("query unknown body codecs: %w", err)
	}

	var unknown []application.BodyCodecRow
	for rows.Next() {
		var row application.BodyCodecRow
		if err := rows.Scan(&row.Codec, &row.Count); err != nil {
			_ = rows.Close()
			return application.BodyCodecState{}, xerrors.Errorf("scan body codec row: %w", err)
		}
		unknown = append(unknown, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return application.BodyCodecState{}, xerrors.Errorf("iterate body codec rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return application.BodyCodecState{}, xerrors.Errorf("close body codec rows: %w", err)
	}

	for i := range unknown {
		sampleRows, err := db.QueryContext(ctx,
			"SELECT id FROM events WHERE body_codec = ? ORDER BY id LIMIT ?",
			unknown[i].Codec, bodyCodecSampleLimit)
		if err != nil {
			return application.BodyCodecState{}, xerrors.Errorf("query body codec sample ids: %w", err)
		}
		for sampleRows.Next() {
			var id string
			if err := sampleRows.Scan(&id); err != nil {
				_ = sampleRows.Close()
				return application.BodyCodecState{}, xerrors.Errorf("scan body codec sample id: %w", err)
			}
			unknown[i].SampleIDs = append(unknown[i].SampleIDs, id)
		}
		if err := sampleRows.Err(); err != nil {
			_ = sampleRows.Close()
			return application.BodyCodecState{}, xerrors.Errorf("iterate body codec sample ids: %w", err)
		}
		if err := sampleRows.Close(); err != nil {
			return application.BodyCodecState{}, xerrors.Errorf("close body codec sample rows: %w", err)
		}
	}

	return application.BodyCodecState{UnknownRows: unknown}, nil
}
