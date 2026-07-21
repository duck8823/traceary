package types

import (
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventBodyPreview is a bounded content read model for summary-producing
// surfaces. It never contains more body runes than the query's preview limit.
type EventBodyPreview struct {
	eventID          domtypes.EventID
	body             string
	storedBytes      int
	originalBytes    domtypes.Optional[int]
	ingestTruncated  domtypes.Optional[bool]
	storageTruncated domtypes.Optional[bool]
	createdAt        time.Time
}

// EventBodyPreviewOf creates a bounded event-body preview.
func EventBodyPreviewOf(
	eventID domtypes.EventID,
	body string,
	storedBytes int,
	originalBytes domtypes.Optional[int],
	ingestTruncated domtypes.Optional[bool],
	storageTruncated domtypes.Optional[bool],
	createdAt time.Time,
) (EventBodyPreview, error) {
	if storedBytes < 0 {
		return EventBodyPreview{}, xerrors.Errorf("stored body bytes must be greater than or equal to 0")
	}
	if value, ok := originalBytes.Value(); ok && value < 0 {
		return EventBodyPreview{}, xerrors.Errorf("original body bytes must be greater than or equal to 0")
	}
	if createdAt.IsZero() {
		return EventBodyPreview{}, xerrors.Errorf("created at must not be zero")
	}
	return EventBodyPreview{
		eventID: eventID, body: body, storedBytes: storedBytes,
		originalBytes: originalBytes, ingestTruncated: ingestTruncated,
		storageTruncated: storageTruncated, createdAt: createdAt,
	}, nil
}

// EventID returns the event identity.
func (p EventBodyPreview) EventID() domtypes.EventID { return p.eventID }

// Body returns the bounded body prefix.
func (p EventBodyPreview) Body() string { return p.body }

// StoredBytes returns the persisted UTF-8 byte count.
func (p EventBodyPreview) StoredBytes() int { return p.storedBytes }

// OriginalBytes returns the original payload size when known.
func (p EventBodyPreview) OriginalBytes() domtypes.Optional[int] { return p.originalBytes }

// IngestTruncated reports known ingestion truncation.
func (p EventBodyPreview) IngestTruncated() domtypes.Optional[bool] { return p.ingestTruncated }

// StorageTruncated reports known storage-policy truncation.
func (p EventBodyPreview) StorageTruncated() domtypes.Optional[bool] { return p.storageTruncated }

// CreatedAt returns the event timestamp.
func (p EventBodyPreview) CreatedAt() time.Time { return p.createdAt }
