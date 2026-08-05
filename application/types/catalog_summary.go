package types

// CatalogSummaryRebuildResult is aggregate-only evidence for one bounded run.
// The durable per-segment checkpoint remains private to the adapter.
type CatalogSummaryRebuildResult struct {
	Processed int
	Inserted  int
	Existing  int
	Remaining int
	Done      bool
}

// CatalogSummaryDiagnostics deliberately contains no segment identity, key
// identifier, token, digest, basename, timestamp, or payload field.
type CatalogSummaryDiagnostics struct {
	BoundSegments      int64
	ReconciledSegments int64
	ExactKindRows      int64
	BloomKindRows      int64
	UnknownKindSlots   int64
	SessionRows        int64
	UnitCount          int64
	AuditCount         int64
	StoredBytes        int64
	SummaryBytes       int64
	HotRanges          int64
	ReservedRanges     int64
	SegmentRanges      int64
	DriftCount         int64
}
