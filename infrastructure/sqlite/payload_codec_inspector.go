package sqlite

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// PayloadCodecInspector reads only schema and aggregate codec metadata.
type PayloadCodecInspector struct{ db *Database }

// NewPayloadCodecInspector creates a read-only payload codec inspector.
func NewPayloadCodecInspector(db *Database) *PayloadCodecInspector {
	return &PayloadCodecInspector{db: db}
}

var _ application.PayloadCodecInspector = (*PayloadCodecInspector)(nil)

// InspectPayloadCodec returns aggregate codec state without exposing payloads.
func (i *PayloadCodecInspector) InspectPayloadCodec(ctx context.Context) (_ application.PayloadCodecState, err error) {
	db, err := i.db.openReadOnly(ctx)
	if err != nil {
		return application.PayloadCodecState{}, xerrors.Errorf("open store for payload codec inspection: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("close payload codec inspection: %w", closeErr)
		}
	}()
	var state application.PayloadCodecState
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('events') WHERE name='body_codec')`).Scan(&exists); err != nil {
		return state, xerrors.Errorf("inspect payload codec metadata columns: %w", err)
	}
	if exists == 0 {
		return state, nil
	}
	state.MetadataAvailable = true
	if err := db.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton=1`).Scan(&state.MinimumReader); err != nil {
		return state, xerrors.Errorf("read payload codec reader boundary: %w", err)
	}
	queries := []struct {
		field *int64
		query string
	}{
		{&state.EventBodyZstd, `SELECT COUNT(*) FROM events WHERE body_codec='zstd'`},
		{&state.AuditCommandZstd, `SELECT COUNT(*) FROM command_audits WHERE command_codec='zstd'`},
		{&state.AuditInputZstd, `SELECT COUNT(*) FROM command_audits WHERE input_codec='zstd'`},
		{&state.AuditOutputZstd, `SELECT COUNT(*) FROM command_audits WHERE output_codec='zstd'`},
	}
	for _, item := range queries {
		if err := db.QueryRowContext(ctx, item.query).Scan(item.field); err != nil {
			return state, xerrors.Errorf("count compressed payloads: %w", err)
		}
	}
	return state, nil
}
