package application

import "context"

// PayloadCodecState is the bounded, content-free status surface used by
// doctor. Counts are per physical lane and do not expose payload values.
type PayloadCodecState struct {
	MetadataAvailable  bool
	MinimumReader      int
	CompatibilityMode  string
	CompatibilityState string
	EventBodyZstd      int64
	AuditCommandZstd   int64
	AuditInputZstd     int64
	AuditOutputZstd    int64
}

// PayloadCodecInspector reports the store's payload representation without
// applying migrations or opening a read-write connection.
type PayloadCodecInspector interface {
	InspectPayloadCodec(context.Context) (PayloadCodecState, error)
}
