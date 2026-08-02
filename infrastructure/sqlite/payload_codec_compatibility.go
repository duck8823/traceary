package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"
)

type payloadCodecCompatibilityQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// globalNonIdentityPayload uses only constant-size counters on the rewritten
// schema and only the historical partial indexes on an already-applied v36
// schema. Unknown or inconsistent evidence fails closed with an error.
func globalNonIdentityPayload(ctx context.Context, q payloadCodecCompatibilityQueryer) (bool, error) {
	var mode, state string
	var eventBody, command, input, output int64
	if err := q.QueryRowContext(ctx, `SELECT mode,state,event_body_nonidentity,audit_command_nonidentity,audit_input_nonidentity,audit_output_nonidentity FROM payload_codec_compatibility_state WHERE singleton=1`).Scan(&mode, &state, &eventBody, &command, &input, &output); err != nil {
		return false, xerrors.Errorf("inspect payload codec compatibility state: %w", err)
	}
	if state != "valid" || eventBody < 0 || command < 0 || input < 0 || output < 0 {
		return false, xerrors.New("payload codec compatibility state is invalid")
	}
	switch mode {
	case "counter":
		// Inspect lanes independently: valid SQLite INTEGER counters can be
		// MaxInt64, so summing them could wrap and falsely report identity-only.
		return eventBody > 0 || command > 0 || input > 0 || output > 0, nil
	case "legacy_index":
		var found int
		if err := q.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM events INDEXED BY idx_events_nonidentity_body_codec WHERE body_codec <> 'identity') OR
EXISTS(SELECT 1 FROM command_audits INDEXED BY idx_command_audits_nonidentity_command_codec WHERE command_codec <> 'identity') OR
EXISTS(SELECT 1 FROM command_audits INDEXED BY idx_command_audits_nonidentity_input_codec WHERE input_codec <> 'identity') OR
EXISTS(SELECT 1 FROM command_audits INDEXED BY idx_command_audits_nonidentity_output_codec WHERE output_codec <> 'identity')`).Scan(&found); err != nil {
			return false, xerrors.Errorf("inspect legacy indexed payload codecs: %w", err)
		}
		return found != 0, nil
	default:
		return false, xerrors.Errorf("unknown payload codec compatibility mode %q", mode)
	}
}
