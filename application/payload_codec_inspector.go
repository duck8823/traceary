package application

import "context"

// PayloadCodecState is the bounded, content-free status surface used by
// doctor. Counts are per physical lane and do not expose payload values.
// The counters are of non-identity rows rather than of zstd rows specifically:
// the store keeps one counter per lane regardless of codec, so a codec added
// later, or one written by external SQL, is included without being named.
type PayloadCodecState struct {
	MetadataAvailable       bool
	MinimumReader           int
	CompatibilityMode       string
	CompatibilityState      string
	EventBodyNonIdentity    int64
	AuditCommandNonIdentity int64
	AuditInputNonIdentity   int64
	AuditOutputNonIdentity  int64
}

// PayloadCodecInspector reports the store's payload representation without
// applying migrations or opening a read-write connection.
type PayloadCodecInspector interface {
	InspectPayloadCodec(context.Context) (PayloadCodecState, error)
}
