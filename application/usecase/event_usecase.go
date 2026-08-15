package usecase

import (
	"context"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// EventUsecase consolidates event recording and query operations.
type EventUsecase interface {
	// Log records a log event. Zero-value kind defaults to note.
	// logCfg carries redaction settings; its zero value is a
	// pass-through for non-transcript kinds. For EventKindTranscript
	// the implementation applies built-in redactors + the caller's
	// extra patterns before persisting so no log-ingest surface has
	// to re-implement that policy in the presentation layer.
	// The write result reports whether a row was inserted. On an exact
	// hook redelivery Event() carries the canonical existing id.
	Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, logCfg apptypes.LogRedaction) (apptypes.EventWriteResult, error)

	// DeleteTranscript removes one previously logged transcript event.
	// Missing or non-transcript ids return nil so a failed supersede can
	// fail open without aborting the newer write.
	DeleteTranscript(ctx context.Context, eventID types.EventID) error

	// Audit records a command execution audit event. The AuditInput value
	// object carries the command, attribution, exit code, and structural
	// failure flag; auditCfg carries the redaction policy. The write
	// result reports insert vs exact redelivery, same as Log.
	Audit(ctx context.Context, in apptypes.AuditInput, auditCfg apptypes.AuditRedaction) (apptypes.EventWriteResult, *model.CommandAudit, error)

	// Search performs full-text search across events.
	Search(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error)

	// List returns events in descending time order.
	List(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error)

	// ListWindow returns every event matching the criteria whose created_at
	// falls in [From, To) under a single read snapshot, so concurrent writers
	// cannot cause the paged scan to drop events. criteria.Limit() controls
	// the per-page size for the internal scan; criteria.Offset() is ignored.
	ListWindow(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error)

	// Show returns the details for a single event.
	Show(ctx context.Context, eventID types.EventID) (apptypes.EventDetails, error)

	// Context returns recent events for the given context.
	Context(ctx context.Context, criteria apptypes.EventContextCriteria) ([]*model.Event, error)

	// Timeline returns work blocks separated by idle gaps.
	Timeline(ctx context.Context, criteria apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error)

	// HydrateCommandAudits decodes selected codec-managed command audit
	// payloads onto already-listed events. Listing joins only fixed-size
	// metadata; call this when a reader needs command/input/output plaintext.
	HydrateCommandAudits(ctx context.Context, events []*model.Event, fields queryservice.CommandAuditPayloadFields) error
}
