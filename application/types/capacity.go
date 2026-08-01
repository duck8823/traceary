package types

// CapacityReport is a metadata-only snapshot of SQLite allocation.
type CapacityReport struct {
	SchemaVersion   string                 `json:"schema_version"`
	Evidence        CapacityEvidence       `json:"evidence"`
	PayloadEvidence CapacityEvidence       `json:"payload_evidence"`
	PageSizeBytes   int64                  `json:"page_size_bytes"`
	PageCount       int64                  `json:"page_count"`
	DatabaseBytes   int64                  `json:"database_bytes"`
	FreePages       int64                  `json:"free_pages"`
	FreeBytes       int64                  `json:"free_bytes"`
	WALBytes        int64                  `json:"wal_bytes"`
	Objects         []CapacityObject       `json:"objects"`
	PayloadClasses  []CapacityPayloadClass `json:"payload_classes"`
}

// CapacityEvidence states whether the optional dbstat attribution is available.
type CapacityEvidence struct {
	Status string `json:"status"`
	Method string `json:"method"`
	Reason string `json:"reason,omitempty"`
}

// CapacityObject attributes allocated pages to a table or index name.
type CapacityObject struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Pages int64  `json:"pages"`
	Bytes int64  `json:"bytes"`
}

// CapacityPayloadClass aggregates body sizes into non-identifying buckets.
type CapacityPayloadClass struct {
	Name  string `json:"name"`
	Rows  int64  `json:"rows"`
	Bytes int64  `json:"bytes"`
}
