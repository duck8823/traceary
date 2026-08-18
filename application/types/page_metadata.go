package types

// StorePageMetadata is the O(1) large-store doctor snapshot: pragma page
// accounting plus the search-projection generation singleton. It does not
// include dbstat objects, payload classes, or event/audit bodies.
type StorePageMetadata struct {
	PageSizeBytes          int64
	PageCount              int64
	FreePages              int64
	DatabaseBytes          int64
	ReclaimableBytes       int64
	ProjectionPresent      bool
	ProjectionGenerationID string
	ProjectionState        string
	ProjectionPhase        string
}
