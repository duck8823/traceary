package types

import (
	"time"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// RecentCommandSummary is a body-safe handoff projection with enough
// provenance for consumers to decide whether explicit detail retrieval is
// necessary.
type RecentCommandSummary struct {
	eventID           domtypes.EventID
	summary           string
	returnedBytes     int
	responseTruncated bool
	bodyExtent        EventBodyExtent
	createdAt         time.Time
}

// RecentCommandSummaryOf creates a structured recent-command projection.
func RecentCommandSummaryOf(
	eventID domtypes.EventID,
	summary string,
	responseTruncated bool,
	bodyExtent EventBodyExtent,
	createdAt time.Time,
) (RecentCommandSummary, error) {
	if createdAt.IsZero() {
		return RecentCommandSummary{}, xerrors.Errorf("created at must not be zero")
	}
	return RecentCommandSummary{
		eventID: eventID, summary: summary, returnedBytes: len(summary),
		responseTruncated: responseTruncated, bodyExtent: bodyExtent, createdAt: createdAt,
	}, nil
}

// EventID returns the event identity.
func (s RecentCommandSummary) EventID() domtypes.EventID { return s.eventID }

// Summary returns the body-safe, single-line command summary.
func (s RecentCommandSummary) Summary() string { return s.summary }

// ReturnedBytes returns the UTF-8 byte count included in Summary.
func (s RecentCommandSummary) ReturnedBytes() int { return s.returnedBytes }

// ResponseTruncated reports whether the handoff response omitted stored body content.
func (s RecentCommandSummary) ResponseTruncated() bool { return s.responseTruncated }

// BodyExtent returns persisted body size and truncation facts.
func (s RecentCommandSummary) BodyExtent() EventBodyExtent { return s.bodyExtent }

// CreatedAt returns the event timestamp.
func (s RecentCommandSummary) CreatedAt() time.Time { return s.createdAt }
