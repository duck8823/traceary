package types

import (
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventBodyExtent describes persisted event-body size and truncation facts.
// Unknown facts remain absent instead of being represented as zero or false.
type EventBodyExtent struct {
	originalBytes       domtypes.Optional[int]
	storedBytes         int
	ingestTruncated     domtypes.Optional[bool]
	storageTruncated    domtypes.Optional[bool]
	bodyMetadataVersion domtypes.Optional[int]
}

// EventBodyExtentOf creates persisted event-body extent metadata.
func EventBodyExtentOf(
	originalBytes domtypes.Optional[int],
	storedBytes int,
	ingestTruncated domtypes.Optional[bool],
	storageTruncated domtypes.Optional[bool],
	bodyMetadataVersion domtypes.Optional[int],
) (EventBodyExtent, error) {
	if storedBytes < 0 {
		return EventBodyExtent{}, xerrors.Errorf("stored body bytes must be greater than or equal to 0")
	}
	if value, ok := originalBytes.Value(); ok && value < 0 {
		return EventBodyExtent{}, xerrors.Errorf("original body bytes must be greater than or equal to 0")
	}
	if value, ok := bodyMetadataVersion.Value(); ok && value < 0 {
		return EventBodyExtent{}, xerrors.Errorf("body metadata version must be greater than or equal to 0")
	}
	return EventBodyExtent{
		originalBytes:       originalBytes,
		storedBytes:         storedBytes,
		ingestTruncated:     ingestTruncated,
		storageTruncated:    storageTruncated,
		bodyMetadataVersion: bodyMetadataVersion,
	}, nil
}

// OriginalBytes returns the original payload size when the ingest path knew it.
func (e EventBodyExtent) OriginalBytes() domtypes.Optional[int] { return e.originalBytes }

// StoredBytes returns the persisted UTF-8 byte count.
func (e EventBodyExtent) StoredBytes() int { return e.storedBytes }

// IngestTruncated reports known ingestion truncation.
func (e EventBodyExtent) IngestTruncated() domtypes.Optional[bool] { return e.ingestTruncated }

// StorageTruncated reports known storage-policy truncation.
func (e EventBodyExtent) StorageTruncated() domtypes.Optional[bool] { return e.storageTruncated }

// BodyMetadataVersion returns the internal extraction version when known.
func (e EventBodyExtent) BodyMetadataVersion() domtypes.Optional[int] {
	return e.bodyMetadataVersion
}

// CommandAuditMetadata contains body-free command outcome facts.
type CommandAuditMetadata struct {
	exitCode domtypes.Optional[int]
	failed   bool
}

// CommandAuditMetadataOf creates command outcome metadata.
func CommandAuditMetadataOf(exitCode domtypes.Optional[int], failed bool) CommandAuditMetadata {
	return CommandAuditMetadata{exitCode: exitCode, failed: failed}
}

// ExitCode returns the recorded command exit code when present.
func (m CommandAuditMetadata) ExitCode() domtypes.Optional[int] { return m.exitCode }

// Failed reports a structural or recorded command failure.
func (m CommandAuditMetadata) Failed() bool { return m.failed }

// EventMetadata is a body-free read model for event inspection surfaces.
type EventMetadata struct {
	eventID      domtypes.EventID
	kind         domtypes.EventKind
	client       domtypes.Client
	agent        domtypes.Agent
	sessionID    domtypes.SessionID
	workspace    domtypes.Workspace
	sourceHook   string
	createdAt    time.Time
	bodyExtent   EventBodyExtent
	commandAudit domtypes.Optional[CommandAuditMetadata]
}

// EventMetadataOf creates a body-free event read model.
func EventMetadataOf(
	eventID domtypes.EventID,
	kind domtypes.EventKind,
	client domtypes.Client,
	agent domtypes.Agent,
	sessionID domtypes.SessionID,
	workspace domtypes.Workspace,
	sourceHook string,
	createdAt time.Time,
	bodyExtent EventBodyExtent,
	commandAudit domtypes.Optional[CommandAuditMetadata],
) (EventMetadata, error) {
	if createdAt.IsZero() {
		return EventMetadata{}, xerrors.Errorf("created at must not be zero")
	}
	return EventMetadata{
		eventID:      eventID,
		kind:         kind,
		client:       client,
		agent:        agent,
		sessionID:    sessionID,
		workspace:    workspace,
		sourceHook:   sourceHook,
		createdAt:    createdAt,
		bodyExtent:   bodyExtent,
		commandAudit: commandAudit,
	}, nil
}

// EventID returns the event identity.
func (m EventMetadata) EventID() domtypes.EventID { return m.eventID }

// Kind returns the event kind.
func (m EventMetadata) Kind() domtypes.EventKind { return m.kind }

// Client returns the event client.
func (m EventMetadata) Client() domtypes.Client { return m.client }

// Agent returns the event agent.
func (m EventMetadata) Agent() domtypes.Agent { return m.agent }

// SessionID returns the session identity.
func (m EventMetadata) SessionID() domtypes.SessionID { return m.sessionID }

// Workspace returns the event workspace.
func (m EventMetadata) Workspace() domtypes.Workspace { return m.workspace }

// SourceHook returns the source hook attribution.
func (m EventMetadata) SourceHook() string { return m.sourceHook }

// CreatedAt returns the event timestamp.
func (m EventMetadata) CreatedAt() time.Time { return m.createdAt }

// BodyExtent returns persisted body size and truncation facts.
func (m EventMetadata) BodyExtent() EventBodyExtent { return m.bodyExtent }

// CommandAudit returns body-free command outcome metadata when linked.
func (m EventMetadata) CommandAudit() domtypes.Optional[CommandAuditMetadata] {
	return m.commandAudit
}
