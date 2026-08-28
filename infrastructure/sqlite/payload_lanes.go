package sqlite

import "bytes"

// payloadCodecCompatibilityCounterMode is the only compatibility mode whose
// triggers tolerate a codec transition (migration 036 / 043).
const payloadCodecCompatibilityCounterMode = "counter"

// backfillLane is one (table, field) unit the codec recipe rewrites.
type backfillLane struct {
	Table       string
	Field       string // logical field name used in errors and result naming
	Column      string // physical TEXT/BLOB column
	CodecPrefix string // prefix of the five codec metadata columns
	PKColumn    string // human-facing row key column (id / event_id)
	Ord         int    // stable order within a rowid
}

// backfillLanes is the full recipe. Order is the stable walk order: body,
// then the three audit texts.
var backfillLanes = []backfillLane{
	{Table: "events", Field: "body", Column: "body", CodecPrefix: "body", PKColumn: "id", Ord: 0},
	{Table: "command_audits", Field: "command", Column: "command_text", CodecPrefix: "command", PKColumn: "event_id", Ord: 1},
	{Table: "command_audits", Field: "input", Column: "input_text", CodecPrefix: "input", PKColumn: "event_id", Ord: 2},
	{Table: "command_audits", Field: "output", Column: "output_text", CodecPrefix: "output", PKColumn: "event_id", Ord: 3},
}

// commandAuditLanes returns the three command_audits lanes of the recipe.
func commandAuditLanes() []backfillLane {
	out := make([]backfillLane, 0, 3)
	for _, lane := range backfillLanes {
		if lane.Table == "command_audits" {
			out = append(out, lane)
		}
	}
	return out
}

// storedBodyArg preserves the column affinity each codec is stored with:
// identity as TEXT (what every other writer binds), zstd as BLOB.
func storedBodyArg(payload encodedPayload) any {
	if payload.Codec == payloadCodecIdentity {
		return string(payload.Bytes)
	}
	return payload.Bytes
}

func payloadMetadataCount(r payloadRow) int {
	n := 0
	for _, valid := range []bool{r.Codec.Valid, r.FormatVersion.Valid, r.PlaintextBytes.Valid, r.StoredBytes.Valid, r.SHA256.Valid} {
		if valid {
			n++
		}
	}
	return n
}

func backfillRowAlreadyMatches(r payloadRow, target encodedPayload) bool {
	if payloadMetadataCount(r) != 5 {
		return false
	}
	if !r.Codec.Valid || r.Codec.String != target.Codec {
		return false
	}
	if !r.FormatVersion.Valid || int(r.FormatVersion.Int64) != target.FormatVersion {
		return false
	}
	if !r.PlaintextBytes.Valid || r.PlaintextBytes.Int64 != target.PlaintextBytes {
		return false
	}
	if !r.StoredBytes.Valid || r.StoredBytes.Int64 != target.StoredBytes {
		return false
	}
	if !r.SHA256.Valid || r.SHA256.String != target.SHA256 {
		return false
	}
	return bytes.Equal(r.Stored, target.Bytes)
}
