package queryservice

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
)

// CommandAuditPayloadFields selects which codec-managed audit columns to decode.
// Listing joins only fixed-size metadata; consumers request payloads explicitly.
type CommandAuditPayloadFields struct {
	Command bool
	Input   bool
	Output  bool
}

// CommandOnlyPayload is the MCP list/search body surface: command line only.
func CommandOnlyPayload() CommandAuditPayloadFields {
	return CommandAuditPayloadFields{Command: true}
}

// FullCommandAuditPayload is the sensitive-path surface: command + input + output.
func FullCommandAuditPayload() CommandAuditPayloadFields {
	return CommandAuditPayloadFields{Command: true, Input: true, Output: true}
}

// CommandAuditPayloadQueryService decodes codec-managed audit payloads onto
// already-listed events. Implementations must use the same hydrateAuditPayload
// path as GetDetails so non-identity codecs, integrity checks, and size limits
// apply.
type CommandAuditPayloadQueryService interface {
	// HydrateCommandAudits mutates events in place, replacing attached
	// metadata-only audits with decoded payload fields selected by fields.
	// Events without an attached audit are skipped. Empty fields is a no-op.
	HydrateCommandAudits(ctx context.Context, events []*model.Event, fields CommandAuditPayloadFields) error
}
